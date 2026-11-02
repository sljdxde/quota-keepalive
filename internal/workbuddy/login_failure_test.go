package workbuddy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type hookPendingStore struct {
	save   func(context.Context, string, []byte) error
	load   func(context.Context, string) ([]byte, error)
	delete func(context.Context, string) error
}

func (s *hookPendingStore) Save(ctx context.Context, sessionID string, payload []byte) error {
	if s.save == nil {
		return nil
	}
	return s.save(ctx, sessionID, payload)
}

func (s *hookPendingStore) Load(ctx context.Context, sessionID string) ([]byte, error) {
	if s.load == nil {
		return nil, ErrPendingCredentialNotFound
	}
	return s.load(ctx, sessionID)
}

func (s *hookPendingStore) Delete(ctx context.Context, sessionID string) error {
	if s.delete == nil {
		return nil
	}
	return s.delete(ctx, sessionID)
}

func installLoginSession(svc *LoginService, id string, status LoginStatus) *loginSession {
	sess := &loginSession{
		id: id,
		view: LoginView{
			SessionID: id, Target: "workbuddy-cn", Status: status,
			Protocol: ProtocolBaseline,
		},
		cancel: func() {},
	}
	svc.sessions["workbuddy-cn"] = sess
	return sess
}

func TestLoginDefaultsUnsupportedTargetAndRandomFailure(t *testing.T) {
	svc := NewLoginService(LoginConfig{})
	if svc.baseURL != DefaultBaseURL || svc.client == nil ||
		svc.pollInterval <= 0 || svc.timeout <= 0 || svc.now == nil {
		t.Fatalf("登录服务缺省值不完整: %+v", svc)
	}
	if _, err := svc.Begin(context.Background(), "other"); !errors.Is(err, ErrUnsupportedTarget) {
		t.Fatalf("非 WorkBuddy CN target 应被拒绝，实际 %v", err)
	}
	if view := svc.Status(context.Background(), "workbuddy-cn"); view.Status != LoginIdle {
		t.Fatalf("未发起登录应为 idle，实际 %+v", view)
	}
	if _, err := svc.Cancel(context.Background(), "workbuddy-cn"); !errors.Is(err, ErrUnsupportedTarget) {
		t.Fatalf("无会话时 Cancel 应失败，实际 %v", err)
	}
	svc.deletePending("no-store")

	original := randomRead
	randomRead = func([]byte) (int, error) {
		return 0, errors.New("entropy unavailable")
	}
	t.Cleanup(func() { randomRead = original })
	if _, err := NewLoginService(LoginConfig{}).Begin(context.Background(), "workbuddy-cn"); !errors.Is(err, errCredentialStore) {
		t.Fatalf("随机 ID 失败应映射为凭据存储错误，实际 %v", err)
	}
}

func TestLoginRetryPendingVerificationFailureBranches(t *testing.T) {
	t.Run("验证器或存储未接线", func(t *testing.T) {
		svc := NewLoginService(LoginConfig{})
		installLoginSession(svc, "retry-session", LoginRetryable)
		view, err := svc.Begin(context.Background(), "workbuddy-cn")
		if err != nil {
			t.Fatal(err)
		}
		if view.Status != LoginRetryable {
			t.Fatalf("缺少验证器应保持 retryable，实际 %+v", view)
		}
	})
	t.Run("读取待验证凭据失败", func(t *testing.T) {
		svc := NewLoginService(LoginConfig{
			PendingStore: &hookPendingStore{
				load: func(context.Context, string) ([]byte, error) {
					return nil, errors.New("storage unavailable")
				},
			},
			Verifier: &fakeVerifier{},
		})
		installLoginSession(svc, "retry-session", LoginRetryable)
		view, err := svc.Begin(context.Background(), "workbuddy-cn")
		if err != nil {
			t.Fatal(err)
		}
		if view.Status != LoginReauthRequired {
			t.Fatalf("待验证凭据丢失应要求重登，实际 %+v", view)
		}
	})
	t.Run("待验证凭据损坏", func(t *testing.T) {
		deleted := ""
		svc := NewLoginService(LoginConfig{
			PendingStore: &hookPendingStore{
				load: func(context.Context, string) ([]byte, error) {
					return []byte("{"), nil
				},
				delete: func(_ context.Context, sessionID string) error {
					deleted = sessionID
					return nil
				},
			},
			Verifier: &fakeVerifier{},
		})
		installLoginSession(svc, "retry-session", LoginRetryable)
		view, err := svc.Begin(context.Background(), "workbuddy-cn")
		if err != nil {
			t.Fatal(err)
		}
		if view.Status != LoginFailed || deleted != "retry-session" {
			t.Fatalf("损坏凭据应清理并失败，view=%+v deleted=%q", view, deleted)
		}
	})
}

