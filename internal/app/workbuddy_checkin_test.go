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
)

type workBuddyCheckinTarget struct {
	mu           sync.Mutex
	active       bool
	checked      bool
	claimCode    int
	claimCredit  float64
	claimMessage string
	claimDelayed bool
	failUID      string
	statuses     int
	claims       int
}

func (f *workBuddyCheckinTarget) serve(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()

		if got := r.Header.Get("Authorization"); got != "Bearer app-access" &&
			got != "Bearer app-access-two" {
			t.Errorf("Authorization 头错误: %q", got)
		}
		switch r.URL.Path {
		case "/v2/billing/meter/checkin-activity-status":
			f.statuses++
			if f.failUID != "" && r.Header.Get("X-User-Id") == f.failUID {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			writeWorkBuddyEnvelope(t, w, map[string]any{
				"active": f.active, "today_checked_in": f.checked,
				"streak_days": 3, "daily_credit": 100, "today_credit": 0,
			})
		case "/v2/billing/meter/daily-checkin":
			f.claims++
			if f.claimCode != 0 {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"code": f.claimCode, "msg": f.claimMessage,
				})
				return
			}
			f.checked = true
			writeWorkBuddyEnvelope(t, w, map[string]any{
				"credit": f.claimCredit, "streak_days": 4,
				"credit_delayed": f.claimDelayed,
				"message":        f.claimMessage,
			})
		default:
			t.Errorf("签到出现未预期请求: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
}

func newWorkBuddyCheckinApp(
	t *testing.T, targetURL string, now time.Time,
) (*App, *accounts.Service, string) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	acct := accounts.NewService(st.DB())
	const accountID, sourceID = "wb_one", "wbm_one"
	if err := acct.Discover(context.Background(), "workbuddy-cn",
		[]accounts.DiscoveredAccount{{
			ID: accountID, DisplayName: "****1111", MaskedIdentity: "****1111",
			SourceID: sourceID, SourceKind: accounts.KindManaged,
		}}); err != nil {
		t.Fatal(err)
	}
	if err := acct.SetEnabled(context.Background(), "workbuddy-cn", accountID, true); err != nil {
		t.Fatal(err)
	}

	creds := capabilities.NewCredentialService(
		failingAppSecretStore{},
		newAppSecretStore(),
		capabilities.Grant{
			"credentials.managed": true,
			"credentials.resolve": true,
		},
		func() time.Time { return now },
	)
	if err := creds.Persist(context.Background(), capabilities.CredentialRef{
		Target: "workbuddy-cn", AccountID: accountID,
		SourceID: sourceID, Kind: capabilities.Managed,
	}, string(workBuddyCredential(t, "app-access", "app-refresh", "app-uid",
		"copilot.tencent.com", now.Add(time.Hour)))); err != nil {
		t.Fatal(err)
	}

	a := &App{
		cfg:   Config{BaseURLs: map[string]string{"workbuddy-cn": targetURL}},
		now:   func() time.Time { return now },
		store: st, accounts: acct, credentials: creds,
		engine: engine.New(st.DB(), func() time.Time { return now }, "checkin-test"),
		status: status.NewService(st.DB(), func() time.Time { return now },
			func(string) []string { return []string{"checkin"} }),
		hosts: map[string]*adapterhost.Host{},
	}
	return a, acct, accountID
}

func TestWorkBuddyCheckinRunClaimsOnceAndRecordsEvidence(t *testing.T) {
	now := time.Date(2026, 10, 29, 14, 0, 0, 0, engine.Beijing)
	target := &workBuddyCheckinTarget{active: true, claimCredit: 100}
	srv := target.serve(t)
	defer srv.Close()

	a, _, accountID := newWorkBuddyCheckinApp(t, srv.URL, now)
	obs, err := a.CallTool(context.Background(), "checkin_run",
		rawJSON(`{"target":"workbuddy-cn","account_id":"`+accountID+`"}`))
	if err != nil {
		t.Fatalf("签到失败: %v", err)
	}
	got := obs.(engine.Observation)
	if !got.Executed || got.State != engine.SignedConfirmed {
		t.Errorf("午后有回执应确认到账，实际 %+v", got)
	}
	if target.statuses != 1 || target.claims != 1 {
		t.Errorf("应先查状态再领取一次，status=%d claims=%d", target.statuses, target.claims)
	}
	var logState, logMessage string
	if err := a.store.DB().QueryRowContext(context.Background(),
		`SELECT state, message FROM logs WHERE target = 'workbuddy-cn' AND account_id = ?`,
		accountID).Scan(&logState, &logMessage); err != nil {
		t.Fatalf("签到结果应写入日志: %v", err)
	}
	if logState != string(engine.SignedConfirmed) || logMessage != "已到账" {
		t.Errorf("签到日志应记录最终状态，实际 %s/%s", logState, logMessage)
	}
}

