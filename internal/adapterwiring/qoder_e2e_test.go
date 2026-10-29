package adapterwiring

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"qka/internal/adapterhost"
	"qka/internal/capabilities"
	"qka/internal/engine"
)

func qoderAccounts() []map[string]any {
	return []map[string]any{
		{
			"id": "q_1", "displayName": "Qoder One", "maskedIdentity": "****4321",
			"source": map[string]any{"id": "qs_1", "kind": "managed", "health": "healthy"},
		},
		{
			"id": "q_2", "displayName": "Qoder Two", "maskedIdentity": "****8765",
			"source": map[string]any{"id": "qs_2", "kind": "managed", "health": "healthy"},
		},
	}
}

// newQoderTarget 启动 Qoder CN 假目标；activityId 现读现用，checkin 记录收到的活动 id。
func newQoderTarget(t *testing.T, accounts []map[string]any, activityId string, seen *[]string) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	mux := http.NewServeMux()

	mux.HandleFunc("/api/accounts", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"accounts": accounts})
	})
	mux.HandleFunc("/api/activity", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"activityId": activityId})
	})
	mux.HandleFunc("/api/checkin", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			AccountID  string `json:"accountId"`
			ActivityID string `json:"activityId"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if seen != nil {
			*seen = append(*seen, req.ActivityID)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success", "points": 100, "accountId": req.AccountID,
		})
	})

	return httptest.NewServer(mux)
}

func launchAdapter(t *testing.T, targetURL, adapterName string) *adapterhost.Host {
	t.Helper()
	u, _ := parseURL(targetURL)
	fetch := capabilities.NewHTTPFetch(
		capabilities.AllowedHostsFromManifest([]string{u.Host}),
		capabilities.GrantFromNeeds([]string{"http.outbound"}))

	st, err := storeOpenTemp(t)
	if err != nil {
		t.Fatal(err)
	}

	mux := NewHookMux()
	RegisterHTTPFetch(mux, fetch)
	RegisterLogWrite(mux, st.DB())
	mux.Init["baseURL"] = targetURL

	adapterDir := filepath.Join("..", "..", "adapters", adapterName)
	h, err := adapterhost.LaunchWith(context.Background(), "bun", adapterDir, mux)
	if err != nil {
		t.Fatalf("LaunchWith 失败: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })
	return h
}

func TestQoderDiscoverAndCheckin(t *testing.T) {
	var seen []string
	target := newQoderTarget(t, qoderAccounts(), "act-100", &seen)
	defer target.Close()
	h := launchAdapter(t, target.URL, "qoder-cn")

	// 发现。
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

	// 两个账号分别签到。
	for _, id := range []string{"q_1", "q_2"} {
		if _, err := h.CallWith(context.Background(), "checkin.run",
			map[string]any{"accountId": id}); err != nil {
			t.Errorf("账号 %s 签到失败: %v", id, err)
		}
	}
	if len(seen) != 2 || seen[0] != "act-100" {
		t.Errorf("签到应使用当前活动 id act-100，实际 %v", seen)
	}
}

func TestQoderActivityIdReadLive(t *testing.T) {
	// 用与"旧活动"不同的活动 id 启动：验证现读现用，不硬编码。
	var seen []string
	target := newQoderTarget(t, qoderAccounts(), "act-999-new", &seen)
	defer target.Close()
	h := launchAdapter(t, target.URL, "qoder-cn")

	if _, err := h.CallWith(context.Background(), "checkin.run",
		map[string]any{"accountId": "q_1"}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || seen[0] != "act-999-new" {
		t.Errorf("活动 id 变化时应使用新活动 id，实际 %v", seen)
	}
}

func TestQoderCheckinPersistsToStore(t *testing.T) {
	var seen []string
	target := newQoderTarget(t, qoderAccounts(), "act-1", &seen)
	defer target.Close()

	st, err := storeOpenTemp(t)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	u, _ := parseURL(target.URL)
	fetch := capabilities.NewHTTPFetch(
		capabilities.AllowedHostsFromManifest([]string{u.Host}),
		capabilities.GrantFromNeeds([]string{"http.outbound"}))
	mux := NewHookMux()
	RegisterHTTPFetch(mux, fetch)
	RegisterLogWrite(mux, st.DB())
	mux.Init["baseURL"] = target.URL

	h, err := adapterhost.LaunchWith(context.Background(), "bun",
		filepath.Join("..", "..", "adapters", "qoder-cn"), mux)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })

	now := func() time.Time { return beijingAt(14) }
	eng := engine.New(st.DB(), now, "host")

	key := engine.Key{Target: "qoder-cn", AccountID: "q_1", Operation: engine.Checkin}
	obs, err := eng.Run(context.Background(), key, CheckinExecutor(h))
	if err != nil {
		t.Fatal(err)
	}
	if obs.State != engine.SignedConfirmed {
		t.Fatalf("午后成功应 confirmed，实际 %s", obs.State)
	}

	var state string
	if err := st.DB().QueryRowContext(context.Background(),
		`SELECT state FROM daily_ops WHERE target = ? AND account_id = ?`,
		"qoder-cn", "q_1").Scan(&state); err != nil {
		t.Fatalf("Qoder 签到未落库: %v", err)
	}
	if state != "signed_confirmed" {
		t.Errorf("落库状态错误: %s", state)
	}
}

func TestQoderAndWorkbuddyIndependent(t *testing.T) {
	// 两个目标、两个适配器，在同一调用中各自独立执行。
	var qoderSeen []string
	qoderTarget := newQoderTarget(t, qoderAccounts(), "act-1", &qoderSeen)
	defer qoderTarget.Close()
	workbuddyTarget := newFakeTarget(t, scenario{accounts: healthyAccounts()})
	defer workbuddyTarget.Close()

	qoderHost := launchAdapter(t, qoderTarget.URL, "qoder-cn")
	workbuddyHost := launchAdapter(t, workbuddyTarget.URL, "workbuddy-cn")

	qoderResult, err := qoderHost.CallWith(context.Background(), "checkin.run",
		map[string]any{"accountId": "q_1"})
	if err != nil {
		t.Fatalf("Qoder 失败: %v", err)
	}
	workbuddyResult, err := workbuddyHost.CallWith(context.Background(), "checkin.run",
		map[string]any{"accountId": "u_1"})
	if err != nil {
		t.Fatalf("WorkBuddy 失败: %v", err)
	}

	var q struct {
		Points int `json:"points"`
	}
	var w struct {
		Points int `json:"points"`
	}
	_ = json.Unmarshal(qoderResult, &q)
	_ = json.Unmarshal(workbuddyResult, &w)
	if q.Points != 100 || w.Points != 200 {
		t.Errorf("两适配器应各自返回独立结果，实际 qoder=%d workbuddy=%d", q.Points, w.Points)
	}
}
