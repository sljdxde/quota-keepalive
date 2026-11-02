package app

import (
	"context"
	"encoding/json"
	"errors"
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
	"qka/internal/requestguard"
	"qka/internal/status"
	"qka/internal/store"
)

type workBuddyCheckinTarget struct {
	mu                  sync.Mutex
	active              bool
	checked             bool
	statusUnauthorized  bool
	claimUnauthorized   bool
	balanceUnauthorized bool
	balanceNetworkFail  bool
	claimCode           int
	claimCredit         float64
	claimMessage        string
	claimDelayed        bool
	omitClaimCredit     bool
	todayCredit         float64
	balancePackages     []map[string]any
	failUID             string
	statuses            int
	claims              int
	balances            int
	paths               []string
}

func (f *workBuddyCheckinTarget) serve(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()

		if r.URL.Path != "/v2/plugin/auth/token/refresh" {
			if got := r.Header.Get("Authorization"); got != "Bearer app-access" &&
				got != "Bearer app-access-two" {
				t.Errorf("Authorization 头错误: %q", got)
			}
		}
		f.paths = append(f.paths, r.URL.Path)
		switch r.URL.Path {
		case "/v2/plugin/auth/token/refresh":
			writeWorkBuddyEnvelope(t, w, map[string]any{
				"accessToken":  "app-access",
				"refreshToken": "app-refresh-renewed",
				"expiresIn":    3600,
			})
		case "/v2/billing/meter/checkin-activity-status":
			f.statuses++
			if f.failUID != "" && r.Header.Get("X-User-Id") == f.failUID {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			if f.statusUnauthorized {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			writeWorkBuddyEnvelope(t, w, map[string]any{
				"active": f.active, "today_checked_in": f.checked,
				"streak_days": 3, "daily_credit": 100,
				"today_credit": map[bool]float64{true: f.todayCredit, false: 0}[f.checked],
			})
		case "/v2/billing/meter/daily-checkin":
			f.claims++
			if f.claimUnauthorized {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if f.claimCode != 0 {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"code": f.claimCode, "msg": f.claimMessage,
				})
				return
			}
			f.checked = true
			data := map[string]any{
				"credit": f.claimCredit, "streak_days": 4,
				"credit_delayed": f.claimDelayed,
				"message":        f.claimMessage,
			}
			if f.omitClaimCredit {
				delete(data, "credit")
			}
			writeWorkBuddyEnvelope(t, w, data)
		case "/billing/meter/get-user-resource-summary":
			f.balances++
			if f.balanceNetworkFail {
				if hijacker, ok := w.(http.Hijacker); ok {
					conn, _, err := hijacker.Hijack()
					if err == nil {
						_ = conn.Close()
						return
					}
				}
				http.Error(w, "network failure", http.StatusInternalServerError)
				return
			}
			if f.balanceUnauthorized {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if f.balancePackages == nil {
				writeWorkBuddyEnvelope(t, w, map[string]any{"IsPaidUser": true})
				return
			}
			writeWorkBuddyEnvelope(t, w, map[string]any{"Packages": f.balancePackages})
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
	configDir := t.TempDir()
	st, err := store.Open(configDir)
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
		cfg:   Config{ConfigDir: configDir, BaseURLs: map[string]string{"workbuddy-cn": targetURL}},
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
	if target.statuses != 2 || target.claims != 1 {
		t.Errorf("应先查状态再领取一次，成功后只读刷新额度，status=%d claims=%d",
			target.statuses, target.claims)
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

func TestWorkBuddyCheckinStoresCreditAndCurrentQuota(t *testing.T) {
	now := time.Date(2026, 10, 29, 14, 0, 0, 0, engine.Beijing)
	target := &workBuddyCheckinTarget{
		active: true, claimCredit: 100,
		balancePackages: []map[string]any{{
			"CycleTotalCapacity": "1500", "CycleUsedCapacity": "100",
		}},
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
	if got.Credits == nil || *got.Credits != 100 {
		t.Fatalf("签到结果应显示本次领取额度，实际 %+v", got)
	}
	if got.Balance == nil || *got.Balance != 1400 {
		t.Fatalf("签到结果应显示剩余额度，实际 %+v", got)
	}

	view, err := a.status.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Accounts) != 1 || len(view.Accounts[0].Operations) != 1 {
		t.Fatalf("账号状态应包含今日签到，实际 %+v", view)
	}
	op := view.Accounts[0].Operations[0]
	if op.Credits == nil || *op.Credits != 100 ||
		op.Balance == nil || *op.Balance != 1400 {
		t.Errorf("状态视图应同时包含领取额度与剩余额度，实际 %+v", op)
	}
	var readings int
	if err := a.store.DB().QueryRowContext(context.Background(), `
SELECT count(*) FROM quota_readings
WHERE provider = 'workbuddy-cn' AND account_id = ?`, accountID).Scan(&readings); err != nil {
		t.Fatal(err)
	}
	if readings != 1 {
		t.Fatalf("签到成功后应写入额度历史，实际 %d 条", readings)
	}
}

func TestWorkBuddyAlreadyCheckedInStoresTodayCreditAndQuota(t *testing.T) {
	now := time.Date(2026, 10, 29, 14, 0, 0, 0, engine.Beijing)
	target := &workBuddyCheckinTarget{
		active: true, checked: true, todayCredit: 100,
		balancePackages: []map[string]any{{
			"CycleRemainCapacity": "1200",
		}},
	}
	srv := target.serve(t)
	defer srv.Close()

	a, _, accountID := newWorkBuddyCheckinApp(t, srv.URL, now)
	obs, err := a.CallTool(context.Background(), "checkin_run",
		rawJSON(`{"target":"workbuddy-cn","account_id":"`+accountID+`"}`))
	if err != nil {
		t.Fatalf("状态查询失败: %v", err)
	}
	got := obs.(engine.Observation)
	if got.State != engine.Already || got.Executed != true {
		t.Fatalf("今日已领取应返回 already，实际 %+v", got)
	}
	if target.claims != 0 {
		t.Errorf("今日已领取不得再次领取，实际 %d 次", target.claims)
	}
	if got.Credits == nil || *got.Credits != 100 ||
		got.Balance == nil || *got.Balance != 1200 {
		t.Errorf("已领取状态应显示今日额度与当前额度，实际 %+v", got)
	}
}

func TestWorkBuddyUnknownQuotaDoesNotBecomeZero(t *testing.T) {
	now := time.Date(2026, 10, 29, 14, 0, 0, 0, engine.Beijing)
	target := &workBuddyCheckinTarget{active: true, omitClaimCredit: true}
	srv := target.serve(t)
	defer srv.Close()

	a, _, accountID := newWorkBuddyCheckinApp(t, srv.URL, now)
	obs, err := a.CallTool(context.Background(), "checkin_run",
		rawJSON(`{"target":"workbuddy-cn","account_id":"`+accountID+`"}`))
	if err != nil {
		t.Fatalf("签到失败: %v", err)
	}
	got := obs.(engine.Observation)
	if got.Credits != nil || got.Balance != nil {
		t.Errorf("未知额度不得伪造为 0，实际 %+v", got)
	}
	if got.State != engine.SignedProvisional {
		t.Errorf("无额度回执仍应保持待确认，实际 %+v", got)
	}
}

func TestWorkBuddyRefreshReadsQuotaWithoutClaiming(t *testing.T) {
	now := time.Date(2026, 10, 29, 14, 0, 0, 0, engine.Beijing)
	target := &workBuddyCheckinTarget{
		active: true, checked: true, todayCredit: 100,
		balancePackages: []map[string]any{{
			"CycleTotalCapacity": 1500.0, "CycleUsedCapacity": 300.0,
		}},
	}
	srv := target.serve(t)
	defer srv.Close()

	a, _, accountID := newWorkBuddyCheckinApp(t, srv.URL, now)
	if err := a.RunAccount(context.Background(), "workbuddy-cn", accountID, "refresh"); err != nil {
		t.Fatalf("只读刷新额度失败: %v", err)
	}
	if target.claims != 0 {
		t.Fatalf("刷新额度不得调用领取接口，实际 %d 次", target.claims)
	}
	view, err := a.status.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	op := view.Accounts[0].Operations[0]
	if op.State != "pending" || op.Credits == nil || *op.Credits != 100 ||
		op.Balance == nil || *op.Balance != 1200 {
		t.Fatalf("刷新应写入只读额度且不改变待执行状态，实际 %+v", op)
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

func TestWorkBuddyQuotaReadFailureDoesNotUndoSuccessfulCheckin(t *testing.T) {
	now := time.Date(2026, 10, 29, 14, 0, 0, 0, engine.Beijing)
	target := &workBuddyCheckinTarget{
		active: true, claimCredit: 100, balanceUnauthorized: true,
	}
	srv := target.serve(t)
	defer srv.Close()

	a, acct, accountID := newWorkBuddyCheckinApp(t, srv.URL, now)
	obs, err := a.CallTool(context.Background(), "checkin_run",
		rawJSON(`{"target":"workbuddy-cn","account_id":"`+accountID+`"}`))
	if err != nil {
		t.Fatalf("签到失败: %v", err)
	}
	got := obs.(engine.Observation)
	if got.State != engine.SignedConfirmed || got.Credits == nil || *got.Credits != 100 {
		t.Fatalf("额度读取失败不得撤销签到成功，实际 %+v", got)
	}
	if got.Balance != nil {
		t.Errorf("余额 401 时不得伪造额度，实际 %+v", got.Balance)
	}
	var message string
	if err := a.store.DB().QueryRowContext(context.Background(), `
SELECT message FROM daily_ops
WHERE target = 'workbuddy-cn' AND account_id = ? AND operation = 'checkin'
ORDER BY day_key DESC LIMIT 1`, accountID).Scan(&message); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(message, "刷新额度") {
		t.Errorf("余额读取失败时卡片应给出刷新提示，实际 %q", message)
	}
	account, err := acct.Get(context.Background(), "workbuddy-cn", accountID)
	if err != nil {
		t.Fatal(err)
	}
	if account.Sources[0].Health != accounts.HealthHealthy {
		t.Errorf("成功签到后的可选额度读取失败不得标记需重登，实际 %+v",
			account.Sources[0])
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
	summary, err := a.RunAllGuarded(context.Background())
	if err != nil {
		t.Fatalf("可重试的账号失败应由状态机记录，不应中止批次: %v", err)
	}
	if summary.Attempted != 2 || summary.Succeeded != 1 || summary.NeedsAttention != 1 {
		t.Errorf("批次摘要应隔离一个失败账号，实际 %+v", summary)
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

func TestWorkBuddyCheckin401WithWorkingBalanceEntersOwnAppFallback(t *testing.T) {
	now := time.Date(2026, 10, 29, 14, 0, 0, 0, engine.Beijing)
	target := &workBuddyCheckinTarget{active: true, claimUnauthorized: true}
	srv := target.serve(t)
	defer srv.Close()

	a, _, accountID := newWorkBuddyCheckinApp(t, srv.URL, now)
	a.riskGuard = requestguard.New(a.now)
	obs, err := a.CallTool(context.Background(), "checkin_run",
		rawJSON(`{"target":"workbuddy-cn","account_id":"`+accountID+`"}`))
	if err != nil {
		t.Fatalf("签到兜底不应中止: %v", err)
	}
	got := obs.(engine.Observation)
	if got.State != engine.ManualRequired {
		t.Fatalf("签到 401 且余额可用应进入 manual_required，实际 %+v", got)
	}
	if strings.Contains(got.Message, "已到账") || strings.Contains(got.Message, "自动签到成功") {
		t.Errorf("兜底不得显示自动签到成功，实际 %q", got.Message)
	}
	if !strings.Contains(got.Message, "请回 WorkBuddy App 签到") {
		t.Errorf("兜底必须给出下一步，实际 %q", got.Message)
	}
	if target.balances != 1 {
		t.Errorf("应以同一受管凭据只读查询余额一次，实际 %d", target.balances)
	}
	if _, reason, ok := a.riskGuard.Cooldown("workbuddy-cn", accountID); !ok ||
		reason != requestguard.ReasonChallenge {
		t.Fatalf("manual-required check-in should apply a challenge cooldown: %v %v", reason, ok)
	}

	var logState, logMessage string
	if err := a.store.DB().QueryRowContext(context.Background(),
		`SELECT state, message FROM logs WHERE target = 'workbuddy-cn' AND account_id = ?`,
		accountID).Scan(&logState, &logMessage); err != nil {
		t.Fatalf("兜底结果应写入日志: %v", err)
	}
	if logState != string(engine.ManualRequired) || logMessage != got.Message {
		t.Errorf("兜底日志错误: %s/%s", logState, logMessage)
	}
}

func TestWorkBuddyCheckinBalance401RequiresReauth(t *testing.T) {
	now := time.Date(2026, 10, 29, 14, 0, 0, 0, engine.Beijing)
	target := &workBuddyCheckinTarget{
		active: true, claimUnauthorized: true, balanceUnauthorized: true,
	}
	srv := target.serve(t)
	defer srv.Close()

	a, acct, accountID := newWorkBuddyCheckinApp(t, srv.URL, now)
	obs, err := a.CallTool(context.Background(), "checkin_run",
		rawJSON(`{"target":"workbuddy-cn","account_id":"`+accountID+`"}`))
	if err != nil {
		t.Fatalf("登录失效应由状态机记录: %v", err)
	}
	got := obs.(engine.Observation)
	if got.State != engine.FailedRetryable {
		t.Errorf("登录失效应可重登后重试，实际 %+v", got)
	}
	account, err := acct.Get(context.Background(), "workbuddy-cn", accountID)
	if err != nil {
		t.Fatal(err)
	}
	if account.Sources[0].Health != accounts.HealthReauthRequired {
		t.Errorf("余额也 401 应标记需重登，实际 %+v", account.Sources[0])
	}
}

func TestWorkBuddyCheckinBalanceNetworkFailureStaysRetryable(t *testing.T) {
	now := time.Date(2026, 10, 29, 14, 0, 0, 0, engine.Beijing)
	target := &workBuddyCheckinTarget{
		active: true, claimUnauthorized: true, balanceNetworkFail: true,
	}
	srv := target.serve(t)
	defer srv.Close()

	a, acct, accountID := newWorkBuddyCheckinApp(t, srv.URL, now)
	obs, err := a.CallTool(context.Background(), "checkin_run",
		rawJSON(`{"target":"workbuddy-cn","account_id":"`+accountID+`"}`))
	if err != nil {
		t.Fatalf("网络失败应由状态机记录: %v", err)
	}
	got := obs.(engine.Observation)
	if got.State != engine.FailedRetryable {
		t.Errorf("余额网络失败应可重试，实际 %+v", got)
	}
	account, err := acct.Get(context.Background(), "workbuddy-cn", accountID)
	if err != nil {
		t.Fatal(err)
	}
	if account.Sources[0].Health != accounts.HealthHealthy {
		t.Errorf("网络失败不得误标需重登或兜底，实际 %+v", account.Sources[0])
	}
}

func TestWorkBuddyManualFallbackSuppressesSameDayClaim(t *testing.T) {
	now := time.Date(2026, 10, 29, 14, 0, 0, 0, engine.Beijing)
	target := &workBuddyCheckinTarget{active: true, claimUnauthorized: true}
	srv := target.serve(t)
	defer srv.Close()

	a, _, accountID := newWorkBuddyCheckinApp(t, srv.URL, now)
	for i := 0; i < 2; i++ {
		obs, err := a.CallTool(context.Background(), "checkin_run",
			rawJSON(`{"target":"workbuddy-cn","account_id":"`+accountID+`"}`))
		if err != nil {
			t.Fatalf("第 %d 次执行失败: %v", i+1, err)
		}
		got := obs.(engine.Observation)
		if i == 1 {
			if got.State != engine.ManualRequired {
				t.Errorf("兜底后同日不得再次发送领取，实际 %+v", got)
			}
		}
	}
	if target.claims != 2 {
		t.Errorf("首次领取 401 后只可刷新重试一次，后续不得再领取，实际 %d", target.claims)
	}
}

func TestWorkBuddyManualFallbackReviewFindsClaimedToday(t *testing.T) {
	now := time.Date(2026, 10, 29, 14, 0, 0, 0, engine.Beijing)
	target := &workBuddyCheckinTarget{active: true, claimUnauthorized: true}
	srv := target.serve(t)
	defer srv.Close()

	a, _, accountID := newWorkBuddyCheckinApp(t, srv.URL, now)
	if _, err := a.CallTool(context.Background(), "checkin_run",
		rawJSON(`{"target":"workbuddy-cn","account_id":"`+accountID+`"}`)); err != nil {
		t.Fatal(err)
	}

	// The user completes check-in in WorkBuddy; QKA must only observe it.
	target.mu.Lock()
	target.checked = true
	target.mu.Unlock()
	obs, err := a.CallTool(context.Background(), "checkin_run",
		rawJSON(`{"target":"workbuddy-cn","account_id":"`+accountID+`"}`))
	if err != nil {
		t.Fatalf("复查失败: %v", err)
	}
	got := obs.(engine.Observation)
	if !got.Executed || got.State != engine.Already ||
		!strings.Contains(got.Message, "只读复查观察到今日已领取（HTTP 200）") {
		t.Errorf("复查到今日已领取应更新结果，实际 %+v", got)
	}
	var logMessage string
	if err := a.store.DB().QueryRowContext(context.Background(),
		`SELECT message FROM logs WHERE account_id = ? AND state = 'already'`,
		accountID).Scan(&logMessage); err != nil {
		t.Fatal(err)
	}
	if logMessage != got.Message {
		t.Errorf("复查证据应写入日志，实际 %q", logMessage)
	}
	if target.claims != 2 {
		t.Errorf("复查不得发送新的领取请求，实际领取总数 %d", target.claims)
	}
}

func TestWorkBuddyManualFallbackReviewWithoutClaimKeepsFallback(t *testing.T) {
	now := time.Date(2026, 10, 29, 14, 0, 0, 0, engine.Beijing)
	target := &workBuddyCheckinTarget{active: true, claimUnauthorized: true}
	srv := target.serve(t)
	defer srv.Close()

	a, _, accountID := newWorkBuddyCheckinApp(t, srv.URL, now)
	for i := 0; i < 2; i++ {
		obs, err := a.CallTool(context.Background(), "checkin_run",
			rawJSON(`{"target":"workbuddy-cn","account_id":"`+accountID+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			got := obs.(engine.Observation)
			if got.State != engine.ManualRequired ||
				!strings.Contains(got.Message, "请回 WorkBuddy App 签到") {
				t.Errorf("复查未领取时应保留兜底提示，实际 %+v", got)
			}
		}
	}
	if target.claims != 2 {
		t.Errorf("复查不得再次领取，实际领取总数 %d", target.claims)
	}
}

func TestWorkBuddyManualFallbackReview401RequiresReauth(t *testing.T) {
	now := time.Date(2026, 10, 29, 14, 0, 0, 0, engine.Beijing)
	target := &workBuddyCheckinTarget{active: true, claimUnauthorized: true}
	srv := target.serve(t)
	defer srv.Close()

	a, acct, accountID := newWorkBuddyCheckinApp(t, srv.URL, now)
	if _, err := a.CallTool(context.Background(), "checkin_run",
		rawJSON(`{"target":"workbuddy-cn","account_id":"`+accountID+`"}`)); err != nil {
		t.Fatal(err)
	}

	target.mu.Lock()
	target.statusUnauthorized = true
	target.balanceUnauthorized = true
	target.mu.Unlock()
	obs, err := a.CallTool(context.Background(), "checkin_run",
		rawJSON(`{"target":"workbuddy-cn","account_id":"`+accountID+`"}`))
	if err != nil {
		t.Fatalf("复查登录失效应由状态机记录: %v", err)
	}
	got := obs.(engine.Observation)
	if got.State != engine.FailedRetryable {
		t.Errorf("复查登录失效应可重登后重试，实际 %+v", got)
	}
	account, err := acct.Get(context.Background(), "workbuddy-cn", accountID)
	if err != nil {
		t.Fatal(err)
	}
	if account.Sources[0].Health != accounts.HealthReauthRequired {
		t.Errorf("复查 401 应标记需重登，实际 %+v", account.Sources[0])
	}
	var dailyState string
	if err := a.store.DB().QueryRowContext(context.Background(),
		`SELECT state FROM daily_ops WHERE account_id = ? AND operation = 'checkin'`,
		accountID).Scan(&dailyState); err != nil {
		t.Fatal(err)
	}
	if dailyState != string(engine.ManualRequired) {
		t.Errorf("复查需重登时不得解除当日领取抑制，实际 %q", dailyState)
	}
	if target.claims != 2 {
		t.Errorf("复查 401 不得发送领取请求，实际领取总数 %d", target.claims)
	}
}

func TestWorkBuddyManualFallbackReview401WithWorkingBalanceKeepsFallback(t *testing.T) {
	now := time.Date(2026, 10, 29, 14, 0, 0, 0, engine.Beijing)
	target := &workBuddyCheckinTarget{active: true, claimUnauthorized: true}
	srv := target.serve(t)
	defer srv.Close()

	a, acct, accountID := newWorkBuddyCheckinApp(t, srv.URL, now)
	if _, err := a.CallTool(context.Background(), "checkin_run",
		rawJSON(`{"target":"workbuddy-cn","account_id":"`+accountID+`"}`)); err != nil {
		t.Fatal(err)
	}

	target.mu.Lock()
	target.statusUnauthorized = true
	target.mu.Unlock()
	obs, err := a.CallTool(context.Background(), "checkin_run",
		rawJSON(`{"target":"workbuddy-cn","account_id":"`+accountID+`"}`))
	if err != nil {
		t.Fatalf("余额可用时复查 401 不应中止: %v", err)
	}
	got := obs.(engine.Observation)
	if got.State != engine.ManualRequired ||
		!strings.Contains(got.Message, "余额查询仍可用") {
		t.Errorf("余额可用时应保留应用内签到兜底，实际 %+v", got)
	}
	account, err := acct.Get(context.Background(), "workbuddy-cn", accountID)
	if err != nil {
		t.Fatal(err)
	}
	if account.Sources[0].Health != accounts.HealthHealthy {
		t.Errorf("余额可用时不得标记需重登，实际 %+v", account.Sources[0])
	}
	if target.claims != 2 {
		t.Errorf("复查不得发送领取请求，实际领取总数 %d", target.claims)
	}
}

func TestWorkBuddyManualFallbackReviewDoesNotSwallowCancellation(t *testing.T) {
	now := time.Date(2026, 10, 29, 14, 0, 0, 0, engine.Beijing)
	target := &workBuddyCheckinTarget{active: true, claimUnauthorized: true}
	srv := target.serve(t)
	defer srv.Close()

	a, _, accountID := newWorkBuddyCheckinApp(t, srv.URL, now)
	if _, err := a.CallTool(context.Background(), "checkin_run",
		rawJSON(`{"target":"workbuddy-cn","account_id":"`+accountID+`"}`)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.CallTool(ctx, "checkin_run",
		rawJSON(`{"target":"workbuddy-cn","account_id":"`+accountID+`"}`)); !errors.Is(err, context.Canceled) {
		t.Fatalf("复查不得吞掉取消错误，实际 %v", err)
	}
}

func TestWorkBuddyManualFallbackReviewInactiveDoesNotAskForAppCheckin(t *testing.T) {
	now := time.Date(2026, 10, 29, 14, 0, 0, 0, engine.Beijing)
	target := &workBuddyCheckinTarget{active: true, claimUnauthorized: true}
	srv := target.serve(t)
	defer srv.Close()

	a, _, accountID := newWorkBuddyCheckinApp(t, srv.URL, now)
	if _, err := a.CallTool(context.Background(), "checkin_run",
		rawJSON(`{"target":"workbuddy-cn","account_id":"`+accountID+`"}`)); err != nil {
		t.Fatal(err)
	}

	target.mu.Lock()
	target.active = false
	target.mu.Unlock()
	obs, err := a.CallTool(context.Background(), "checkin_run",
		rawJSON(`{"target":"workbuddy-cn","account_id":"`+accountID+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	got := obs.(engine.Observation)
	if strings.Contains(got.Message, "请回 WorkBuddy App 签到") ||
		!strings.Contains(got.Message, "活动未开始或已结束") {
		t.Errorf("活动结束时不得要求回 App 签到，实际 %+v", got)
	}
	if target.claims != 2 {
		t.Errorf("复查不得发送领取请求，实际领取总数 %d", target.claims)
	}
}

func TestWorkBuddyManualFallbackReconcilesFromRunAll(t *testing.T) {
	now := time.Date(2026, 10, 29, 14, 0, 0, 0, engine.Beijing)
	target := &workBuddyCheckinTarget{active: true, claimUnauthorized: true}
	srv := target.serve(t)
	defer srv.Close()

	a, _, _ := newWorkBuddyCheckinApp(t, srv.URL, now)
	if err := a.RunAll(context.Background()); err != nil {
		t.Fatalf("手动执行失败: %v", err)
	}
	var state string
	_ = a.store.DB().QueryRowContext(context.Background(),
		`SELECT state FROM daily_ops WHERE account_id = 'wb_one'`).Scan(&state)
	if state != string(engine.ManualRequired) {
		t.Fatalf("手动执行应写入 manual_required，实际 %q", state)
	}

	target.mu.Lock()
	target.checked = true
	target.mu.Unlock()
	if err := a.RunAll(context.Background()); err != nil {
		t.Fatalf("复查失败: %v", err)
	}
	_ = a.store.DB().QueryRowContext(context.Background(),
		`SELECT state FROM daily_ops WHERE account_id = 'wb_one'`).Scan(&state)
	if state != string(engine.Already) {
		t.Errorf("RunAll 复查已领取后应更新状态，实际 %q", state)
	}
	if target.claims != 2 {
		t.Errorf("RunAll 不得再次领取，实际领取总数 %d", target.claims)
	}
}
