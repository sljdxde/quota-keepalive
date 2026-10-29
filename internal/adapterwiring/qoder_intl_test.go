package adapterwiring

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func intlAccounts() []map[string]any {
	return []map[string]any{
		{
			"id": "i_1", "displayName": "Intl One", "maskedIdentity": "****1111",
			"source": map[string]any{"id": "is_1", "kind": "managed", "health": "healthy"},
		},
		{
			"id": "i_2", "displayName": "Intl Two", "maskedIdentity": "****2222",
			"source": map[string]any{"id": "is_2", "kind": "managed", "health": "healthy"},
		},
	}
}

func newIntlTarget(t *testing.T, accounts []map[string]any, claimable bool, seen *[]string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/api/accounts", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"accounts": accounts})
	})
	mux.HandleFunc("/api/activity", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"activityId": "intl-act", "claimable": claimable,
		})
	})
	mux.HandleFunc("/api/checkin", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			AccountID string `json:"accountId"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if seen != nil {
			*seen = append(*seen, req.AccountID)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success", "points": 50, "accountId": req.AccountID,
		})
	})

	return httptest.NewServer(mux)
}

func TestIntlDiscoverAndCheckin(t *testing.T) {
	var seen []string
	target := newIntlTarget(t, intlAccounts(), true, &seen)
	defer target.Close()
	h := launchAdapter(t, target.URL, "qoder")

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

	for _, id := range []string{"i_1", "i_2"} {
		if _, err := h.CallWith(context.Background(), "checkin.run",
			map[string]any{"accountId": id}); err != nil {
			t.Errorf("账号 %s 签到失败: %v", id, err)
		}
	}
	if len(seen) != 2 {
		t.Errorf("应完成 2 个账号签到，实际 %d", len(seen))
	}
}

func TestIntlActivityNotClaimable(t *testing.T) {
	target := newIntlTarget(t, intlAccounts(), false, nil)
	defer target.Close()
	h := launchAdapter(t, target.URL, "qoder")

	result, err := h.CallWith(context.Background(), "checkin.run",
		map[string]any{"accountId": "i_1"})
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(result, &d); err != nil {
		t.Fatal(err)
	}
	if d.Status != "unavailable" {
		t.Errorf("活动不可领取应显式 unavailable，实际 %s", d.Status)
	}
}

func TestIntlIndependentFromQoderCN(t *testing.T) {
	var intlSeen, cnSeen []string
	intlTarget := newIntlTarget(t, intlAccounts(), true, &intlSeen)
	defer intlTarget.Close()
	cnTarget := newQoderTarget(t, qoderAccounts(), "cn-act", &cnSeen)
	defer cnTarget.Close()

	intlHost := launchAdapter(t, intlTarget.URL, "qoder")
	cnHost := launchAdapter(t, cnTarget.URL, "qoder-cn")

	intlResult, _ := intlHost.CallWith(context.Background(), "checkin.run",
		map[string]any{"accountId": "i_1"})
	cnResult, _ := cnHost.CallWith(context.Background(), "checkin.run",
		map[string]any{"accountId": "q_1"})

	var i, c struct {
		Points int `json:"points"`
	}
	_ = json.Unmarshal(intlResult, &i)
	_ = json.Unmarshal(cnResult, &c)
	if i.Points != 50 || c.Points != 100 {
		t.Errorf("国际版与 CN 应独立返回额度，实际 intl=%d cn=%d", i.Points, c.Points)
	}
}