func TestWorkBuddyCheckinWithoutReceiptStaysProvisional(t *testing.T) {
	now := time.Date(2026, 10, 29, 14, 0, 0, 0, engine.Beijing)
	target := &workBuddyCheckinTarget{active: true}
	srv := target.serve(t)
	defer srv.Close()

	a, _, accountID := newWorkBuddyCheckinApp(t, srv.URL, now)
	obs, err := a.CallTool(context.Background(), "checkin_run",
		rawJSON(`{"target":"workbuddy-cn","account_id":"`+accountID+`"}`))
	if err != nil {
		t.Fatalf("签到失败: %v", err)
	}
	got := obs.(engine.Observation)
	if got.State != engine.SignedProvisional || got.Message != "已领取，待确认" {
		t.Errorf("无回执不得宣称到账，实际 %+v", got)
	}
}

func TestWorkBuddyCheckinDelayedCreditStaysProvisional(t *testing.T) {
	now := time.Date(2026, 10, 29, 14, 0, 0, 0, engine.Beijing)
	target := &workBuddyCheckinTarget{
		active: true, claimCredit: 100, claimMessage: "签到成功", claimDelayed: true,
	}
	srv := target.serve(t)
	defer srv.Close()

	a, _, accountID := newWorkBuddyCheckinApp(t, srv.URL, now)
	obs, err := a.CallTool(context.Background(), "checkin_run",
		rawJSON(`{"target":"workbuddy-cn","account_id":"`+accountID+`"}`))
	if err != nil {
		t.Fatalf("签到失败: %v", err)
	}
	got := obs.(engine.Observation)
	if got.State != engine.SignedProvisional || got.Message != "已领取，待确认" {
		t.Errorf("延迟到账不得宣称已到账，实际 %+v", got)
	}
}

func TestWorkBuddyCheckinSameDayDoesNotClaimAgain(t *testing.T) {
	now := time.Date(2026, 10, 29, 14, 0, 0, 0, engine.Beijing)
	target := &workBuddyCheckinTarget{active: true, claimCredit: 100}
	srv := target.serve(t)
	defer srv.Close()

	a, _, accountID := newWorkBuddyCheckinApp(t, srv.URL, now)
	for i := 0; i < 2; i++ {
		if _, err := a.CallTool(context.Background(), "checkin_run",
			rawJSON(`{"target":"workbuddy-cn","account_id":"`+accountID+`"}`)); err != nil {
			t.Fatalf("第 %d 次签到失败: %v", i+1, err)
		}
	}
	if target.claims != 1 {
		t.Errorf("同日不得重复领取，实际 %d 次", target.claims)
	}
}

func TestWorkBuddyCheckinRefusalIsTerminalForTheDay(t *testing.T) {
	cases := []struct {
		code  int
		state engine.State
	}{
		{1001, engine.Already},
		{1002, engine.Ineligible},
		{1003, engine.Inactive},
	}
	for _, tc := range cases {
		t.Run(string(tc.state), func(t *testing.T) {
			now := time.Date(2026, 10, 29, 14, 0, 0, 0, engine.Beijing)
			target := &workBuddyCheckinTarget{
				active: true, claimCode: tc.code, claimMessage: "上游原因",
			}
			srv := target.serve(t)
			defer srv.Close()

			a, _, accountID := newWorkBuddyCheckinApp(t, srv.URL, now)
			for i := 0; i < 2; i++ {
				obs, err := a.CallTool(context.Background(), "checkin_run",
					rawJSON(`{"target":"workbuddy-cn","account_id":"`+accountID+`"}`))
				if err != nil {
					t.Fatalf("第 %d 次签到失败: %v", i+1, err)
				}
				got := obs.(engine.Observation)
				if i == 0 && got.State != tc.state {
					t.Errorf("业务码 %d 应映射 %s，实际 %s", tc.code, tc.state, got.State)
				}
				if i == 1 && got.Executed {
					t.Errorf("业务码 %d 当日终态后不得再次请求", tc.code)
				}
			}
			if target.claims != 1 {
				t.Errorf("业务码 %d 当日只应尝试领取一次，实际 %d", tc.code, target.claims)
			}
		})
	}
}

func TestWorkBuddyCheckinFailureDoesNotChangeAccountState(t *testing.T) {
	now := time.Date(2026, 10, 29, 14, 0, 0, 0, engine.Beijing)
	target := &workBuddyCheckinTarget{active: true, claimCode: 500, claimMessage: "transient"}
	srv := target.serve(t)
	defer srv.Close()

	a, acct, accountID := newWorkBuddyCheckinApp(t, srv.URL, now)
	obs, err := a.CallTool(context.Background(), "checkin_run",
		rawJSON(`{"target":"workbuddy-cn","account_id":"`+accountID+`"}`))
	if err != nil {
		t.Fatalf("签到失败应进入状态机而非中止: %v", err)
	}
	if got := obs.(engine.Observation); got.State != engine.FailedRetryable {
		t.Errorf("通用上游失败应可重试，实际 %+v", got)
	}

	acc, err := acct.Get(context.Background(), "workbuddy-cn", accountID)
	if err != nil {
		t.Fatalf("签到失败不得删除账号: %v", err)
	}
	if !acc.Enabled || len(acc.Sources) != 1 ||
		acc.Sources[0].Health != accounts.HealthHealthy {
		t.Errorf("签到失败不得改变启用或来源健康: %+v", acc)
	}
}

