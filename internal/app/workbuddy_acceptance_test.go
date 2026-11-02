package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"qka/internal/accounts"
	"qka/internal/adapterhost"
	"qka/internal/capabilities"
	"qka/internal/engine"
	"qka/internal/status"
	"qka/internal/store"
	"qka/internal/workbuddy"
)

const (
	acceptanceAccess  = "{{SECRET_accept-access}}"
	acceptanceRefresh = "{{SECRET_accept-refresh}}"
	acceptanceUID     = "{{SECRET_accept-uid}}"
	acceptancePhone   = "{{SECRET_accept-phone}}"
)

type workBuddyAcceptanceTarget struct {
	mu                sync.Mutex
	checked           bool
	claimUnauthorized bool
	tokenPolls        int
	identityPolls     int
	claims            int
	balances          int
	refreshOK         bool
	paths             []string
}

func (f *workBuddyAcceptanceTarget) serve(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.paths = append(f.paths, r.URL.Path)

		switch r.URL.Path {
		case "/v2/plugin/auth/state":
			writeWorkBuddyEnvelope(t, w, map[string]any{
				"state":   "acceptance-state",
				"authUrl": "http://" + r.Host + "/official-login",
			})
		case "/v2/plugin/auth/token":
			f.tokenPolls++
			if f.tokenPolls == 1 {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"code": 11217, "msg": "waiting"})
				return
			}
			writeWorkBuddyEnvelope(t, w, map[string]any{
				"accessToken": acceptanceAccess, "refreshToken": acceptanceRefresh,
				"expiresIn": 3600, "domain": "copilot.tencent.com", "tokenType": "Bearer",
			})
		case "/v2/plugin/login/account":
			f.identityPolls++
			if f.identityPolls == 1 {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"code": 12151, "msg": "waiting"})
				return
			}
			writeWorkBuddyEnvelope(t, w, map[string]any{
				"uid": acceptanceUID, "type": "personal",
				"nickname": "验收账号", "phoneNumber": acceptancePhone,
			})
		case "/v2/plugin/accounts":
			if got := r.Header.Get("Authorization"); got != "Bearer "+acceptanceAccess {
				t.Errorf("只读验证 Authorization 错误: %q", got)
			}
			writeWorkBuddyEnvelope(t, w, map[string]any{
				"accounts": []map[string]any{{"uid": acceptanceUID}},
			})
		case "/billing/meter/get-user-resource-summary":
			f.balances++
			if got := r.Header.Get("Authorization"); got != "Bearer "+acceptanceAccess {
				t.Errorf("余额 Authorization 错误: %q", got)
			}
			writeWorkBuddyEnvelope(t, w, map[string]any{"IsPaidUser": true})
		case "/v2/plugin/auth/token/refresh":
			if !f.refreshOK {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			writeWorkBuddyEnvelope(t, w, map[string]any{
				"accessToken": acceptanceAccess, "refreshToken": acceptanceRefresh,
				"expiresIn": 3600,
			})
		case "/v2/billing/meter/checkin-activity-status":
			if got := r.Header.Get("Authorization"); got != "Bearer "+acceptanceAccess {
				t.Errorf("签到状态 Authorization 错误: %q", got)
			}
			writeWorkBuddyEnvelope(t, w, map[string]any{
				"active": true, "today_checked_in": f.checked,
				"streak_days": 3, "daily_credit": 100, "today_credit": 0,
			})
		case "/v2/billing/meter/daily-checkin":
			f.claims++
			if f.claimUnauthorized {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			f.checked = true
			writeWorkBuddyEnvelope(t, w, map[string]any{
				"credit": 100, "streak_days": 4, "message": "签到成功",
			})
		default:
			t.Errorf("验收出现未预期请求: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
}

func (f *workBuddyAcceptanceTarget) snapshot() (claims, balances int, paths []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.claims, f.balances, append([]string(nil), f.paths...)
}

func waitForLoginStatus(t *testing.T, svc *workbuddy.LoginService, want workbuddy.LoginStatus) workbuddy.LoginView {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		view := svc.Status(context.Background(), "workbuddy-cn")
		if view.Status == want {
			return view
		}
		time.Sleep(5 * time.Millisecond)
	}
	return svc.Status(context.Background(), "workbuddy-cn")
}

func TestWorkBuddyControlledAccountAcceptance(t *testing.T) {
	now := time.Date(2026, 10, 29, 14, 0, 0, 0, engine.Beijing)
	target := &workBuddyAcceptanceTarget{refreshOK: true}
	srv := target.serve(t)
	defer srv.Close()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	acct := accounts.NewService(st.DB())
	file := newAppSecretStore()
	creds := capabilities.NewCredentialService(
		failingAppSecretStore{}, file,
		capabilities.Grant{
			"credentials.managed": true,
			"credentials.resolve": true,
		},
		func() time.Time { return now },
	)
	acct.SetManagedCredentialDeleter(creds)

	pending := newWorkBuddyPendingStoreForAcceptance()
	login := workbuddy.NewLoginService(workbuddy.LoginConfig{
		BaseURL:      srv.URL,
		PendingStore: pending,
		OpenBrowser:  func(string) error { return nil },
		PollInterval: 5 * time.Millisecond,
		Timeout:      2 * time.Second,
		Now:          func() time.Time { return now },
		Verifier: workbuddy.NewVerificationService(workbuddy.VerificationConfig{
			BaseURL:      srv.URL,
			PendingStore: pending,
			Sink:         newWorkBuddyVerificationSink(acct, creds),
		}),
	})

	view, err := login.Begin(context.Background(), "workbuddy-cn")
	if err != nil {
		t.Fatalf("发起受控登录失败: %v", err)
	}
	if view.Status != workbuddy.LoginPending ||
		view.OfficialOrigin != srv.URL ||
		!strings.HasPrefix(view.OfficialOrigin, "http://127.0.0.1") {
		t.Fatalf("受控登录应只打开回环假目标，实际 %+v", view)
	}
	view = waitForLoginStatus(t, login, workbuddy.LoginVerified)
	if view.Status != workbuddy.LoginVerified {
		t.Fatalf("受控登录与只读验证应完成，实际 %+v", view)
	}
	assertAcceptanceViewIsRedacted(t, "登录状态", mustJSON(t, view))

	list, err := acct.List(context.Background(), "workbuddy-cn")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("应只创建一个账号，实际 %d 个", len(list))
	}
	account := list[0]
	if !account.Enabled {
		t.Fatal("首次登录验证的健康账号必须自动启用")
	}
	if account.MaskedIdentity != "****"+acceptancePhone[len(acceptancePhone)-4:] {
		t.Errorf("账号展示身份未脱敏，实际 %q", account.MaskedIdentity)
	}
	if len(account.Sources) != 1 || account.Sources[0].Kind != accounts.KindManaged ||
		account.Sources[0].Health != accounts.HealthHealthy {
		t.Errorf("应建立健康受管来源，实际 %+v", account.Sources)
	}
	assertAcceptanceViewIsRedacted(t, "账号列表", mustJSON(t, list))

	statusSvc := status.NewService(st.DB(), func() time.Time { return now },
		func(string) []string { return []string{"checkin"} })
	a := &App{
		cfg:   Config{BaseURLs: map[string]string{"workbuddy-cn": srv.URL}},
		now:   func() time.Time { return now },
		store: st, accounts: acct, credentials: creds,
		engine: engine.New(st.DB(), func() time.Time { return now }, "acceptance"),
		status: statusSvc, hosts: map[string]*adapterhost.Host{},
	}

	obsAny, err := a.CallTool(context.Background(), "checkin_run",
		rawJSON(`{"target":"workbuddy-cn","account_id":"`+account.ID+`"}`))
	if err != nil {
		t.Fatalf("受控签到失败: %v", err)
	}
	obs := obsAny.(engine.Observation)
	if !obs.Executed || obs.State != engine.SignedConfirmed {
		t.Fatalf("有回执的签到应确认到账，实际 %+v", obs)
	}
	assertAcceptanceViewIsRedacted(t, "签到结果", mustJSON(t, obs))

	// A same-day second run must not send another claim.
	if _, err := a.CallTool(context.Background(), "checkin_run",
		rawJSON(`{"target":"workbuddy-cn","account_id":"`+account.ID+`"}`)); err != nil {
		t.Fatalf("同日第二次执行失败: %v", err)
	}
	claims, _, _ := target.snapshot()
	if claims != 1 {
		t.Fatalf("同日第二次执行不得再次领取，实际领取 %d 次", claims)
	}

	// Disable: the account must disappear from plan and status while keeping
	// its sources and credential for a later re-enable.
	if err := acct.SetEnabled(context.Background(), "workbuddy-cn", account.ID, false); err != nil {
		t.Fatal(err)
	}
	plan, err := statusSvc.PlanValidate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range plan.Items {
		if item.AccountID == account.ID {
			t.Errorf("停用账号不得进入执行计划: %+v", item)
		}
	}
	viewStatus, err := statusSvc.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range viewStatus.Accounts {
		if entry.AccountID == account.ID {
			t.Errorf("停用账号不得出现在运行状态: %+v", entry)
		}
	}
	disabled, err := acct.Get(context.Background(), "workbuddy-cn", account.ID)
	if err != nil {
		t.Fatalf("停用不得删除账号: %v", err)
	}
	if disabled.Enabled || len(disabled.Sources) != 1 {
		t.Fatalf("停用应保留账号与来源，实际 %+v", disabled)
	}

	// Delete: removes the QKA mapping, sources and managed credential only.
	if err := acct.Delete(context.Background(), "workbuddy-cn", account.ID); err != nil {
		t.Fatalf("删除 QKA 映射失败: %v", err)
	}
	after, err := acct.List(context.Background(), "workbuddy-cn")
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 0 {
		t.Errorf("删除后账号列表应为空，实际 %+v", after)
	}
	var sources int
	if err := st.DB().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM sources WHERE target = 'workbuddy-cn' AND account_id = ?`,
		account.ID).Scan(&sources); err != nil {
		t.Fatal(err)
	}
	if sources != 0 {
		t.Errorf("删除后来源应级联清理，实际 %d", sources)
	}
	if _, err := creds.Resolve(context.Background(), capabilities.CredentialRef{
		Target: "workbuddy-cn", AccountID: account.ID,
		SourceID: account.Sources[0].ID, Kind: capabilities.Managed,
	}); err == nil {
		t.Error("删除后受管凭据必须不可解析")
	}

	// The upstream account remains usable: a separate login with the same
	// identity re-creates the mapping and managed source idempotently.
	loginAgain := workbuddy.NewLoginService(workbuddy.LoginConfig{
		BaseURL:      srv.URL,
		PendingStore: pending,
		OpenBrowser:  func(string) error { return nil },
		PollInterval: 5 * time.Millisecond,
		Timeout:      2 * time.Second,
		Now:          func() time.Time { return now },
		Verifier: workbuddy.NewVerificationService(workbuddy.VerificationConfig{
			BaseURL:      srv.URL,
			PendingStore: pending,
			Sink:         newWorkBuddyVerificationSink(acct, creds),
		}),
	})
	if _, err := loginAgain.Begin(context.Background(), "workbuddy-cn"); err != nil {
		t.Fatalf("删除后重新登录失败: %v", err)
	}
	view = waitForLoginStatus(t, loginAgain, workbuddy.LoginVerified)
	if view.Status != workbuddy.LoginVerified {
		t.Fatalf("删除后同一身份应可重新验证，实际 %+v", view)
	}
	again, err := acct.List(context.Background(), "workbuddy-cn")
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 1 || again[0].ID != account.ID || !again[0].Enabled {
		t.Fatalf("重新登录应恢复同一脱敏账号并自动启用，实际 %+v", again)
	}

	logsJSON := mustJSON(t, a.tailLogsForAcceptance(t))
	assertAcceptanceViewIsRedacted(t, "日志", logsJSON)
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func assertAcceptanceViewIsRedacted(t *testing.T, label, raw string) {
	t.Helper()
	for _, secret := range []string{
		acceptanceAccess, acceptanceRefresh, acceptanceUID, acceptancePhone,
		"Authorization", "X-Refresh-Token",
	} {
		if strings.Contains(raw, secret) {
			t.Errorf("%s泄露敏感值 %q: %s", label, secret, raw)
		}
	}
}

type acceptanceLogPage struct {
	Entries []struct {
		Message string `json:"message"`
	} `json:"entries"`
}

func (a *App) tailLogsForAcceptance(t *testing.T) acceptanceLogPage {
	t.Helper()
	raw, err := a.CallTool(context.Background(), "log_tail", rawJSON(`{"limit":50}`))
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	var page acceptanceLogPage
	if err := json.Unmarshal(data, &page); err != nil {
		t.Fatal(err)
	}
	return page
}

type workBuddyAcceptancePendingStore struct {
	mu      sync.Mutex
	payload map[string][]byte
}

func newWorkBuddyPendingStoreForAcceptance() *workBuddyAcceptancePendingStore {
	return &workBuddyAcceptancePendingStore{payload: map[string][]byte{}}
}

func (s *workBuddyAcceptancePendingStore) Save(_ context.Context, id string, raw []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.payload[id] = append([]byte(nil), raw...)
	return nil
}

func (s *workBuddyAcceptancePendingStore) Load(_ context.Context, id string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, ok := s.payload[id]
	if !ok {
		return nil, workbuddy.ErrPendingCredentialNotFound
	}
	return append([]byte(nil), raw...), nil
}

func (s *workBuddyAcceptancePendingStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.payload, id)
	return nil
}