func TestLoginCancelCompletedSessionAndStatusBranches(t *testing.T) {
	svc := NewLoginService(LoginConfig{})
	installLoginSession(svc, "verified", LoginVerified)
	view, err := svc.Cancel(context.Background(), "workbuddy-cn")
	if err != nil || view.Status != LoginVerified {
		t.Fatalf("终态 Cancel 不应改写状态，view=%+v err=%v", view, err)
	}

	installLoginSession(svc, "pending-verification", LoginPendingVerification)
	svc.mu.Lock()
	svc.sessions["workbuddy-cn"].view.CanCancel = false
	svc.mu.Unlock()
	svc.applyVerification("workbuddy-cn", "wrong-session", VerificationOutcome{Status: LoginVerified})
	svc.fail("workbuddy-cn", "wrong-session", errors.New("stale"))
	if got := svc.Status(context.Background(), "workbuddy-cn"); got.Status != LoginPendingVerification {
		t.Fatalf("过期回调不得覆盖当前会话，实际 %+v", got)
	}
}

func TestLoginCleanupFailuresAreVisible(t *testing.T) {
	t.Run("取消登录", func(t *testing.T) {
		svc := NewLoginService(LoginConfig{
			PendingStore: &hookPendingStore{delete: func(context.Context, string) error {
				return errors.New("keychain locked")
			}},
		})
		installLoginSession(svc, "cancel", LoginPending)
		view, err := svc.Cancel(context.Background(), "workbuddy-cn")
		if err == nil || view.Status != LoginCancelled ||
			!strings.Contains(view.Message, "临时登录凭据清理失败") {
			t.Fatalf("取消后的清理失败必须可见: view=%+v err=%v", view, err)
		}
	})

	t.Run("重试验证前清理", func(t *testing.T) {
		svc := NewLoginService(LoginConfig{
			PendingStore: &hookPendingStore{
				load: func(context.Context, string) ([]byte, error) {
					return []byte("{"), nil
				},
				delete: func(context.Context, string) error {
					return errors.New("keychain locked")
				},
			},
			Verifier: &fakeVerifier{},
		})
		installLoginSession(svc, "retry", LoginRetryable)
		view, err := svc.Begin(context.Background(), "workbuddy-cn")
		if err != nil {
			t.Fatal(err)
		}
		if view.Status != LoginRetryable ||
			!strings.Contains(view.Message, "临时登录凭据清理失败") {
			t.Fatalf("损坏凭据清理失败应要求重试: %+v", view)
		}
	})

	t.Run("轮询失败后清理", func(t *testing.T) {
		svc := NewLoginService(LoginConfig{
			PendingStore: &hookPendingStore{delete: func(context.Context, string) error {
				return errors.New("keychain locked")
			}},
		})
		installLoginSession(svc, "poll", LoginPending)
		svc.fail("workbuddy-cn", "poll", errNetwork)
		view := svc.Status(context.Background(), "workbuddy-cn")
		if view.Status != LoginFailed ||
			!strings.Contains(view.Message, "临时登录凭据清理失败") {
			t.Fatalf("轮询失败后的清理失败必须可见: %+v", view)
		}
	})
}

