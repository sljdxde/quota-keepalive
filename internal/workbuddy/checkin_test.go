package workbuddy

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
)

type fakeCheckinTarget struct {
	mu                 sync.Mutex
	active             bool
	checked            bool
	statusCode         int
	claimStatusCode    int
	claimCode          int
	claimCredit        float64
	claimMsg           string
	claimDelayed       bool
	balanceStatusCode  int
	balanceNetworkFail bool
	statuses           int
	claims             int
	balances           int
}

func (f *fakeCheckinTarget) serve(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()

		if r.URL.Path != "/v2/plugin/auth/token/refresh" {
			if r.Header.Get("Authorization") != "Bearer access-for-checkin" {
				t.Errorf("Authorization 头错误: %q", r.Header.Get("Authorization"))
			}
			if r.Header.Get("X-Domain") != "copilot.tencent.com" {
				t.Errorf("X-Domain 头错误: %q", r.Header.Get("X-Domain"))
			}
			if r.Header.Get("X-User-Id") != "uid-for-checkin" {
				t.Errorf("X-User-Id 头错误: %q", r.Header.Get("X-User-Id"))
			}
			if r.Header.Get("X-Product") != "SaaS" {
				t.Errorf("X-Product 头错误: %q", r.Header.Get("X-Product"))
			}
			if r.Header.Get("X-IDE-Type") != "WorkBuddy" {
				t.Errorf("X-IDE-Type 头错误: %q", r.Header.Get("X-IDE-Type"))
			}
			if got := r.Header.Get("User-Agent"); !strings.HasPrefix(got, "WorkBuddy/") {
				t.Errorf("User-Agent 头错误: %q", got)
			}
			if r.Method != http.MethodPost {
				t.Errorf("签到接口应使用 POST，实际 %s", r.Method)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body) != 0 {
				t.Errorf("签到请求体应为空对象，实际 %v err=%v", body, err)
			}
		}

		switch r.URL.Path {
		case "/v2/plugin/auth/token/refresh":
			writeEnvelope(t, w, map[string]any{
				"accessToken":  "access-for-checkin",
				"refreshToken": "refresh-for-checkin",
				"expiresIn":    3600,
			})
		case "/v2/billing/meter/checkin-activity-status":
			f.statuses++
			if f.statusCode != 0 {
				w.WriteHeader(f.statusCode)
				return
			}
			writeEnvelope(t, w, map[string]any{
				"active":           f.active,
				"today_checked_in": f.checked,
				"streak_days":      3,
				"daily_credit":     100,
				"today_credit":     map[bool]float64{true: 100, false: 0}[f.checked],
				"total_credits":    300,
			})
		case "/v2/billing/meter/daily-checkin":
			f.claims++
			if f.claimStatusCode != 0 {
				w.WriteHeader(f.claimStatusCode)
				return
			}
			if f.claimCode != 0 {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"code": f.claimCode,
					"msg":  f.claimMsg,
				})
				return
			}
			f.checked = true
			writeEnvelope(t, w, map[string]any{
				"credit":         f.claimCredit,
				"streak_days":    4,
				"is_streak_day":  true,
				"credit_delayed": f.claimDelayed,
				"message":        f.claimMsg,
			})
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
			if f.balanceStatusCode != 0 {
				w.WriteHeader(f.balanceStatusCode)
				return
			}
			writeEnvelope(t, w, map[string]any{"IsPaidUser": true})
		default:
			t.Errorf("签到出现未预期请求: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
}

func newCheckinTestService(t *testing.T, target *fakeCheckinTarget, now time.Time) (*CheckinService, *httptest.Server) {
	t.Helper()
	srv := target.serve(t)
	t.Cleanup(srv.Close)
	store := newManagedCredentialStore(PendingCredential{
		Target: "workbuddy-cn", AccessToken: "access-for-checkin",
		RefreshToken: "refresh-for-checkin", UID: "uid-for-checkin",
		Domain: "copilot.tencent.com", AccessExpiresAt: now.Add(time.Hour).UnixMilli(),
	})
	session := NewManagedSession(ManagedSessionConfig{
		BaseURL: srv.URL, Store: store, Now: func() time.Time { return now },
	})
	return NewCheckinService(CheckinConfig{Session: session, BaseURL: srv.URL}), srv
}

func TestCheckinQueriesStatusThenClaimsOnceWhenEligible(t *testing.T) {
	now := time.Date(2026, 10, 29, 14, 0, 0, 0, time.UTC)
	target := &fakeCheckinTarget{active: true, claimCredit: 100}
	svc, _ := newCheckinTestService(t, target, now)

	result, err := svc.Checkin(context.Background())
	if err != nil {
		t.Fatalf("签到失败: %v", err)
	}
	if result.Outcome != CheckinClaimed {
		t.Errorf("应返回 claimed，实际 %+v", result)
	}
	if result.Credit != 100 || result.StreakDays != 4 {
		t.Errorf("领取回执解析错误: %+v", result)
	}
	if target.statuses != 1 || target.claims != 1 {
		t.Errorf("应先查状态再领取一次，status=%d claims=%d", target.statuses, target.claims)
	}
}

