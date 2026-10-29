package workbuddy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

type verificationStore struct {
	mu      sync.Mutex
	payload map[string][]byte
	deleted []string
}

func newVerificationStore() *verificationStore {
	return &verificationStore{payload: map[string][]byte{}}
}

func (s *verificationStore) Save(_ context.Context, sessionID string, payload []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.payload[sessionID] = append([]byte(nil), payload...)
	return nil
}

func (s *verificationStore) Load(_ context.Context, sessionID string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, ok := s.payload[sessionID]
	if !ok {
		return nil, ErrPendingCredentialNotFound
	}
	return append([]byte(nil), raw...), nil
}

func (s *verificationStore) Delete(_ context.Context, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.payload, sessionID)
	s.deleted = append(s.deleted, sessionID)
	return nil
}

func (s *verificationStore) has(sessionID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.payload[sessionID]
	return ok
}

type verificationSink struct {
	account    VerifiedAccount
	credential []byte
	calls      int
	err        error
}

func (s *verificationSink) AcceptVerifiedAccount(_ context.Context, account VerifiedAccount, credential []byte) error {
	s.calls++
	s.account = account
	s.credential = append([]byte(nil), credential...)
	return s.err
}

func TestVerifyReadOnlyCreatesMaskedManagedAccountAndDeletesPendingCredential(t *testing.T) {
	const (
		accessToken = "access-for-test"
		uid         = "uid-for-test-123456"
		enterprise  = "enterprise-for-test"
		domain      = "copilot.tencent.com"
	)

	var mu sync.Mutex
	var paths []string
	var accountDomain, balanceDomain string

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()

		switch r.URL.Path {
		case "/v2/plugin/accounts":
			if got := r.Header.Get("Authorization"); got != "Bearer "+accessToken {
				t.Errorf("accounts Authorization 错误: %q", got)
			}
			accountDomain = r.Header.Get("X-Domain")
			writeEnvelope(t, w, map[string]any{
				"accounts": []map[string]any{{
					"uid": uid, "enterpriseId": enterprise, "type": "enterprise",
				}},
			})
		case "/billing/meter/get-user-resource-summary":
			balanceDomain = r.Header.Get("X-Domain")
			if got := r.Header.Get("Authorization"); got != "Bearer "+accessToken {
				t.Errorf("balance Authorization 错误: %q", got)
			}
			if got := r.Header.Get("X-User-Id"); got != uid {
				t.Errorf("balance X-User-Id 错误: %q", got)
			}
			if got := r.Header.Get("X-Product"); got != "SaaS" {
				t.Errorf("balance X-Product 错误: %q", got)
			}
			if got := r.Header.Get("X-IDE-Type"); got != "WorkBuddy" {
				t.Errorf("balance X-IDE-Type 错误: %q", got)
			}
			if got := r.Header.Get("X-Enterprise-Id"); got != enterprise {
				t.Errorf("balance X-Enterprise-Id 错误: %q", got)
			}
			if got := r.Header.Get("X-Tenant-Id"); got != enterprise {
				t.Errorf("balance X-Tenant-Id 错误: %q", got)
			}
			writeEnvelope(t, w, map[string]any{"IsPaidUser": true})
		default:
			t.Errorf("验证阶段出现非只读请求: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer target.Close()

	store := newVerificationStore()
	credential := PendingCredential{
		Target: "workbuddy-cn", SessionID: "session-1", Protocol: ProtocolBaseline,
		AccessToken: accessToken, RefreshToken: "refresh-for-test",
		Domain: domain, UID: uid, EnterpriseID: enterprise,
	}
	raw, _ := json.Marshal(credential)
	if err := store.Save(context.Background(), credential.SessionID, raw); err != nil {
		t.Fatal(err)
	}
	sink := &verificationSink{}
	svc := NewVerificationService(VerificationConfig{
		BaseURL:      target.URL,
		PendingStore: store,
		Sink:         sink,
	})

	outcome := svc.Verify(context.Background(), credential)
	if outcome.Status != LoginVerified {
		t.Fatalf("只读验证应通过，实际 %+v", outcome)
	}
	if sink.calls != 1 {
		t.Fatalf("验证通过后应只落库一次，实际 %d", sink.calls)
	}
	if sink.account.Target != "workbuddy-cn" || !strings.HasPrefix(sink.account.AccountID, "wb_") ||
		!strings.HasPrefix(sink.account.SourceID, "wbm_") {
		t.Errorf("账号或来源标识不稳定/不脱敏: %+v", sink.account)
	}
	if strings.Contains(sink.account.AccountID, uid) || strings.Contains(sink.account.SourceID, uid) {
		t.Errorf("稳定标识不得包含完整 uid: %+v", sink.account)
	}
	if sink.account.MaskedIdentity != "****3456" ||
		sink.account.DisplayName != sink.account.MaskedIdentity {
		t.Errorf("展示身份必须只含脱敏值，实际 %+v", sink.account)
	}
	if len(sink.credential) == 0 {
		t.Error("验证通过后应把 token bundle 交给受管凭据存储")
	}
	if store.has(credential.SessionID) {
		t.Error("验证成功后应删除待验证凭据")
	}
	if accountDomain != domain || balanceDomain != domain {
		t.Errorf("accounts 与 balance 必须使用同一 token domain，实际 %q / %q", accountDomain, balanceDomain)
	}
	if len(paths) != 2 {
		t.Errorf("验证阶段应只有 accounts 与 balance 两个请求，实际 %v", paths)
	}
}

func TestVerifyFailureDoesNotReachSink(t *testing.T) {
	const (
		accessToken = "access-for-failure"
		uid         = "uid-for-failure-123456"
		domain      = "copilot.tencent.com"
	)

	cases := []struct {
		name         string
		status       int
		accounts     []map[string]any
		closeServer  bool
		wantStatus   LoginStatus
		keepPending  bool
		responseCode int
	}{
		{
			name:        "身份不匹配",
			status:      http.StatusOK,
			accounts:    []map[string]any{{"uid": "another-user"}},
			wantStatus:  LoginFailed,
			keepPending: false,
		},
		{
			name:        "401",
			status:      http.StatusUnauthorized,
			wantStatus:  LoginReauthRequired,
			keepPending: false,
		},
		{
			name:        "403",
			status:      http.StatusForbidden,
			wantStatus:  LoginReauthRequired,
			keepPending: false,
		},
		{
			name:        "网络失败",
			closeServer: true,
			wantStatus:  LoginRetryable,
			keepPending: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var checkinRequests int
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "checkin") {
					checkinRequests++
					http.Error(w, "forbidden checkin", http.StatusInternalServerError)
					return
				}
				if r.URL.Path != "/v2/plugin/accounts" {
					http.NotFound(w, r)
					return
				}
				if tc.status != http.StatusOK {
					w.WriteHeader(tc.status)
					return
				}
				writeEnvelope(t, w, map[string]any{"accounts": tc.accounts})
			}))

			store := newVerificationStore()
			credential := PendingCredential{
				Target: "workbuddy-cn", SessionID: "session-failure", Protocol: ProtocolBaseline,
				AccessToken: accessToken, RefreshToken: "refresh", Domain: domain, UID: uid,
			}
			raw, _ := json.Marshal(credential)
			_ = store.Save(context.Background(), credential.SessionID, raw)
			sink := &verificationSink{}
			svc := NewVerificationService(VerificationConfig{
				BaseURL: target.URL, PendingStore: store, Sink: sink,
			})
			if tc.closeServer {
				target.Close()
			} else {
				defer target.Close()
			}

			outcome := svc.Verify(context.Background(), credential)
			if outcome.Status != tc.wantStatus {
				t.Fatalf("失败状态错误: %+v", outcome)
			}
			if sink.calls != 0 {
				t.Errorf("验证失败不得创建账号或保存受管凭据，实际调用 %d 次", sink.calls)
			}
			if got := store.has(credential.SessionID); got != tc.keepPending {
				t.Errorf("待验证凭据保留状态错误: got=%v want=%v", got, tc.keepPending)
			}
			if checkinRequests != 0 {
				t.Errorf("验证阶段不得调用签到接口，实际 %d 次", checkinRequests)
			}
			if tc.status == http.StatusUnauthorized || tc.status == http.StatusForbidden {
				if !strings.Contains(outcome.Message, "重新登录") {
					t.Errorf("401/403 应提示重新登录: %q", outcome.Message)
				}
			}
			if tc.closeServer && !strings.Contains(outcome.Message, "重试") {
				t.Errorf("网络失败应提示可重试: %q", outcome.Message)
			}
		})
	}
}