func TestLoginApplyVerificationDefaultFailure(t *testing.T) {
	svc := NewLoginService(LoginConfig{})
	installLoginSession(svc, "verify", LoginPendingVerification)
	svc.applyVerification("workbuddy-cn", "verify", VerificationOutcome{
		Status: LoginPending, Message: "   ",
	})
	got := svc.Status(context.Background(), "workbuddy-cn")
	if got.Status != LoginFailed || got.Message != "只读验证未通过；未创建账号" {
		t.Fatalf("未知验证状态应安全失败，实际 %+v", got)
	}
}

func TestLoginBeginWithoutBrowserOpenerFails(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, map[string]any{
			"state": "state", "authUrl": "http://" + r.Host + "/login",
		})
	}))
	defer target.Close()

	svc := NewLoginService(LoginConfig{BaseURL: target.URL, PendingStore: newMemoryPendingStore()})
	view, err := svc.Begin(context.Background(), "workbuddy-cn")
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != LoginFailed || !strings.Contains(view.Message, "无法打开") {
		t.Fatalf("缺少浏览器启动器应失败，实际 %+v", view)
	}
}

func TestLoginCancelAfterStateResponseDoesNotOpenBrowser(t *testing.T) {
	stateReturned := make(chan struct{})
	releaseState := make(chan struct{})
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		close(stateReturned)
		<-releaseState
		return testJSONResponse(http.StatusOK,
			`{"code":0,"data":{"state":"state","authUrl":"https://copilot.tencent.com/login"}}`), nil
	})}
	svc := NewLoginService(LoginConfig{
		BaseURL: "https://copilot.tencent.com", HTTPClient: client,
		PendingStore: newMemoryPendingStore(),
		OpenBrowser:  func(string) error { return nil },
	})
	result := make(chan LoginView, 1)
	go func() {
		view, _ := svc.Begin(context.Background(), "workbuddy-cn")
		result <- view
	}()
	<-stateReturned
	if _, err := svc.Cancel(context.Background(), "workbuddy-cn"); err != nil {
		t.Fatal(err)
	}
	close(releaseState)
	view := <-result
	if view.Status != LoginCancelled {
		t.Fatalf("取消后完成的 state 请求不得重新打开浏览器，实际 %+v", view)
	}
}

