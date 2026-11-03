package workbuddy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

type memoryPendingStore struct {
	mu      sync.Mutex
	payload map[string][]byte
}

type fakeVerifier struct {
	mu         sync.Mutex
	calls      int
	credential PendingCredential
	outcomes   []VerificationOutcome
}

func (f *fakeVerifier) Verify(_ context.Context, credential PendingCredential) VerificationOutcome {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.credential = credential
	index := f.calls - 1
	if index >= len(f.outcomes) {
		index = len(f.outcomes) - 1
	}
	if index < 0 {
		return VerificationOutcome{Status: LoginRetryable, Message: "请重试"}
	}
	return f.outcomes[index]
}

func newMemoryPendingStore() *memoryPendingStore {
	return &memoryPendingStore{payload: map[string][]byte{}}
}

func (s *memoryPendingStore) Save(_ context.Context, sessionID string, payload []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.payload[sessionID] = append([]byte(nil), payload...)
	return nil
}

func (s *memoryPendingStore) Load(_ context.Context, sessionID string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	payload, ok := s.payload[sessionID]
	if !ok {
		return nil, ErrPendingCredentialNotFound
	}
	return append([]byte(nil), payload...), nil
}

func (s *memoryPendingStore) Delete(_ context.Context, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.payload, sessionID)
	return nil
}

func (s *memoryPendingStore) all() map[string][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string][]byte, len(s.payload))
	for k, v := range s.payload {
		out[k] = append([]byte(nil), v...)
	}
	return out
}

