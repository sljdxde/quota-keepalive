package adapterwiring

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"qka/internal/adapterhost"
	"qka/internal/capabilities"
	"qka/internal/store"
)

// scenario 描述受控假目标应呈现的状态。
type scenario struct {
	accountsStatus int              // /api/accounts 的状态码（0 表示 200）
	accounts       []map[string]any // 账号列表
	checkinFail    map[string]bool  // 哪些账号签到失败
}

func healthyAccounts() []map[string]any {
	return []map[string]any{
		{
			"id": "u_1", "displayName": "User One", "maskedIdentity": "****1234",
			"source": map[string]any{"id": "s_1", "kind": "managed", "health": "healthy"},
		},
		{
			"id": "u_2", "displayName": "User Two", "maskedIdentity": "****5678",
			"source": map[string]any{"id": "s_2", "kind": "managed", "health": "healthy"},
		},
	}
}

func newFakeTarget(t *testing.T, sc scenario) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/api/accounts", func(w http.ResponseWriter, r *http.Request) {
		if sc.accountsStatus != 0 {
			w.WriteHeader(sc.accountsStatus)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"accounts": sc.accounts})
	})

	mux.HandleFunc("/api/checkin", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			AccountID string `json:"accountId"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if sc.checkinFail[req.AccountID] {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "error"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success", "points": 200, "accountId": req.AccountID,
		})
	})

	return httptest.NewServer(mux)
}

func launchWorkbuddy(t *testing.T, targetURL string) *adapterhost.Host {
	t.Helper()
	host := targetURL
	if u, err := parseURL(targetURL); err == nil {
		host = u.Host
	}

	fetch := capabilities.NewHTTPFetch(
		capabilities.AllowedHostsFromManifest([]string{host}),
		capabilities.GrantFromNeeds([]string{"http.outbound"}))

	// 临时 store 用于 log.write 钩子（端到端测试不校验其落库）。
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	mux := NewHookMux()
	RegisterHTTPFetch(mux, fetch)
	RegisterLogWrite(mux, st.DB())
	mux.Init["baseURL"] = targetURL

	adapterDir := filepath.Join("..", "..", "adapters", "workbuddy-cn")
	h, err := adapterhost.LaunchWith(context.Background(), "bun", adapterDir, mux)
	if err != nil {
		t.Fatalf("LaunchWith 失败: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })
	return h
}

func TestDiscoverReturnsAccountsWithoutToken(t *testing.T) {
	target := newFakeTarget(t, scenario{accounts: healthyAccounts()})
	defer target.Close()
	h := launchWorkbuddy(t, target.URL)

	result, err := h.Call(context.Background(), "accounts.discover")
	if err != nil {
		t.Fatalf("discover 失败: %v", err)
	}

	var data struct {
		Status   string `json:"status"`
		Accounts []struct {
			ID             string `json:"id"`
			DisplayName    string `json:"displayName"`
			MaskedIdentity string `json:"maskedIdentity"`
			Source         struct {
				ID   string `json:"id"`
				Kind string `json:"kind"`
			} `json:"source"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(result, &data); err != nil {
		t.Fatal(err)
	}
	if data.Status != "ok" || len(data.Accounts) != 2 {
		t.Fatalf("发现结果错误: status=%s count=%d", data.Status, len(data.Accounts))
	}
	if data.Accounts[0].Source.Kind != "managed" {
		t.Errorf("来源类型错误")
	}
	if containsToken(result) {
		t.Errorf("发现结果不得返回 token，实际 %s", result)
	}
}

func TestDiscoverFailureScenarios(t *testing.T) {
	cases := []struct {
		name     string
		sc       scenario
		wantStatus string
	}{
		{
			name:       "未登录",
			sc:         scenario{accounts: []map[string]any{}},
			wantStatus: "not_signed_in",
		},
		{
			name: "需重登",
			sc: scenario{accounts: []map[string]any{
				{
					"id": "u_1", "displayName": "User One", "maskedIdentity": "****1234",
					"source": map[string]any{"id": "s_1", "kind": "managed", "health": "reauth_required"},
				},
			}},
			wantStatus: "reauth_required",
		},
		{
			name:       "平台不支持",
			sc:         scenario{accountsStatus: http.StatusForbidden},
			wantStatus: "platform_unsupported",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := newFakeTarget(t, tc.sc)
			defer target.Close()
			h := launchWorkbuddy(t, target.URL)

			result, err := h.Call(context.Background(), "accounts.discover")
			if err != nil {
				t.Fatalf("discover 失败: %v", err)
			}
			var data struct {
				Status string `json:"status"`
			}
			if err := json.Unmarshal(result, &data); err != nil {
				t.Fatal(err)
			}
			if data.Status != tc.wantStatus {
				t.Errorf("期望 %s，实际 %s", tc.wantStatus, data.Status)
			}
		})
	}
}

func TestCheckinRunSuccess(t *testing.T) {
	target := newFakeTarget(t, scenario{accounts: healthyAccounts()})
	defer target.Close()
	h := launchWorkbuddy(t, target.URL)

	result, err := h.CallWith(context.Background(), "checkin.run",
		map[string]any{"accountId": "u_1"})
	if err != nil {
		t.Fatalf("checkin 失败: %v", err)
	}

	var data struct {
		Status string `json:"status"`
		Points int    `json:"points"`
	}
	if err := json.Unmarshal(result, &data); err != nil {
		t.Fatal(err)
	}
	if data.Status != "success" || data.Points != 200 {
		t.Errorf("签到结果错误: %+v", data)
	}
}

func TestCheckinFailureIsolatedPerAccount(t *testing.T) {
	target := newFakeTarget(t, scenario{
		accounts:    healthyAccounts(),
		checkinFail: map[string]bool{"u_2": true},
	})
	defer target.Close()
	h := launchWorkbuddy(t, target.URL)

	// u_1 成功。
	if _, err := h.CallWith(context.Background(), "checkin.run",
		map[string]any{"accountId": "u_1"}); err != nil {
		t.Errorf("u_1 应成功，实际 %v", err)
	}

	// u_2 失败：不影响 u_1 的结果。
	if _, err := h.CallWith(context.Background(), "checkin.run",
		map[string]any{"accountId": "u_2"}); err == nil {
		t.Error("u_2 应失败")
	}
}

func TestImportedSourceMarkedReadOnly(t *testing.T) {
	// 一个导入来源（kind=imported）应被标记 read_only。
	sc := scenario{accounts: []map[string]any{
		{
			"id": "u_1", "displayName": "User One", "maskedIdentity": "****1234",
			"source": map[string]any{
				"id": "imp_1", "kind": "imported", "health": "healthy", "readOnly": true,
			},
		},
	}}
	target := newFakeTarget(t, sc)
	defer target.Close()
	h := launchWorkbuddy(t, target.URL)

	result, err := h.Call(context.Background(), "accounts.discover")
	if err != nil {
		t.Fatal(err)
	}
	if !containsReadOnly(result) {
		t.Errorf("导入来源应标记 read_only，实际 %s", result)
	}
}
