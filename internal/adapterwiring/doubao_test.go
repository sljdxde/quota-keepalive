package adapterwiring

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"qka/internal/engine"
)

func doubaoAccounts() []map[string]any {
	return []map[string]any{
		{
			"id": "d_1", "displayName": "Doubao One", "maskedIdentity": "****3333",
			"source": map[string]any{"id": "ds_1", "kind": "managed", "health": "healthy"},
		},
		{
			"id": "d_2", "displayName": "Doubao Two", "maskedIdentity": "****4444",
			"source": map[string]any{"id": "ds_2", "kind": "managed", "health": "healthy"},
		},
	}
}

// newDoubaoTarget 豆包工作假目标：首次成功 200 POINT，重复返回 710010702。
func newDoubaoTarget(t *testing.T, accounts []map[string]any, requests *int) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	checked := map[string]bool{}
	mux := http.NewServeMux()

	mux.HandleFunc("/api/accounts", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"accounts": accounts})
	})
	mux.HandleFunc("/api/checkin", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			AccountID string `json:"accountId"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		defer mu.Unlock()
		if requests != nil {
			*requests++
		}
		if checked[req.AccountID] {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": "710010702", "message": "system error",
			})
			return
		}
		checked[req.AccountID] = true
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success", "points": 200,
		})
	})

	return httptest.NewServer(mux)
}

func TestDoubaoDiscover(t *testing.T) {
	target := newDoubaoTarget(t, doubaoAccounts(), nil)
	defer target.Close()
	h := launchAdapter(t, target.URL, "doubao-work")

	disc, err := h.Call(context.Background(), "accounts.discover")
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Status   string `json:"status"`
		Accounts []struct {
			ID string `json:"id"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(disc, &d); err != nil {
		t.Fatal(err)
	}
	if d.Status != "ok" || len(d.Accounts) != 2 {
		t.Fatalf("发现错误: %+v", d)
	}
}

func TestDoubaoFirstCheckinSuccess(t *testing.T) {
	target := newDoubaoTarget(t, doubaoAccounts(), nil)
	defer target.Close()
	h := launchAdapter(t, target.URL, "doubao-work")

	result, err := h.CallWith(context.Background(), "checkin.run",
		map[string]any{"accountId": "d_1"})
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Status string `json:"status"`
		Points int    `json:"points"`
	}
	if err := json.Unmarshal(result, &d); err != nil {
		t.Fatal(err)
	}
	if d.Status != "success" || d.Points != 200 {
		t.Errorf("首次签到应成功 +200 POINT，实际 %+v", d)
	}
}

func TestDoubaoRepeatReturnsBusinessCode(t *testing.T) {
	target := newDoubaoTarget(t, doubaoAccounts(), nil)
	defer target.Close()
	h := launchAdapter(t, target.URL, "doubao-work")

	// 首次成功。
	_, _ = h.CallWith(context.Background(), "checkin.run",
		map[string]any{"accountId": "d_1"})
	// 重复：通用业务码，不宣称已签到。
	result, err := h.CallWith(context.Background(), "checkin.run",
		map[string]any{"accountId": "d_1"})
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Status string `json:"status"`
		Code   string `json:"code"`
	}
	_ = json.Unmarshal(result, &d)
	if d.Code != "710010702" {
		t.Errorf("重复应返回 710010702，实际 %+v", d)
	}
}

func TestDoubaoLocalSuccessGatePreventsSecondClaim(t *testing.T) {
	var requests int
	target := newDoubaoTarget(t, doubaoAccounts(), &requests)
	defer target.Close()

	now := func() time.Time { return beijingAt(14) }
	env := setupWiredAt(t, target.URL, now)

	key := engine.Key{Target: "doubao-work", AccountID: "d_1", Operation: engine.Checkin}

	// 首次 Run：执行并成功。
	obs, err := env.eng.Run(context.Background(), key, CheckinExecutor(env.host))
	if err != nil {
		t.Fatal(err)
	}
	if obs.State != engine.SignedConfirmed {
		t.Fatalf("首次午后成功应 confirmed，实际 %s", obs.State)
	}
	firstRequests := requests

	// 再次 Run：本地已终态，不执行、不发领取。
	obs2, err := env.eng.Run(context.Background(), key, CheckinExecutor(env.host))
	if err != nil {
		t.Fatal(err)
	}
	if obs2.Executed {
		t.Error("本地成功门后不应再次执行领取")
	}
	if requests != firstRequests {
		t.Errorf("成功后不应再发领取请求，首次 %d 次，实际 %d 次", firstRequests, requests)
	}
}