func TestCheckinResultCarriesOnlyExplicitReceiptEvidence(t *testing.T) {
	cases := []struct {
		name        string
		credit      float64
		message     string
		delayed     bool
		wantReceipt bool
	}{
		{"领取回执含 credit", 100, "", false, true},
		{"领取回执含明确消息", 0, "签到成功", false, true},
		{"领取成功但无回执", 0, "", false, false},
		{"延迟到账即使有消息也不确认", 100, "签到成功", true, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, 10, 29, 14, 0, 0, 0, time.UTC)
			target := &fakeCheckinTarget{
				active: true, claimCredit: tc.credit, claimMsg: tc.message,
				claimDelayed: tc.delayed,
			}
			svc, _ := newCheckinTestService(t, target, now)

			result, err := svc.Checkin(context.Background())
			if err != nil {
				t.Fatalf("签到失败: %v", err)
			}
			if result.Outcome != CheckinClaimed {
				t.Fatalf("应返回 claimed，实际 %+v", result)
			}
			if got := result.HasReceipt(); got != tc.wantReceipt {
				t.Errorf("回执判断错误: got=%v result=%+v", got, result)
			}
		})
	}
}

func TestCheckinSkipsClaimWhenInactiveOrAlreadyCheckedIn(t *testing.T) {
	cases := []struct {
		name    string
		target  *fakeCheckinTarget
		outcome CheckinOutcome
	}{
		{"活动未开始", &fakeCheckinTarget{active: false}, CheckinInactive},
		{"今日已领取", &fakeCheckinTarget{active: true, checked: true}, CheckinDone},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, 10, 29, 14, 0, 0, 0, time.UTC)
			svc, _ := newCheckinTestService(t, tc.target, now)

			result, err := svc.Checkin(context.Background())
			if err != nil {
				t.Fatalf("签到失败: %v", err)
			}
			if result.Outcome != tc.outcome {
				t.Errorf("期望 %s，实际 %+v", tc.outcome, result)
			}
			if tc.target.statuses != 1 || tc.target.claims != 0 {
				t.Errorf("不应发起领取，status=%d claims=%d", tc.target.statuses, tc.target.claims)
			}
		})
	}
}

func TestCheckinMapsBusinessRefusals(t *testing.T) {
	cases := []struct {
		code    int
		outcome CheckinOutcome
	}{
		{1001, CheckinDone},
		{1002, CheckinIneligible},
		{1003, CheckinInactive},
	}

	for _, tc := range cases {
		t.Run(string(tc.outcome), func(t *testing.T) {
			now := time.Date(2026, 10, 29, 14, 0, 0, 0, time.UTC)
			target := &fakeCheckinTarget{active: true, claimCode: tc.code, claimMsg: "上游原因"}
			svc, _ := newCheckinTestService(t, target, now)

			result, err := svc.Checkin(context.Background())
			if err != nil {
				t.Fatalf("业务拒绝不应成为传输错误: %v", err)
			}
			if result.Outcome != tc.outcome || result.Message != "上游原因" {
				t.Errorf("业务码 %d 映射错误: %+v", tc.code, result)
			}
			if target.claims != 1 {
				t.Errorf("应恰好尝试领取一次，实际 %d", target.claims)
			}
		})
	}
}

func TestCheckinStatus401WithSuccessfulBalanceRequiresOwnApp(t *testing.T) {
	now := time.Date(2026, 10, 29, 14, 0, 0, 0, time.UTC)
	target := &fakeCheckinTarget{active: true, statusCode: http.StatusUnauthorized}
	svc, _ := newCheckinTestService(t, target, now)

	result, err := svc.Checkin(context.Background())
	if err != nil {
		t.Fatalf("签到状态 401 但余额可用时不应成为传输错误: %v", err)
	}
	if result.Outcome != CheckinManualRequired {
		t.Errorf("应进入应用内签到兜底，实际 %+v", result)
	}
	if !strings.Contains(result.Message, "请回 WorkBuddy App 签到") {
		t.Errorf("兜底结果必须给出应用内签到下一步，实际 %q", result.Message)
	}
	if target.claims != 0 {
		t.Errorf("状态查询 401 后不得发送领取请求，实际 %d 次", target.claims)
	}
	if target.balances != 1 {
		t.Errorf("应以同一受管凭据只读查询余额一次，实际 %d 次", target.balances)
	}
}

