package workbuddy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func validVerificationCredential() PendingCredential {
	return PendingCredential{
		Target: "workbuddy-cn", SessionID: "verify-session",
		Protocol: ProtocolBaseline, AccessToken: "verify-access",
		RefreshToken: "verify-refresh", Domain: "copilot.tencent.com",
		UID: "verify-uid", IdentityType: "personal",
	}
}

func TestVerificationDefaultsAndIncompleteCredentials(t *testing.T) {
	svc := NewVerificationService(VerificationConfig{})
	if svc.baseURL != DefaultBaseURL || svc.client == nil {
		t.Fatalf("验证服务缺省值不完整: %+v", svc)
	}
	for _, credential := range []PendingCredential{
		{Target: "other", SessionID: "s", AccessToken: "a", UID: "u"},
		{Target: "workbuddy-cn", AccessToken: "a", UID: "u"},
		{Target: "workbuddy-cn", SessionID: "s", UID: "u"},
		{Target: "workbuddy-cn", SessionID: "s", AccessToken: "a"},
	} {
		outcome := svc.Verify(context.Background(), credential)
		if outcome.Status != LoginFailed {
			t.Fatalf("不完整凭据应失败，credential=%+v outcome=%+v", credential, outcome)
		}
	}
}

func TestVerificationInvalidCredentialCleanupFailureIsRetryable(t *testing.T) {
	svc := NewVerificationService(VerificationConfig{
		PendingStore: &hookPendingStore{delete: func(context.Context, string) error {
			return errors.New("keychain locked")
		}},
	})
	outcome := svc.Verify(context.Background(), PendingCredential{
		Target: "workbuddy-cn", SessionID: "invalid",
	})
	if outcome.Status != LoginRetryable ||
		!strings.Contains(outcome.Message, "临时登录凭据清理失败") {
		t.Fatalf("无效凭据清理失败应要求重试: %+v", outcome)
	}
}

func TestVerificationInvalidDomainClearsPending(t *testing.T) {
	deleted := ""
	store := &hookPendingStore{
		delete: func(_ context.Context, sessionID string) error {
			deleted = sessionID
			return nil
		},
	}
	svc := NewVerificationService(VerificationConfig{PendingStore: store})
	credential := validVerificationCredential()
	credential.Domain = "bad host"
	outcome := svc.Verify(context.Background(), credential)
	if outcome.Status != LoginFailed || deleted != credential.SessionID {
		t.Fatalf("非法 domain 应清理并失败，outcome=%+v deleted=%q", outcome, deleted)
	}
}

func TestVerificationStorageBoundaryFailures(t *testing.T) {
	credential := validVerificationCredential()
	balance := func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, map[string]any{"IsPaidUser": true})
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		balance(w, r)
	}))
	defer target.Close()

	t.Run("账号存储未接线", func(t *testing.T) {
		svc := NewVerificationService(VerificationConfig{
			BaseURL: target.URL, PendingStore: newVerificationStore(),
		})
		outcome := svc.Verify(context.Background(), credential)
		if outcome.Status != LoginRetryable {
			t.Fatalf("无 sink 应 retryable，实际 %+v", outcome)
		}
	})
	t.Run("账号落库失败", func(t *testing.T) {
		svc := NewVerificationService(VerificationConfig{
			BaseURL: target.URL, PendingStore: newVerificationStore(),
			Sink: &verificationSink{err: errors.New("db locked")},
		})
		outcome := svc.Verify(context.Background(), credential)
		if outcome.Status != LoginRetryable {
			t.Fatalf("sink 失败应 retryable，实际 %+v", outcome)
		}
	})
	t.Run("待验证凭据清理失败", func(t *testing.T) {
		store := &hookPendingStore{delete: func(context.Context, string) error {
			return errors.New("keychain locked")
		}}
		svc := NewVerificationService(VerificationConfig{
			BaseURL: target.URL, PendingStore: store, Sink: &verificationSink{},
		})
		outcome := svc.Verify(context.Background(), credential)
		if outcome.Status != LoginRetryable || !strings.Contains(outcome.Message, "临时登录凭据清理失败") {
			t.Fatalf("清理失败应 retryable，实际 %+v", outcome)
		}
	})
}