func TestLoginRequestHelperFailures(t *testing.T) {
	t.Run("state 请求构造失败", func(t *testing.T) {
		svc := NewLoginService(LoginConfig{BaseURL: "http://[::1"})
		if _, err := svc.requestState(context.Background()); !errors.Is(err, errNetwork) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("state 业务拒绝", func(t *testing.T) {
		svc := NewLoginService(LoginConfig{BaseURL: "https://example.test", HTTPClient: &http.Client{
			Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return testJSONResponse(http.StatusOK, `{"code":7}`), nil
			}),
		}})
		if _, err := svc.requestState(context.Background()); !errors.Is(err, errLoginRejected) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("state 数据不完整", func(t *testing.T) {
		svc := NewLoginService(LoginConfig{BaseURL: "https://example.test", HTTPClient: &http.Client{
			Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return testJSONResponse(http.StatusOK, `{"code":0,"data":{}}`), nil
			}),
		}})
		if _, err := svc.requestState(context.Background()); !errors.Is(err, errInvalidResponse) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("token 请求构造失败", func(t *testing.T) {
		svc := NewLoginService(LoginConfig{BaseURL: "http://[::1"})
		if _, _, err := svc.requestToken(context.Background(), "state"); !errors.Is(err, errNetwork) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("token 业务拒绝", func(t *testing.T) {
		svc := NewLoginService(LoginConfig{BaseURL: "https://example.test", HTTPClient: &http.Client{
			Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return testJSONResponse(http.StatusOK, `{"code":7}`), nil
			}),
		}})
		if _, _, err := svc.requestToken(context.Background(), "state"); !errors.Is(err, errLoginRejected) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("token 数据不完整", func(t *testing.T) {
		svc := NewLoginService(LoginConfig{BaseURL: "https://example.test", HTTPClient: &http.Client{
			Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return testJSONResponse(http.StatusOK,
					`{"code":0,"data":{"accessToken":"access"}}`), nil
			}),
		}})
		if _, _, err := svc.requestToken(context.Background(), "state"); !errors.Is(err, errInvalidResponse) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("identity 请求构造失败", func(t *testing.T) {
		svc := NewLoginService(LoginConfig{BaseURL: "http://[::1"})
		if _, _, err := svc.requestIdentity(context.Background(), "state", tokenData{AccessToken: "a"}); !errors.Is(err, errNetwork) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("identity HTTP 失败", func(t *testing.T) {
		svc := NewLoginService(LoginConfig{BaseURL: "https://example.test", HTTPClient: &http.Client{
			Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return testJSONResponse(http.StatusInternalServerError, `{}`), nil
			}),
		}})
		if _, _, err := svc.requestIdentity(context.Background(), "state", tokenData{AccessToken: "a"}); !errors.Is(err, errLoginRejected) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("identity 业务拒绝", func(t *testing.T) {
		svc := NewLoginService(LoginConfig{BaseURL: "https://example.test", HTTPClient: &http.Client{
			Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return testJSONResponse(http.StatusOK, `{"code":7}`), nil
			}),
		}})
		if _, _, err := svc.requestIdentity(context.Background(), "state", tokenData{AccessToken: "a"}); !errors.Is(err, errLoginRejected) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("identity 数据不完整", func(t *testing.T) {
		svc := NewLoginService(LoginConfig{BaseURL: "https://example.test", HTTPClient: &http.Client{
			Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return testJSONResponse(http.StatusOK, `{"code":0,"data":{}}`), nil
			}),
		}})
		if _, _, err := svc.requestIdentity(context.Background(), "state", tokenData{AccessToken: "a"}); !errors.Is(err, errInvalidResponse) {
			t.Fatalf("err=%v", err)
		}
	})
}

func TestLoginPollFailureBranches(t *testing.T) {
	newSuccessServer := func(t *testing.T) *httptest.Server {
		t.Helper()
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v2/plugin/auth/token":
				writeEnvelope(t, w, map[string]any{
					"accessToken": "access", "refreshToken": "refresh", "expiresIn": 3600,
				})
			case "/v2/plugin/login/account":
				writeEnvelope(t, w, map[string]any{"uid": "uid"})
			default:
				http.NotFound(w, r)
			}
		}))
	}

	t.Run("身份轮询失败", func(t *testing.T) {
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v2/plugin/auth/token" {
				writeEnvelope(t, w, map[string]any{
					"accessToken": "access", "refreshToken": "refresh", "expiresIn": 3600,
				})
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer target.Close()
		svc := NewLoginService(LoginConfig{BaseURL: target.URL, PendingStore: newMemoryPendingStore()})
		installLoginSession(svc, "identity-error", LoginPending)
		svc.poll(context.Background(), "workbuddy-cn", "identity-error", "state")
		if got := svc.Status(context.Background(), "workbuddy-cn"); got.Status != LoginFailed {
			t.Fatalf("身份轮询失败应 failed，实际 %+v", got)
		}
	})

	t.Run("存储未接线", func(t *testing.T) {
		target := newSuccessServer(t)
		defer target.Close()
		svc := NewLoginService(LoginConfig{BaseURL: target.URL})
		installLoginSession(svc, "nil-store", LoginPending)
		svc.poll(context.Background(), "workbuddy-cn", "nil-store", "state")
		if got := svc.Status(context.Background(), "workbuddy-cn"); got.Status != LoginFailed {
			t.Fatalf("无凭据存储应 failed，实际 %+v", got)
		}
	})

	t.Run("保存待验证凭据失败", func(t *testing.T) {
		target := newSuccessServer(t)
		defer target.Close()
		store := &hookPendingStore{save: func(context.Context, string, []byte) error {
			return errors.New("disk full")
		}}
		svc := NewLoginService(LoginConfig{BaseURL: target.URL, PendingStore: store})
		installLoginSession(svc, "save-error", LoginPending)
		svc.poll(context.Background(), "workbuddy-cn", "save-error", "state")
		if got := svc.Status(context.Background(), "workbuddy-cn"); got.Status != LoginFailed {
			t.Fatalf("保存失败应 failed，实际 %+v", got)
		}
	})

	t.Run("保存前上下文取消", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/v2/plugin/auth/token":
				return testJSONResponse(http.StatusOK,
					`{"code":0,"data":{"accessToken":"access","refreshToken":"refresh"}}`), nil
			case "/v2/plugin/login/account":
				return &http.Response{
					StatusCode: http.StatusOK,
					Body: &closeHookReadCloser{
						Reader:  strings.NewReader(`{"code":0,"data":{"uid":"uid"}}`),
						closeFn: cancel,
					},
				}, nil
			default:
				return testJSONResponse(http.StatusNotFound, `{}`), nil
			}
		})}
		svc := NewLoginService(LoginConfig{
			BaseURL: "https://example.test", HTTPClient: client,
			PendingStore: newMemoryPendingStore(),
		})
		installLoginSession(svc, "cancel-before-save", LoginPending)
		svc.poll(ctx, "workbuddy-cn", "cancel-before-save", "state")
		if got := svc.Status(context.Background(), "workbuddy-cn"); got.Status != LoginFailed {
			t.Fatalf("保存前取消应 failed，实际 %+v", got)
		}
	})

	t.Run("保存后上下文取消", func(t *testing.T) {
		target := newSuccessServer(t)
		defer target.Close()
		ctx, cancel := context.WithCancel(context.Background())
		store := &hookPendingStore{save: func(context.Context, string, []byte) error {
			cancel()
			return nil
		}}
		svc := NewLoginService(LoginConfig{BaseURL: target.URL, PendingStore: store})
		installLoginSession(svc, "cancel-after-save", LoginPending)
		svc.poll(ctx, "workbuddy-cn", "cancel-after-save", "state")
		if got := svc.Status(context.Background(), "workbuddy-cn"); got.Status != LoginFailed {
			t.Fatalf("保存后取消应 failed，实际 %+v", got)
		}
	})

	t.Run("保存后会话已被替换", func(t *testing.T) {
		target := newSuccessServer(t)
		defer target.Close()
		started := make(chan struct{})
		release := make(chan struct{})
		deleted := make(chan string, 1)
		store := &hookPendingStore{
			save: func(context.Context, string, []byte) error {
				close(started)
				<-release
				return nil
			},
			delete: func(_ context.Context, sessionID string) error {
				deleted <- sessionID
				return nil
			},
		}
		svc := NewLoginService(LoginConfig{BaseURL: target.URL, PendingStore: store})
		installLoginSession(svc, "old-session", LoginPending)
		done := make(chan struct{})
		go func() {
			defer close(done)
			svc.poll(context.Background(), "workbuddy-cn", "old-session", "state")
		}()
		<-started
		svc.mu.Lock()
		svc.sessions["workbuddy-cn"] = &loginSession{
			id:   "new-session",
			view: LoginView{Target: "workbuddy-cn", Status: LoginVerified},
		}
		svc.mu.Unlock()
		close(release)
		<-done
		if got := <-deleted; got != "old-session" {
			t.Fatalf("被替换会话的待验证凭据应清理，实际 %q", got)
		}
	})
}

