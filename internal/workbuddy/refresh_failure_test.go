package workbuddy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func credentialBytes(t *testing.T, credential PendingCredential) []byte {
	t.Helper()
	raw, err := json.Marshal(credential)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func validRefreshCredential(now time.Time) PendingCredential {
	return PendingCredential{
		Target: "workbuddy-cn", AccessToken: "old-access",
		RefreshToken:    "old-refresh",
		AccessExpiresAt: now.Add(time.Hour).UnixMilli(),
		Domain:          "copilot.tencent.com", UID: "uid",
	}
}

func TestNewManagedSessionDefaults(t *testing.T) {
	session := NewManagedSession(ManagedSessionConfig{})
	if session.baseURL != DefaultBaseURL || session.client == nil || session.now == nil {
		t.Fatalf("受管会话缺省值不完整: %+v", session)
	}
}

func TestManagedSessionMarkReauthRequiredContract(t *testing.T) {
	session := NewManagedSession(ManagedSessionConfig{})
	if err := session.MarkReauthRequired(context.Background()); err == nil {
		t.Fatal("无凭据存储时标记重登应失败")
	}
	store := &hookManagedStore{}
	session = NewManagedSession(ManagedSessionConfig{Store: store})
	if err := session.MarkReauthRequired(context.Background()); err != nil {
		t.Fatalf("来源状态更新成功时应返回 nil，实际 %v", err)
	}
}

func TestManagedSessionDoInputAndCredentialFailures(t *testing.T) {
	now := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	t.Run("空请求", func(t *testing.T) {
		session := NewManagedSession(ManagedSessionConfig{})
		if _, err := session.Do(context.Background(), nil); err == nil {
			t.Fatal("nil 请求必须失败")
		}
	})
	t.Run("请求体读取失败", func(t *testing.T) {
		store := newManagedCredentialStore(validRefreshCredential(now))
		session := NewManagedSession(ManagedSessionConfig{Store: store})
		req, _ := http.NewRequest(http.MethodPost, "https://example.test/", nil)
		req.Body = &failingReadCloser{err: errors.New("body read failed")}
		if _, err := session.Do(context.Background(), req); err == nil {
			t.Fatal("请求体读取失败必须中止")
		}
	})
	t.Run("无凭据存储", func(t *testing.T) {
		session := NewManagedSession(ManagedSessionConfig{})
		req, _ := http.NewRequest(http.MethodGet, "https://example.test/", nil)
		if _, err := session.Do(context.Background(), req); !errors.Is(err, ErrManagedReauthRequired) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("凭据读取失败", func(t *testing.T) {
		store := &hookManagedStore{load: func(context.Context) ([]byte, error) {
			return nil, errors.New("keychain unavailable")
		}}
		session := NewManagedSession(ManagedSessionConfig{Store: store})
		req, _ := http.NewRequest(http.MethodGet, "https://example.test/", nil)
		if _, err := session.Do(context.Background(), req); !errors.Is(err, ErrManagedReauthRequired) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("凭据不完整", func(t *testing.T) {
		for _, credential := range []PendingCredential{
			{Target: "other", AccessToken: "a", UID: "u"},
			{Target: "workbuddy-cn", UID: "u"},
			{Target: "workbuddy-cn", AccessToken: "a"},
		} {
			raw := credentialBytes(t, credential)
			store := &hookManagedStore{load: func(context.Context) ([]byte, error) {
				return raw, nil
			}}
			session := NewManagedSession(ManagedSessionConfig{Store: store})
			req, _ := http.NewRequest(http.MethodGet, "https://example.test/", nil)
			if _, err := session.Do(context.Background(), req); !errors.Is(err, ErrManagedReauthRequired) {
				t.Fatalf("credential=%+v err=%v", credential, err)
			}
		}
	})
}

func TestManagedSessionSecondSendFailureAfterRefresh(t *testing.T) {
	now := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	store := newManagedCredentialStore(validRefreshCredential(now))
	var calls atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		call := calls.Add(1)
		switch call {
		case 1:
			return testJSONResponse(http.StatusUnauthorized, `{}`), nil
		case 2:
			return testJSONResponse(http.StatusOK,
				`{"code":0,"data":{"accessToken":"new-access","refreshToken":"new-refresh","expiresIn":3600}}`), nil
		default:
			return nil, errors.New("connection dropped")
		}
	})}
	session := NewManagedSession(ManagedSessionConfig{
		BaseURL: "https://example.test", HTTPClient: client, Store: store,
		Now: func() time.Time { return now },
	})
	req, _ := http.NewRequest(http.MethodPost, "https://example.test/business", strings.NewReader("{}"))
	if _, err := session.Do(context.Background(), req); err == nil {
		t.Fatal("刷新后第二次发送失败必须返回错误")
	}
}

func TestManagedSessionRefreshReloadFailure(t *testing.T) {
	now := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	credential := validRefreshCredential(now)
	credential.AccessExpiresAt = now.Add(10 * time.Second).UnixMilli()
	raw := credentialBytes(t, credential)
	var loads atomic.Int32
	store := &hookManagedStore{load: func(context.Context) ([]byte, error) {
		if loads.Add(1) == 1 {
			return raw, nil
		}
		return nil, errors.New("credential disappeared")
	}}
	session := NewManagedSession(ManagedSessionConfig{
		BaseURL: "https://example.test", Store: store,
		Now: func() time.Time { return now },
	})
	req, _ := http.NewRequest(http.MethodPost, "https://example.test/business", strings.NewReader("{}"))
	if _, err := session.Do(context.Background(), req); !errors.Is(err, ErrManagedReauthRequired) {
		t.Fatalf("刷新二次读取失败应要求重登，实际 %v", err)
	}
}

func TestManagedSessionRefreshPersistenceAndHealthFailures(t *testing.T) {
	now := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	newServer := func(t *testing.T) *httptest.Server {
		t.Helper()
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeEnvelope(t, w, map[string]any{
				"accessToken": "new-access", "refreshToken": "new-refresh", "expiresIn": 3600,
			})
		}))
	}
	baseCredential := validRefreshCredential(now)
	baseCredential.AccessExpiresAt = now.Add(10 * time.Second).UnixMilli()
	raw := credentialBytes(t, baseCredential)

	t.Run("保存刷新结果失败", func(t *testing.T) {
		target := newServer(t)
		defer target.Close()
		store := &hookManagedStore{
			load: func(context.Context) ([]byte, error) { return raw, nil },
			save: func(context.Context, []byte) error { return errors.New("disk full") },
		}
		session := NewManagedSession(ManagedSessionConfig{
			BaseURL: target.URL, Store: store, Now: func() time.Time { return now },
		})
		req, _ := http.NewRequest(http.MethodPost, target.URL+"/business", strings.NewReader("{}"))
		if _, err := session.Do(context.Background(), req); !errors.Is(err, ErrManagedReauthRequired) {
			t.Fatalf("保存失败应要求重登，实际 %v", err)
		}
	})

	t.Run("健康状态更新失败", func(t *testing.T) {
		target := newServer(t)
		defer target.Close()
		store := &hookManagedStore{
			load:    func(context.Context) ([]byte, error) { return raw, nil },
			healthy: func(context.Context) error { return errors.New("health write failed") },
		}
		session := NewManagedSession(ManagedSessionConfig{
			BaseURL: target.URL, Store: store, Now: func() time.Time { return now },
		})
		req, _ := http.NewRequest(http.MethodPost, target.URL+"/business", strings.NewReader("{}"))
		if _, err := session.Do(context.Background(), req); err == nil ||
			!strings.Contains(err.Error(), "健康状态更新失败") {
			t.Fatalf("健康状态写入失败应报错，实际 %v", err)
		}
	})

	t.Run("重登状态更新失败", func(t *testing.T) {
		expired := baseCredential
		expired.RefreshExpiresAt = now.Add(-time.Minute).UnixMilli()
		expiredRaw := credentialBytes(t, expired)
		store := &hookManagedStore{
			load:   func(context.Context) ([]byte, error) { return expiredRaw, nil },
			reauth: func(context.Context) error { return errors.New("source update failed") },
		}
		session := NewManagedSession(ManagedSessionConfig{
			BaseURL: "https://example.test", Store: store, Now: func() time.Time { return now },
		})
		req, _ := http.NewRequest(http.MethodPost, "https://example.test/business", strings.NewReader("{}"))
		_, err := session.Do(context.Background(), req)
		if !errors.Is(err, ErrManagedReauthRequired) || !strings.Contains(err.Error(), "来源状态更新失败") {
			t.Fatalf("来源状态更新失败应保留重登分类，实际 %v", err)
		}
	})
}