func TestVerificationRequestConstructionFailures(t *testing.T) {
	svc := NewVerificationService(VerificationConfig{BaseURL: "http://[::1"})
	credential := validVerificationCredential()
	if _, err := svc.readAccounts(context.Background(), credential, "copilot.tencent.com"); !errors.Is(err, errVerificationRetry) {
		t.Fatalf("accounts 请求构造失败错误错误: %v", err)
	}
	if err := svc.readBalance(context.Background(), credential, "copilot.tencent.com"); !errors.Is(err, errVerificationRetry) {
		t.Fatalf("balance 请求构造失败错误错误: %v", err)
	}
}

func TestVerificationReadAccountsInvalidData(t *testing.T) {
	svc := NewVerificationService(VerificationConfig{
		BaseURL: "https://example.test", HTTPClient: &http.Client{
			Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return testJSONResponse(http.StatusOK, `{"code":0,"data":"bad"}`), nil
			}),
		},
	})
	if _, err := svc.readAccounts(context.Background(), validVerificationCredential(), "copilot.tencent.com"); !errors.Is(err, errVerificationRetry) {
		t.Fatalf("accounts 数据非法应可重试，实际 %v", err)
	}
}

func TestVerificationReadEnvelopeFailures(t *testing.T) {
	tests := []struct {
		name    string
		client  *http.Client
		wantErr error
	}{
		{
			name: "网络失败",
			client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, errors.New("network down")
			})},
			wantErr: errVerificationRetry,
		},
		{
			name: "普通 4xx",
			client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return testJSONResponse(http.StatusBadRequest, `{}`), nil
			})},
			wantErr: errVerificationFailed,
		},
		{
			name: "响应读取失败",
			client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Body: &failingReadCloser{
					err: errors.New("read failed"),
				}}, nil
			})},
			wantErr: errVerificationRetry,
		},
		{
			name: "响应超限",
			client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return testJSONResponse(http.StatusOK, strings.Repeat("x", maxResponseSize+1)), nil
			})},
			wantErr: errVerificationRetry,
		},
		{
			name: "响应 JSON 非法",
			client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return testJSONResponse(http.StatusOK, "{"), nil
			})},
			wantErr: errVerificationRetry,
		},
		{
			name: "业务码失败",
			client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return testJSONResponse(http.StatusOK, `{"code":123}`), nil
			})},
			wantErr: errVerificationFailed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewVerificationService(VerificationConfig{
				BaseURL: "https://example.test", HTTPClient: tt.client,
			})
			req, err := http.NewRequest(http.MethodGet, "https://example.test/check", nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := svc.readEnvelope(req); !errors.Is(err, tt.wantErr) {
				t.Fatalf("err=%v want=%v", err, tt.wantErr)
			}
		})
	}
}

func TestVerificationFailureClassification(t *testing.T) {
	store := newVerificationStore()
	svc := NewVerificationService(VerificationConfig{PendingStore: store})
	_ = store.Save(context.Background(), "session", []byte("{}"))
	outcome := svc.failure("session", errVerificationFailed)
	if outcome.Status != LoginFailed || store.has("session") {
		t.Fatalf("默认失败应清理并返回 failed，outcome=%+v", outcome)
	}
	svc.deletePending("")
	withoutStore := NewVerificationService(VerificationConfig{})
	if err := withoutStore.deletePendingStrict(context.Background(), "session"); !errors.Is(err, ErrPendingCredentialNotFound) {
		t.Fatalf("无 store 的严格清理应返回不存在，实际 %v", err)
	}
}