func TestVerifyNormalizesDomainAndUsesBaseHostAsFallback(t *testing.T) {
	cases := []struct {
		name       string
		domain     string
		wantDomain func(*url.URL) string
	}{
		{
			name:   "规范化 token domain",
			domain: "  COPILOT.TENCENT.COM.  ",
			wantDomain: func(*url.URL) string {
				return "copilot.tencent.com"
			},
		},
		{
			name:   "缺省使用基址 host",
			domain: "",
			wantDomain: func(u *url.URL) string {
				return strings.ToLower(u.Host)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var domains []string
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				domains = append(domains, r.Header.Get("X-Domain"))
				switch r.URL.Path {
				case "/v2/plugin/accounts":
					writeEnvelope(t, w, map[string]any{
						"accounts": []map[string]any{{"uid": "domain-uid"}},
					})
				case "/billing/meter/get-user-resource-summary":
					writeEnvelope(t, w, map[string]any{"IsPaidUser": false})
				default:
					http.NotFound(w, r)
				}
			}))
			defer target.Close()

			credential := PendingCredential{
				Target: "workbuddy-cn", SessionID: "domain-session", Protocol: ProtocolBaseline,
				AccessToken: "domain-access", UID: "domain-uid", Domain: tc.domain,
			}
			store := newVerificationStore()
			raw, _ := json.Marshal(credential)
			_ = store.Save(context.Background(), credential.SessionID, raw)
			svc := NewVerificationService(VerificationConfig{
				BaseURL: target.URL, PendingStore: store, Sink: &verificationSink{},
			})
			if outcome := svc.Verify(context.Background(), credential); outcome.Status != LoginVerified {
				t.Fatalf("验证应通过，实际 %+v", outcome)
			}
			parsed, _ := url.Parse(target.URL)
			want := tc.wantDomain(parsed)
			if len(domains) != 2 || domains[0] != want || domains[1] != want {
				t.Errorf("domain 应规范化为 %q 且两次一致，实际 %v", want, domains)
			}
		})
	}
}