func TestManagedSessionRequestRefreshFailureBranches(t *testing.T) {
	now := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	current := validRefreshCredential(now)
	current.AccessExpiresAt = now.Add(10 * time.Second).UnixMilli()

	t.Run("domain 非法", func(t *testing.T) {
		bad := current
		bad.Domain = "bad host"
		session := NewManagedSession(ManagedSessionConfig{
			BaseURL: "https://example.test", Now: func() time.Time { return now },
		})
		if _, err := session.requestRefresh(context.Background(), bad); !errors.Is(err, ErrManagedReauthRequired) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("刷新请求构造失败", func(t *testing.T) {
		session := NewManagedSession(ManagedSessionConfig{
			BaseURL: "http://[::1", Now: func() time.Time { return now },
		})
		if _, err := session.requestRefresh(context.Background(), current); !errors.Is(err, ErrManagedRetryable) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("企业头透传", func(t *testing.T) {
		enterprise := current
		enterprise.EnterpriseID = "enterprise"
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("X-Enterprise-Id"); got != enterprise.EnterpriseID {
				t.Errorf("X-Enterprise-Id=%q", got)
			}
			if got := r.Header.Get("X-Tenant-Id"); got != enterprise.EnterpriseID {
				t.Errorf("X-Tenant-Id=%q", got)
			}
			writeEnvelope(t, w, map[string]any{"accessToken": "new"})
		}))
		defer target.Close()
		session := NewManagedSession(ManagedSessionConfig{
			BaseURL: target.URL, Now: func() time.Time { return now },
		})
		if _, err := session.requestRefresh(context.Background(), enterprise); err != nil {
			t.Fatal(err)
		}
	})

	tests := []struct {
		name    string
		status  int
		body    string
		wantErr error
	}{
		{name: "响应超限", status: http.StatusOK,
			body: strings.Repeat("x", maxResponseSize+1), wantErr: ErrManagedRetryable},
		{name: "其他非 2xx", status: http.StatusTeapot,
			body: `{}`, wantErr: ErrManagedRetryable},
		{name: "业务码失败", status: http.StatusOK,
			body: `{"code":123}`, wantErr: ErrManagedRetryable},
		{name: "缺少 access token", status: http.StatusOK,
			body: `{"code":0,"data":{}}`, wantErr: ErrManagedRetryable},
		{name: "字符串错误描述拒绝", status: http.StatusBadRequest,
			body: `{"error":"invalid_grant"}`, wantErr: ErrManagedReauthRequired},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer target.Close()
			session := NewManagedSession(ManagedSessionConfig{
				BaseURL: target.URL, Now: func() time.Time { return now },
			})
			_, err := session.requestRefresh(context.Background(), current)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err=%v want=%v", err, tt.wantErr)
			}
		})
	}
}