func TestCheckinClaim401WithSuccessfulBalanceRequiresOwnApp(t *testing.T) {
	now := time.Date(2026, 10, 29, 14, 0, 0, 0, time.UTC)
	target := &fakeCheckinTarget{
		active: true, claimStatusCode: http.StatusUnauthorized,
	}
	svc, _ := newCheckinTestService(t, target, now)

	result, err := svc.Checkin(context.Background())
	if err != nil {
		t.Fatalf("领取 401 但余额可用时不应成为传输错误: %v", err)
	}
	if result.Outcome != CheckinManualRequired {
		t.Errorf("应进入应用内签到兜底，实际 %+v", result)
	}
	if !strings.Contains(result.Message, "请回 WorkBuddy App 签到") {
		t.Errorf("兜底结果必须给出应用内签到下一步，实际 %q", result.Message)
	}
	if target.claims != 2 {
		t.Errorf("领取 401 后应只刷新并重试一次，实际领取请求 %d 次", target.claims)
	}
	if target.balances != 1 {
		t.Errorf("应以同一受管凭据只读查询余额一次，实际 %d 次", target.balances)
	}
}

func TestCheckinBalance401RequiresReauth(t *testing.T) {
	now := time.Date(2026, 10, 29, 14, 0, 0, 0, time.UTC)
	target := &fakeCheckinTarget{
		active: true, claimStatusCode: http.StatusUnauthorized,
		balanceStatusCode: http.StatusUnauthorized,
	}
	svc, _ := newCheckinTestService(t, target, now)

	_, err := svc.Checkin(context.Background())
	if !errors.Is(err, ErrManagedReauthRequired) {
		t.Fatalf("余额查询也 401 时应要求重新登录，实际 %v", err)
	}
}

func TestCheckinBalance403RequiresReauth(t *testing.T) {
	now := time.Date(2026, 10, 29, 14, 0, 0, 0, time.UTC)
	target := &fakeCheckinTarget{
		active: true, claimStatusCode: http.StatusUnauthorized,
		balanceStatusCode: http.StatusForbidden,
	}
	svc, _ := newCheckinTestService(t, target, now)

	_, err := svc.Checkin(context.Background())
	if !errors.Is(err, ErrManagedReauthRequired) {
		t.Fatalf("余额查询 403 时应要求重新登录，实际 %v", err)
	}
}

func TestCheckinBalanceNetworkFailureStaysRetryable(t *testing.T) {
	now := time.Date(2026, 10, 29, 14, 0, 0, 0, time.UTC)
	target := &fakeCheckinTarget{
		active: true, claimStatusCode: http.StatusUnauthorized,
		balanceNetworkFail: true,
	}
	svc, _ := newCheckinTestService(t, target, now)

	_, err := svc.Checkin(context.Background())
	if !errors.Is(err, ErrManagedRetryable) {
		t.Fatalf("余额网络失败应可重试，实际 %v", err)
	}
	if errors.Is(err, ErrManagedReauthRequired) {
		t.Fatal("余额网络失败不得要求重新登录")
	}
}

func TestCheckinReviewOnlyQueriesStatusAndReportsCheckedIn(t *testing.T) {
	now := time.Date(2026, 10, 29, 14, 0, 0, 0, time.UTC)
	target := &fakeCheckinTarget{active: true, checked: true}
	svc, _ := newCheckinTestService(t, target, now)

	review, err := svc.Review(context.Background())
	if err != nil {
		t.Fatalf("复查失败: %v", err)
	}
	if !review.CheckedIn || review.StatusCode != http.StatusOK {
		t.Errorf("复查应观察到今日已领取，实际 %+v", review)
	}
	if target.claims != 0 {
		t.Errorf("复查不得发送领取请求，实际 %d 次", target.claims)
	}
}

func TestCheckinReview401WithWorkingBalanceStaysFallback(t *testing.T) {
	now := time.Date(2026, 10, 29, 14, 0, 0, 0, time.UTC)
	target := &fakeCheckinTarget{active: true, statusCode: http.StatusUnauthorized}
	svc, _ := newCheckinTestService(t, target, now)

	review, err := svc.Review(context.Background())
	if err != nil {
		t.Fatalf("余额可用时复查 401 不应中止: %v", err)
	}
	if review.CheckedIn || review.StatusCode != http.StatusUnauthorized {
		t.Errorf("复查应保留未领取与 401 观察，实际 %+v", review)
	}
	if target.balances != 1 {
		t.Errorf("复查 401 应只读查询余额一次，实际 %d", target.balances)
	}
	if target.claims != 0 {
		t.Errorf("复查不得发送领取请求，实际 %d 次", target.claims)
	}
}