func TestVerifyInfersEnterpriseIdentityFromAccounts(t *testing.T) {
	const enterprise = "inferred-enterprise"
	var balanceEnterprise string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/plugin/accounts":
			writeEnvelope(t, w, map[string]any{
				"accounts": []map[string]any{{
					"uid": "enterprise-uid", "enterpriseId": enterprise, "type": "enterprise",
				}},
			})
		case "/billing/meter/get-user-resource-summary":
			balanceEnterprise = r.Header.Get("X-Enterprise-Id")
			writeEnvelope(t, w, map[string]any{"IsPaidUser": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer target.Close()

	credential := PendingCredential{
		Target: "workbuddy-cn", SessionID: "enterprise-session", Protocol: ProtocolBaseline,
		AccessToken: "enterprise-access", UID: "enterprise-uid",
		Domain: "copilot.tencent.com", IdentityType: "enterprise",
	}
	store := newVerificationStore()
	raw, _ := json.Marshal(credential)
	_ = store.Save(context.Background(), credential.SessionID, raw)
	sink := &verificationSink{}
	svc := NewVerificationService(VerificationConfig{
		BaseURL: target.URL, PendingStore: store, Sink: sink,
	})

	if outcome := svc.Verify(context.Background(), credential); outcome.Status != LoginVerified {
		t.Fatalf("企业身份验证应通过，实际 %+v", outcome)
	}
	if balanceEnterprise != enterprise {
		t.Errorf("meter 请求应补全企业头，实际 %q", balanceEnterprise)
	}
	var managed PendingCredential
	if err := json.Unmarshal(sink.credential, &managed); err != nil {
		t.Fatal(err)
	}
	if managed.EnterpriseID != enterprise {
		t.Errorf("受管凭据应补全 enterpriseId，实际 %q", managed.EnterpriseID)
	}
	want := verifiedAccount(PendingCredential{
		Target: "workbuddy-cn", UID: "enterprise-uid", EnterpriseID: enterprise,
	})
	if sink.account.AccountID != want.AccountID || sink.account.SourceID != want.SourceID {
		t.Errorf("企业账号稳定标识应包含补全后的 enterpriseId: got=%+v want=%+v", sink.account, want)
	}
}