func TestLoginPollRetryCancellation(t *testing.T) {
	t.Run("token 等待取消", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: &closeHookReadCloser{
					Reader:  strings.NewReader(`{"code":11217}`),
					closeFn: cancel,
				},
			}, nil
		})}
		svc := NewLoginService(LoginConfig{
			BaseURL: "https://example.test", HTTPClient: client, PollInterval: time.Millisecond,
		})
		if _, err := svc.pollToken(ctx, "state"); !errors.Is(err, context.Canceled) {
			t.Fatalf("token 等待应可取消，实际 %v", err)
		}
	})
	t.Run("identity 等待取消", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: &closeHookReadCloser{
					Reader:  strings.NewReader(`{"code":12151}`),
					closeFn: cancel,
				},
			}, nil
		})}
		svc := NewLoginService(LoginConfig{
			BaseURL: "https://example.test", HTTPClient: client, PollInterval: time.Millisecond,
		})
		if _, err := svc.pollIdentity(ctx, "state", tokenData{AccessToken: "access"}); !errors.Is(err, context.Canceled) {
			t.Fatalf("identity 等待应可取消，实际 %v", err)
		}
	})
}

func TestLoginDoJSONFailureBranches(t *testing.T) {
	newRequest := func(t *testing.T, ctx context.Context) *http.Request {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.test/", nil)
		if err != nil {
			t.Fatal(err)
		}
		return req
	}
	tests := []struct {
		name    string
		ctx     func() (context.Context, context.CancelFunc)
		client  *http.Client
		wantErr error
	}{
		{
			name: "传输截止时间",
			ctx:  func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
			client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, context.DeadlineExceeded
			})},
			wantErr: context.DeadlineExceeded,
		},
		{
			name: "传输取消",
			ctx:  func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
			client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, context.Canceled
			})},
			wantErr: context.Canceled,
		},
		{
			name: "普通网络失败",
			ctx:  func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
			client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, errors.New("dial failed")
			})},
			wantErr: errNetwork,
		},
		{
			name: "请求超时状态",
			ctx:  func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
			client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return testJSONResponse(http.StatusRequestTimeout, `{}`), nil
			})},
		},
		{
			name: "非 2xx",
			ctx:  func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
			client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return testJSONResponse(http.StatusBadRequest, `{}`), nil
			})},
			wantErr: errLoginRejected,
		},
		{
			name: "响应体超限",
			ctx:  func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
			client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return testJSONResponse(http.StatusOK, strings.Repeat("x", maxResponseSize+1)), nil
			})},
			wantErr: errInvalidResponse,
		},
		{
			name: "响应 JSON 非法",
			ctx:  func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
			client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return testJSONResponse(http.StatusOK, "{"), nil
			})},
			wantErr: errInvalidResponse,
		},
		{
			name: "响应体读取失败",
			ctx:  func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
			client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Body: &failingReadCloser{err: errors.New("read failed")}}, nil
			})},
			wantErr: errInvalidResponse,
		},
		{
			name: "响应体读取截止",
			ctx:  func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
			client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Body: &failingReadCloser{err: context.DeadlineExceeded}}, nil
			})},
			wantErr: context.DeadlineExceeded,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := tt.ctx()
			defer cancel()
			svc := NewLoginService(LoginConfig{HTTPClient: tt.client})
			env, err := svc.doJSON(newRequest(t, ctx))
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err=%v want=%v", err, tt.wantErr)
				}
				return
			}
			if err != nil || env.Code != 11217 {
				t.Fatalf("env=%+v err=%v", env, err)
			}
		})
	}

	t.Run("响应体读取时请求已取消", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		svc := NewLoginService(LoginConfig{HTTPClient: &http.Client{
			Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       &failingReadCloser{err: errors.New("read failed")},
				}, nil
			}),
		}})
		if _, err := svc.doJSON(newRequest(t, ctx)); !errors.Is(err, context.Canceled) {
			t.Fatalf("请求上下文已取消时应返回取消，实际 %v", err)
		}
	})
}