func TestNormalizeVerificationDomainFailures(t *testing.T) {
	if _, err := normalizeVerificationDomain("", "http://[::1"); !errors.Is(err, errVerificationFailed) {
		t.Fatalf("非法 base URL 应失败，实际 %v", err)
	}
	if _, err := normalizeVerificationDomain("bad host", "https://example.test"); !errors.Is(err, errVerificationFailed) {
		t.Fatalf("空白 domain 应失败，实际 %v", err)
	}
	if _, err := normalizeVerificationDomain("[", "https://example.test"); !errors.Is(err, errVerificationFailed) {
		t.Fatalf("非法 host 应失败，实际 %v", err)
	}
	if got, err := normalizeVerificationDomain(" COPILOT.TENCENT.COM. ", "https://example.test"); err != nil ||
		got != "copilot.tencent.com" {
		t.Fatalf("domain 规范化错误: %q %v", got, err)
	}
}

func TestInferEnterpriseIDSelectionOrder(t *testing.T) {
	credential := PendingCredential{UID: "uid", IdentityType: "enterprise"}
	sameType := inferEnterpriseID(credential, []accountData{
		{UID: "other", EnterpriseID: "ignored", Type: "enterprise"},
		{UID: "uid", EnterpriseID: "missing-type", Type: "personal", LastLogin: json.RawMessage(`true`)},
		{UID: "uid", EnterpriseID: "same-type", Type: "enterprise"},
	})
	if sameType != "same-type" {
		t.Fatalf("同身份类型优先失败: %q", sameType)
	}
	lastLogin := inferEnterpriseID(credential, []accountData{
		{UID: "uid", EnterpriseID: "not-last", Type: "personal"},
		{UID: "uid", EnterpriseID: "last", Type: "personal", LastLogin: json.RawMessage(`true`)},
	})
	if lastLogin != "last" {
		t.Fatalf("唯一 lastLogin 选择失败: %q", lastLogin)
	}
	only := inferEnterpriseID(credential, []accountData{
		{UID: "uid", EnterpriseID: "only", Type: "personal"},
	})
	if only != "only" {
		t.Fatalf("唯一匹配账号选择失败: %q", only)
	}
	ambiguous := inferEnterpriseID(credential, []accountData{
		{UID: "uid", EnterpriseID: "one", Type: "personal"},
		{UID: "uid", EnterpriseID: "two", Type: "personal"},
	})
	if ambiguous != "" {
		t.Fatalf("歧义账号不得猜测，实际 %q", ambiguous)
	}
	if got := inferEnterpriseID(credential, []accountData{
		{UID: "uid", EnterpriseID: "", Type: "enterprise"},
	}); got != "" {
		t.Fatalf("空 enterpriseId 应跳过，实际 %q", got)
	}
}

func TestJSONTruthyVariants(t *testing.T) {
	tests := []struct {
		raw  string
		want bool
	}{
		{"", false},
		{"{", false},
		{"true", true},
		{"false", false},
		{`" x "`, true},
		{`"  "`, false},
		{"2", true},
		{"0", false},
		{"[1]", true},
		{"[]", false},
		{`{"x":1}`, true},
		{"{}", false},
		{"null", false},
	}
	for _, tt := range tests {
		if got := jsonTruthy(json.RawMessage(tt.raw)); got != tt.want {
			t.Errorf("jsonTruthy(%q)=%v want=%v", tt.raw, got, tt.want)
		}
	}
}

func TestNeedsEnterpriseLookupVariants(t *testing.T) {
	if needsEnterpriseLookup(PendingCredential{}) ||
		needsEnterpriseLookup(PendingCredential{IdentityType: "personal"}) {
		t.Fatal("空身份或个人身份不应查询企业列表")
	}
	if !needsEnterpriseLookup(PendingCredential{IdentityType: " Enterprise "}) {
		t.Fatal("企业身份应查询企业列表")
	}
}