func writeEnvelope(t *testing.T, w http.ResponseWriter, data any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"code": 0,
		"msg":  "ok",
		"data": data,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestBrowserLoginStoresPendingCredentialWithoutCreatingAccount(t *testing.T) {
	const (
		accessToken  = "access-token-for-test"
		refreshToken = "refresh-token-for-test"
		uid          = "uid-for-test"
	)

	var mu sync.Mutex
	var tokenPolls int
	var identityPolls int
	var opened string

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/plugin/auth/state":
			if r.Method != http.MethodPost || r.URL.Query().Get("platform") != "workbuddy" {
				t.Errorf("state 请求错误: %s %s", r.Method, r.URL.String())
			}
			writeEnvelope(t, w, map[string]any{
				"state":   "state-for-test",
				"authUrl": "http://" + r.Host + "/official-login?continue=1",
			})
		case "/v2/plugin/auth/token":
			if got := r.URL.Query().Get("state"); got != "state-for-test" {
				t.Errorf("token state 错误: %q", got)
			}
			mu.Lock()
			tokenPolls++
			n := tokenPolls
			mu.Unlock()
			if n == 1 {
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{"code": 11217, "msg": "waiting"})
				return
			}
			writeEnvelope(t, w, map[string]any{
				"accessToken":  accessToken,
				"refreshToken": refreshToken,
				"expiresIn":    3600,
				"domain":       "copilot.tencent.com",
				"tokenType":    "Bearer",
			})
		case "/v2/plugin/login/account":
			if got := r.Header.Get("Authorization"); got != "Bearer "+accessToken {
				t.Errorf("身份请求 Authorization 错误: %q", got)
			}
			mu.Lock()
			identityPolls++
			n := identityPolls
			mu.Unlock()
			if n == 1 {
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{"code": 12151, "msg": "waiting"})
				return
			}
			writeEnvelope(t, w, map[string]any{
				"uid":         uid,
				"type":        "personal",
				"nickname":    "测试账号",
				"phoneNumber": "13800138000",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer target.Close()

	store := newMemoryPendingStore()
	svc := NewLoginService(LoginConfig{
		BaseURL:      target.URL,
		PendingStore: store,
		OpenBrowser: func(raw string) error {
			opened = raw
			return nil
		},
		PollInterval: 5 * time.Millisecond,
		Timeout:      time.Second,
		Now:          func() time.Time { return time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC) },
	})

	view, err := svc.Begin(context.Background(), "workbuddy-cn")
	if err != nil {
		t.Fatalf("Begin 失败: %v", err)
	}
	if view.Status != LoginPending || view.Stage != StageWaitingForBrowser {
		t.Fatalf("发起后应等待浏览器登录，实际 %+v", view)
	}

	openedURL, err := url.Parse(opened)
	if err != nil {
		t.Fatalf("打开的登录 URL 非法: %v", err)
	}
	if openedURL.Path != "/official-login" || openedURL.Query().Get("continue") != "1" {
		t.Errorf("应打开官方 authUrl，实际 %s", opened)
	}
	if openedURL.Query().Get("version") != "2.0.0" || openedURL.Query().Get("loginSessionId") == "" {
		t.Errorf("authUrl 缺少协议参数: %s", opened)
	}
	// 必须强制走登录页而不是静默授权：否则浏览器里已有账号时，第一次点
	// 「登录 / 添加账号」只会把旧账号再授权一遍，用户得点两次才能加新账号。
	if got := openedURL.Query().Get("force_login"); got != "1" {
		t.Errorf("authUrl 必须带 force_login=1 强制切换账号，实际 %q (%s)", got, opened)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		view = svc.Status(context.Background(), "workbuddy-cn")
		if view.Status == LoginPendingVerification {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if view.Status != LoginPendingVerification {
		t.Fatalf("登录成功应只进入待验证，实际 %+v", view)
	}
	if !strings.Contains(view.Message, "尚未创建账号") {
		t.Errorf("待验证提示必须明确未创建账号，实际 %q", view.Message)
	}

	stored := store.all()
	if len(stored) != 1 {
		t.Fatalf("应只保存一条待验证凭据，实际 %d", len(stored))
	}
	var payload PendingCredential
	for _, raw := range stored {
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatalf("待验证凭据不是合法 JSON: %v", err)
		}
		if strings.Contains(string(raw), "password") || strings.Contains(string(raw), "verification") {
			t.Errorf("凭据不应包含登录秘密字段")
		}
	}
	if payload.AccessToken != accessToken || payload.RefreshToken != refreshToken || payload.UID != uid {
		t.Errorf("待验证凭据字段不完整")
	}
	if strings.Contains(view.Message, accessToken) || strings.Contains(view.Message, refreshToken) ||
		strings.Contains(view.Message, uid) || strings.Contains(view.Message, "13800138000") {
		t.Errorf("登录状态泄露敏感字段: %+v", view)
	}
}

func TestBrowserLoginRunsReadOnlyVerifierBeforePublishingVerifiedState(t *testing.T) {
	const (
		accessToken = "verified-access"
		uid         = "verified-uid-1234"
	)

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/plugin/auth/state":
			writeEnvelope(t, w, map[string]any{
				"state": "verified-state", "authUrl": "http://" + r.Host + "/official-login",
			})
		case "/v2/plugin/auth/token":
			writeEnvelope(t, w, map[string]any{
				"accessToken": accessToken, "refreshToken": "refresh",
				"expiresIn": 3600, "domain": "copilot.tencent.com",
			})
		case "/v2/plugin/login/account":
			writeEnvelope(t, w, map[string]any{
				"uid": uid, "type": "personal", "phoneNumber": "13800138000",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer target.Close()

	verifier := &fakeVerifier{outcomes: []VerificationOutcome{{
		Status: LoginVerified, Message: "只读验证通过，已添加并启用账号",
	}}}
	svc := NewLoginService(LoginConfig{
		BaseURL: target.URL, PendingStore: newMemoryPendingStore(),
		OpenBrowser:  func(string) error { return nil },
		PollInterval: 5 * time.Millisecond, Timeout: time.Second, Verifier: verifier,
	})

	if _, err := svc.Begin(context.Background(), "workbuddy-cn"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	var view LoginView
	for time.Now().Before(deadline) {
		view = svc.Status(context.Background(), "workbuddy-cn")
		if view.Status == LoginVerified {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if view.Status != LoginVerified {
		t.Fatalf("验证通过后应发布 verified，实际 %+v", view)
	}
	if verifier.calls != 1 {
		t.Fatalf("授权完成后应自动只读验证一次，实际 %d", verifier.calls)
	}
	if verifier.credential.AccessToken != accessToken || verifier.credential.UID != uid {
		t.Errorf("验证器未收到完整待验证凭据")
	}
	if strings.Contains(view.Message, accessToken) || strings.Contains(view.Message, uid) {
		t.Errorf("验证状态不得泄露凭据或完整身份: %+v", view)
	}
}

func TestBrowserLoginRetryableVerificationReusesPendingCredential(t *testing.T) {
	var mu sync.Mutex
	stateRequests := 0
	opened := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/plugin/auth/state":
			mu.Lock()
			stateRequests++
			mu.Unlock()
			writeEnvelope(t, w, map[string]any{
				"state": "retry-state", "authUrl": "http://" + r.Host + "/official-login",
			})
		case "/v2/plugin/auth/token":
			writeEnvelope(t, w, map[string]any{
				"accessToken": "retry-access", "refreshToken": "retry-refresh",
				"domain": "copilot.tencent.com",
			})
		case "/v2/plugin/login/account":
			writeEnvelope(t, w, map[string]any{"uid": "retry-uid-1234", "type": "personal"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer target.Close()

	verifier := &fakeVerifier{outcomes: []VerificationOutcome{
		{Status: LoginRetryable, Message: "无法完成只读验证，请检查网络后重试；未创建账号"},
		{Status: LoginVerified, Message: "只读验证通过，已添加并启用账号"},
	}}
	svc := NewLoginService(LoginConfig{
		BaseURL: target.URL, PendingStore: newMemoryPendingStore(),
		OpenBrowser: func(string) error {
			opened++
			return nil
		},
		PollInterval: 5 * time.Millisecond, Timeout: time.Second, Verifier: verifier,
	})

	if _, err := svc.Begin(context.Background(), "workbuddy-cn"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	var view LoginView
	for time.Now().Before(deadline) {
		view = svc.Status(context.Background(), "workbuddy-cn")
		if view.Status == LoginRetryable {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if view.Status != LoginRetryable {
		t.Fatalf("首次验证网络失败应进入 retryable，实际 %+v", view)
	}

	view, err := svc.Begin(context.Background(), "workbuddy-cn")
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != LoginVerified {
		t.Fatalf("重试应复用待验证凭据并完成验证，实际 %+v", view)
	}
	mu.Lock()
	defer mu.Unlock()
	if stateRequests != 1 || opened != 1 {
		t.Errorf("重试验证不得重新发起浏览器登录，state=%d opened=%d", stateRequests, opened)
	}
	if verifier.calls != 2 {
		t.Errorf("应验证两次，实际 %d", verifier.calls)
	}
}

func TestBrowserLoginCancelLeavesNoPendingCredential(t *testing.T) {
	var opened int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/plugin/auth/state":
			writeEnvelope(t, w, map[string]any{
				"state":   "cancel-state",
				"authUrl": "http://" + r.Host + "/official-login",
			})
		case "/v2/plugin/auth/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 11217, "msg": "waiting"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer target.Close()

	store := newMemoryPendingStore()
	svc := NewLoginService(LoginConfig{
		BaseURL:      target.URL,
		PendingStore: store,
		OpenBrowser: func(string) error {
			opened++
			return nil
		},
		PollInterval: 5 * time.Millisecond,
		Timeout:      time.Second,
	})

	view, err := svc.Begin(context.Background(), "workbuddy-cn")
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != LoginPending || opened != 1 {
		t.Fatalf("应打开一次官方登录页，实际 view=%+v opened=%d", view, opened)
	}

	view, err = svc.Cancel(context.Background(), "workbuddy-cn")
	if err != nil {
		t.Fatalf("Cancel 失败: %v", err)
	}
	if view.Status != LoginCancelled {
		t.Fatalf("取消后应 cancelled，实际 %+v", view)
	}
	if view.CanCancel || !strings.Contains(view.Message, "未创建账号") {
		t.Errorf("取消状态不可继续取消且必须说明未创建账号: %+v", view)
	}
	if got := len(store.all()); got != 0 {
		t.Errorf("取消后不得保留待验证凭据，实际 %d 条", got)
	}
}

func TestBrowserLoginTimesOutWithoutPendingCredential(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/plugin/auth/state":
			writeEnvelope(t, w, map[string]any{
				"state":   "timeout-state",
				"authUrl": "http://" + r.Host + "/official-login",
			})
		case "/v2/plugin/auth/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 11217, "msg": "waiting"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer target.Close()

	store := newMemoryPendingStore()
	svc := NewLoginService(LoginConfig{
		BaseURL:      target.URL,
		PendingStore: store,
		OpenBrowser:  func(string) error { return nil },
		PollInterval: 5 * time.Millisecond,
		Timeout:      40 * time.Millisecond,
	})

	if _, err := svc.Begin(context.Background(), "workbuddy-cn"); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(time.Second)
	var view LoginView
	for time.Now().Before(deadline) {
		view = svc.Status(context.Background(), "workbuddy-cn")
		if view.Status == LoginExpired {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if view.Status != LoginExpired {
		t.Fatalf("超时后应 expired，实际 %+v", view)
	}
	if view.CanCancel || !strings.Contains(view.Message, "未创建账号") {
		t.Errorf("超时状态不可继续取消且必须说明未创建账号: %+v", view)
	}
	if got := len(store.all()); got != 0 {
		t.Errorf("超时后不得保留待验证凭据，实际 %d 条", got)
	}
}

func TestBrowserLoginNetworkFailureIsVisibleAndRedacted(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	baseURL := target.URL
	target.Close()

	store := newMemoryPendingStore()
	svc := NewLoginService(LoginConfig{
		BaseURL:      baseURL,
		PendingStore: store,
		OpenBrowser:  func(string) error { return nil },
		PollInterval: 5 * time.Millisecond,
		Timeout:      time.Second,
	})

	view, err := svc.Begin(context.Background(), "workbuddy-cn")
	if err != nil {
		t.Fatalf("网络失败应通过可见状态返回，不应泄露底层错误: %v", err)
	}
	if view.Status != LoginFailed {
		t.Fatalf("网络失败应 failed，实际 %+v", view)
	}
	if strings.Contains(view.Message, baseURL) || strings.Contains(view.Message, "connection") {
		t.Errorf("网络失败提示不得包含底层地址或错误: %q", view.Message)
	}
	if !strings.Contains(view.Message, "未创建账号") {
		t.Errorf("网络失败必须说明未创建账号: %q", view.Message)
	}
	if got := len(store.all()); got != 0 {
		t.Errorf("网络失败后不得保留凭据，实际 %d 条", got)
	}
}

func TestBrowserLoginDuplicateBeginReusesActiveSession(t *testing.T) {
	var stateRequests int
	var opened int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/plugin/auth/state":
			stateRequests++
			writeEnvelope(t, w, map[string]any{
				"state":   "duplicate-state",
				"authUrl": "http://" + r.Host + "/official-login",
			})
		case "/v2/plugin/auth/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 11217, "msg": "waiting"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer target.Close()

	svc := NewLoginService(LoginConfig{
		BaseURL:      target.URL,
		PendingStore: newMemoryPendingStore(),
		OpenBrowser: func(string) error {
			opened++
			return nil
		},
		PollInterval: 5 * time.Millisecond,
		Timeout:      time.Second,
	})

	first, err := svc.Begin(context.Background(), "workbuddy-cn")
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Begin(context.Background(), "workbuddy-cn")
	if err != nil {
		t.Fatal(err)
	}
	if first.StartedAt != second.StartedAt || second.Status != LoginPending {
		t.Errorf("重复发起应复用同一会话，first=%+v second=%+v", first, second)
	}
	if stateRequests != 1 || opened != 1 {
		t.Errorf("重复发起不得再请求或打开窗口，state=%d opened=%d", stateRequests, opened)
	}

	_, _ = svc.Cancel(context.Background(), "workbuddy-cn")
}

func TestBrowserLoginRejectsInsecureAuthURL(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, map[string]any{
			"state":   "unsafe-state",
			"authUrl": "http://evil.example.com/login",
		})
	}))
	defer target.Close()

	opened := false
	svc := NewLoginService(LoginConfig{
		BaseURL:      target.URL,
		PendingStore: newMemoryPendingStore(),
		OpenBrowser: func(string) error {
			opened = true
			return nil
		},
	})
	view, err := svc.Begin(context.Background(), "workbuddy-cn")
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != LoginFailed || opened {
		t.Fatalf("不安全 authUrl 应失败且不得打开，view=%+v opened=%v", view, opened)
	}
	if !strings.Contains(view.Message, "官方登录入口不安全") {
		t.Errorf("应给出安全的失败提示，实际 %q", view.Message)
	}
}

func TestBrowserLoginBrowserOpenFailureIsVisible(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, map[string]any{
			"state":   "open-failure-state",
			"authUrl": "http://" + r.Host + "/official-login",
		})
	}))
	defer target.Close()

	svc := NewLoginService(LoginConfig{
		BaseURL:      target.URL,
		PendingStore: newMemoryPendingStore(),
		OpenBrowser:  func(string) error { return errors.New("browser unavailable") },
	})
	view, err := svc.Begin(context.Background(), "workbuddy-cn")
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != LoginFailed || !strings.Contains(view.Message, "无法打开") {
		t.Fatalf("浏览器启动失败应可见，实际 %+v", view)
	}
}

func TestBrowserLoginCancelWhileFetchingStateDoesNotOpenBrowser(t *testing.T) {
	stateStarted := make(chan struct{})
	releaseState := make(chan struct{})
	var opened int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(stateStarted)
		<-releaseState
		writeEnvelope(t, w, map[string]any{
			"state":   "slow-state",
			"authUrl": "http://" + r.Host + "/official-login",
		})
	}))
	defer target.Close()

	svc := NewLoginService(LoginConfig{
		BaseURL:      target.URL,
		PendingStore: newMemoryPendingStore(),
		OpenBrowser: func(string) error {
			opened++
			return nil
		},
	})

	result := make(chan LoginView, 1)
	go func() {
		view, _ := svc.Begin(context.Background(), "workbuddy-cn")
		result <- view
	}()

	<-stateStarted
	cancelled, err := svc.Cancel(context.Background(), "workbuddy-cn")
	if err != nil || cancelled.Status != LoginCancelled {
		t.Fatalf("取 state 期间应可取消，view=%+v err=%v", cancelled, err)
	}
	close(releaseState)
	<-result

	if opened != 0 {
		t.Fatalf("取消后不得再打开浏览器，实际打开 %d 次", opened)
	}
	if got := svc.Status(context.Background(), "workbuddy-cn"); got.Status != LoginCancelled {
		t.Errorf("取消状态不得被后续 state 响应覆盖，实际 %+v", got)
	}
}

func TestBrowserLoginStateFetchTimeoutIsExpired(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
	}))
	defer target.Close()

	svc := NewLoginService(LoginConfig{
		BaseURL:      target.URL,
		PendingStore: newMemoryPendingStore(),
		OpenBrowser:  func(string) error { return nil },
		Timeout:      40 * time.Millisecond,
	})
	result := make(chan LoginView, 1)
	go func() {
		view, _ := svc.Begin(context.Background(), "workbuddy-cn")
		result <- view
	}()
	<-started

	var view LoginView
	select {
	case view = <-result:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("state 请求未在超时后返回")
	}
	close(release)
	if view.Status != LoginExpired {
		t.Fatalf("state 请求超时应 expired，实际 %+v", view)
	}
}