func TestLoginURLAndMessageSafetyHelpers(t *testing.T) {
	svc := NewLoginService(LoginConfig{BaseURL: "https://example.test"})
	if _, err := svc.prepareAuthURL("%"); !errors.Is(err, errUnsafeAuthURL) {
		t.Fatalf("非法 URL 应拒绝，实际 %v", err)
	}
	if _, err := svc.prepareAuthURL("/relative"); !errors.Is(err, errUnsafeAuthURL) {
		t.Fatalf("无 host URL 应拒绝，实际 %v", err)
	}
	if svc.loopbackAllowed(mustURL(t, "https://localhost/login")) {
		t.Fatal("HTTPS 不应走 loopback 例外")
	}
	if NewLoginService(LoginConfig{BaseURL: "https://example.test"}).
		loopbackAllowed(mustURL(t, "http://localhost/login")) {
		t.Fatal("非 HTTP API base 不应允许 loopback")
	}

	original := randomRead
	randomRead = func([]byte) (int, error) {
		return 0, errors.New("entropy unavailable")
	}
	t.Cleanup(func() { randomRead = original })
	if _, err := svc.prepareAuthURL("https://example.test/login"); !errors.Is(err, errCredentialStore) {
		t.Fatalf("auth URL 随机 ID 失败应映射为存储错误，实际 %v", err)
	}
}