func TestRefreshBusinessCodeVariants(t *testing.T) {
	tests := []struct {
		raw  string
		code int
		ok   bool
	}{
		{"", 0, false},
		{"null", 0, false},
		{"123", 123, true},
		{`" 456 "`, 456, true},
		{`"bad"`, 0, false},
		{`{}`, 0, false},
	}
	for _, tt := range tests {
		code, ok := refreshBusinessCode(json.RawMessage(tt.raw))
		if code != tt.code || ok != tt.ok {
			t.Errorf("refreshBusinessCode(%q)=(%d,%v) want=(%d,%v)",
				tt.raw, code, ok, tt.code, tt.ok)
		}
	}
}

func TestMergeManagedTokenAllBranches(t *testing.T) {
	now := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	current := PendingCredential{
		AccessToken: "old", RefreshToken: "old-refresh",
		AccessExpiresAt: 1, RefreshExpiresAt: 2,
		Domain: "old.example", TokenType: "old-type",
	}
	got := mergeManagedToken(current, tokenData{
		AccessToken: "new", ExpiresAt: 100, RefreshExpiresAt: 200,
		Domain: "new.example", TokenType: "Bearer",
	}, now)
	if got.AccessToken != "new" || got.RefreshToken != "old-refresh" ||
		got.AccessExpiresAt != 100 || got.RefreshExpiresAt != 200 ||
		got.Domain != "new.example" || got.TokenType != "Bearer" {
		t.Fatalf("显式刷新字段合并错误: %+v", got)
	}
	got = mergeManagedToken(current, tokenData{
		AccessToken: "new", RefreshToken: "new-refresh",
		ExpiresIn: 60, RefreshExpiresIn: 120,
	}, now)
	if got.RefreshToken != "new-refresh" ||
		got.AccessExpiresAt != now.Add(time.Minute).UnixMilli() ||
		got.RefreshExpiresAt != now.Add(2*time.Minute).UnixMilli() {
		t.Fatalf("相对刷新字段合并错误: %+v", got)
	}
}