func TestWorkBuddyCheckinRunAllIsolatesAccountFailures(t *testing.T) {
	now := time.Date(2026, 10, 29, 14, 0, 0, 0, engine.Beijing)
	target := &workBuddyCheckinTarget{
		active: true, claimCredit: 100, failUID: "app-uid-two",
	}
	srv := target.serve(t)
	defer srv.Close()

	a, acct, firstAccountID := newWorkBuddyCheckinApp(t, srv.URL, now)
	const secondAccountID, secondSourceID = "wb_two", "wbm_two"
	if err := acct.Discover(context.Background(), "workbuddy-cn",
		[]accounts.DiscoveredAccount{{
			ID: secondAccountID, DisplayName: "****2222", MaskedIdentity: "****2222",
			SourceID: secondSourceID, SourceKind: accounts.KindManaged,
		}}); err != nil {
		t.Fatal(err)
	}
	if err := acct.SetEnabled(context.Background(), "workbuddy-cn", secondAccountID, true); err != nil {
		t.Fatal(err)
	}
	if err := a.credentials.Persist(context.Background(), capabilities.CredentialRef{
		Target: "workbuddy-cn", AccountID: secondAccountID,
		SourceID: secondSourceID, Kind: capabilities.Managed,
	}, string(workBuddyCredential(t, "app-access-two", "app-refresh-two", "app-uid-two",
		"copilot.tencent.com", now.Add(time.Hour)))); err != nil {
		t.Fatal(err)
	}
	// The second account's upstream failure must not stop the first account
	// or disable either mapping.
	err := a.RunAll(context.Background())
	if err != nil {
		t.Fatalf("可重试的账号失败应由状态机记录，不应中止批次: %v", err)
	}

	var firstState, secondState string
	day := engine.BeijingDay(now)
	_ = a.store.DB().QueryRowContext(context.Background(),
		`SELECT state FROM daily_ops WHERE target = 'workbuddy-cn'
		 AND account_id = ? AND operation = 'checkin' AND day_key = ?`,
		firstAccountID, day).Scan(&firstState)
	if firstState != string(engine.SignedConfirmed) {
		t.Errorf("其他账号失败不得阻止第一个账号签到，实际 %q", firstState)
	}
	_ = a.store.DB().QueryRowContext(context.Background(),
		`SELECT state FROM daily_ops WHERE target = 'workbuddy-cn'
		 AND account_id = ? AND operation = 'checkin' AND day_key = ?`,
		secondAccountID, day).Scan(&secondState)
	if secondState != string(engine.FailedRetryable) {
		t.Errorf("失败账号应可重试，实际 %q", secondState)
	}

	for _, accountID := range []string{firstAccountID, secondAccountID} {
		acc, err := acct.Get(context.Background(), "workbuddy-cn", accountID)
		if err != nil {
			t.Fatalf("失败不得删除账号 %s: %v", accountID, err)
		}
		if !acc.Enabled || acc.Sources[0].Health != accounts.HealthHealthy {
			t.Errorf("失败不得改变账号 %s 的启用/健康: %+v", accountID, acc)
		}
	}
}

func TestWorkBuddyCheckinUsesOnlyManagedSource(t *testing.T) {
	now := time.Date(2026, 10, 29, 14, 0, 0, 0, engine.Beijing)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	acct := accounts.NewService(st.DB())
	if err := acct.Discover(context.Background(), "workbuddy-cn",
		[]accounts.DiscoveredAccount{{
			ID: "wb_imported", DisplayName: "****1111", MaskedIdentity: "****1111",
			SourceID: "imp_one", SourceKind: accounts.KindImported,
		}}); err != nil {
		t.Fatal(err)
	}
	if err := acct.SetEnabled(context.Background(), "workbuddy-cn", "wb_imported", true); err != nil {
		t.Fatal(err)
	}

	a := &App{
		cfg: Config{BaseURLs: map[string]string{"workbuddy-cn": "https://copilot.tencent.com"}},
		now: func() time.Time { return now }, store: st, accounts: acct,
		engine: engine.New(st.DB(), func() time.Time { return now }, "checkin-test"),
		status: status.NewService(st.DB(), func() time.Time { return now },
			func(string) []string { return []string{"checkin"} }),
		hosts: map[string]*adapterhost.Host{},
	}
	_, err = a.CallTool(context.Background(), "checkin_run",
		rawJSON(`{"target":"workbuddy-cn","account_id":"wb_imported"}`))
	if err == nil || !strings.Contains(err.Error(), "受管来源") {
		t.Fatalf("导入来源不得被签到写入，实际 %v", err)
	}

	var count int
	_ = st.DB().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM daily_ops WHERE account_id = 'wb_imported'`).Scan(&count)
	if count != 0 {
		t.Errorf("导入来源拒绝后不得写入当日状态，实际 %d", count)
	}
}