func TestLoginPublicMessages(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{errNetwork, errNetwork.Error()},
		{errUnsafeAuthURL, errUnsafeAuthURL.Error()},
		{errBrowserOpen, errBrowserOpen.Error()},
		{errCredentialStore, errCredentialStore.Error()},
		{errLoginRejected, errLoginRejected.Error()},
		{errors.New("secret internal detail"), errInvalidResponse.Error()},
	}
	for _, tt := range tests {
		if got := publicMessage(tt.err); got != tt.want {
			t.Errorf("publicMessage(%v)=%q want=%q", tt.err, got, tt.want)
		}
	}
}

func TestLoginPendingCredentialExpiryAndMasking(t *testing.T) {
	now := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	svc := NewLoginService(LoginConfig{
		BaseURL: "https://copilot.tencent.com",
		Now:     func() time.Time { return now },
	})
	direct := svc.pendingCredential("workbuddy-cn", "session", tokenData{
		AccessToken: "access", RefreshToken: "refresh",
		ExpiresAt: 1234, RefreshExpiresAt: 5678, TokenType: "Bearer",
	}, accountData{UID: "uid", PhoneNumber: "13800138000"})
	if direct.AccessExpiresAt != 1234 || direct.RefreshExpiresAt != 5678 ||
		direct.Domain != "copilot.tencent.com" || direct.MaskedIdentity != "****8000" {
		t.Fatalf("显式到期时间/缺省 domain 错误: %+v", direct)
	}
	relative := svc.pendingCredential("workbuddy-cn", "session", tokenData{
		AccessToken: "access", RefreshToken: "refresh",
		ExpiresIn: 60, RefreshExpiresIn: 120,
	}, accountData{UID: "abc"})
	if relative.AccessExpiresAt != now.Add(time.Minute).UnixMilli() ||
		relative.RefreshExpiresAt != now.Add(2*time.Minute).UnixMilli() ||
		relative.MaskedIdentity != "****" {
		t.Fatalf("相对到期时间/短身份脱敏错误: %+v", relative)
	}

	if got := (PendingCredential{Target: "workbuddy-cn", SessionID: "s", AccessToken: "secret"}).String(); strings.Contains(got, "secret") {
		t.Fatalf("String 不得泄露 token: %q", got)
	}

	invalidBase := NewLoginService(LoginConfig{BaseURL: "http://[::1"})
	if got := invalidBase.effectiveDomain(tokenData{}); got != "" {
		t.Fatalf("非法 base URL 应返回空 domain，实际 %q", got)
	}
}

func TestLoginFailWithoutMatchingSession(t *testing.T) {
	svc := NewLoginService(LoginConfig{})
	svc.fail("workbuddy-cn", "missing", errors.New("stale"))
	if got := svc.Status(context.Background(), "workbuddy-cn"); got.Status != LoginIdle {
		t.Fatalf("无匹配会话时 fail 不应创建状态，实际 %+v", got)
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func TestLoginDoJSONInvalidTokenBodyStillParsedByCaller(t *testing.T) {
	svc := NewLoginService(LoginConfig{HTTPClient: &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return testJSONResponse(http.StatusOK, string([]byte{0x7f})), nil
		}),
	}})
	req, err := http.NewRequest(http.MethodGet, "https://example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.doJSON(req); !errors.Is(err, errInvalidResponse) {
		t.Fatalf("非法 JSON 应失败，实际 %v", err)
	}
}

var _ io.Reader = (*closeHookReadCloser)(nil)