func TestManagedSessionRequestBodyAndDiscardHelpers(t *testing.T) {
	t.Run("无请求体", func(t *testing.T) {
		body, hasBody, err := readRequestBody(&http.Request{})
		if err != nil || hasBody || body != nil {
			t.Fatalf("body=%q hasBody=%v err=%v", body, hasBody, err)
		}
	})
	t.Run("请求体读取失败", func(t *testing.T) {
		if _, _, err := readRequestBody(&http.Request{
			Body: &failingReadCloser{err: errors.New("read failed")},
		}); err == nil {
			t.Fatal("请求体读取失败必须返回错误")
		}
	})
	t.Run("请求体正常读取", func(t *testing.T) {
		body, hasBody, err := readRequestBody(&http.Request{
			Body: ioNopCloserString("payload"),
		})
		if err != nil || !hasBody || string(body) != "payload" {
			t.Fatalf("body=%q hasBody=%v err=%v", body, hasBody, err)
		}
	})
	t.Run("discard 空响应", func(t *testing.T) {
		discardAndClose(nil)
	})
}

type stringReadCloser struct {
	*strings.Reader
}

func (r stringReadCloser) Close() error { return nil }

func ioNopCloserString(value string) *stringReadCloser {
	return &stringReadCloser{Reader: strings.NewReader(value)}
}

func TestManagedSessionSendRequestBodyAndHeaderBranches(t *testing.T) {
	now := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	t.Run("nil Header 与可重放请求体", func(t *testing.T) {
		var seenGetBody bool
		client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.Header.Get("X-Enterprise-Id") != "enterprise" ||
				req.Header.Get("X-Tenant-Id") != "enterprise" {
				t.Errorf("企业头缺失: %v", req.Header)
			}
			if req.Body == nil || req.GetBody == nil {
				t.Error("请求体或 GetBody 未重建")
			} else if _, err := req.GetBody(); err == nil {
				seenGetBody = true
			}
			return testJSONResponse(http.StatusOK, `{}`), nil
		})}
		session := NewManagedSession(ManagedSessionConfig{
			BaseURL: "https://example.test", HTTPClient: client, Now: func() time.Time { return now },
		})
		req, _ := http.NewRequest(http.MethodPost, "https://example.test/business", nil)
		req.Header = nil
		resp, err := session.send(context.Background(), req, PendingCredential{
			AccessToken: "access", Domain: "copilot.tencent.com",
			UID: "uid", EnterpriseID: "enterprise",
		}, []byte("{}"), true)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if !seenGetBody {
			t.Fatal("GetBody 未被调用")
		}
	})
	t.Run("无请求体", func(t *testing.T) {
		client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.Body != nil || req.ContentLength != 0 || req.GetBody != nil {
				t.Errorf("无请求体未清理: body=%v length=%d getBodySet=%v",
					req.Body, req.ContentLength, req.GetBody != nil)
			}
			return testJSONResponse(http.StatusOK, `{}`), nil
		})}
		session := NewManagedSession(ManagedSessionConfig{
			BaseURL: "https://example.test", HTTPClient: client, Now: func() time.Time { return now },
		})
		req, _ := http.NewRequest(http.MethodGet, "https://example.test/business", nil)
		resp, err := session.send(context.Background(), req, PendingCredential{
			AccessToken: "access", Domain: "copilot.tencent.com", UID: "uid",
		}, nil, false)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	})
	t.Run("发送时 domain 非法", func(t *testing.T) {
		store := &hookManagedStore{}
		session := NewManagedSession(ManagedSessionConfig{
			BaseURL: "https://example.test", Store: store, Now: func() time.Time { return now },
		})
		req, _ := http.NewRequest(http.MethodGet, "https://example.test/business", nil)
		_, err := session.send(context.Background(), req, PendingCredential{
			AccessToken: "access", Domain: "bad host", UID: "uid",
		}, nil, false)
		if !errors.Is(err, ErrManagedReauthRequired) {
			t.Fatalf("err=%v", err)
		}
	})
}

func TestRefreshEnvelopeErrorText(t *testing.T) {
	env := refreshEnvelope{
		Msg: "msg", Message: "message", Error: json.RawMessage(`"invalid_grant"`),
	}
	if got := env.errorText(); !strings.Contains(got, "invalid_grant") {
		t.Fatalf("字符串 error 未提取: %q", got)
	}
}
