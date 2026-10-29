package workbuddy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type managedCredentialStore struct {
	mu         sync.Mutex
	credential PendingCredential
	saves      int
	healthy    int
	reauth     int
}

func newManagedCredentialStore(credential PendingCredential) *managedCredentialStore {
	return &managedCredentialStore{credential: credential}
}

func (s *managedCredentialStore) Load(context.Context) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(s.credential); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (s *managedCredentialStore) Save(_ context.Context, payload []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var credential PendingCredential
	if err := json.Unmarshal(payload, &credential); err != nil {
		return err
	}
	s.credential = credential
	s.saves++
	return nil
}

func (s *managedCredentialStore) MarkHealthy(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.healthy++
	return nil
}

func (s *managedCredentialStore) MarkReauthRequired(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reauth++
	return nil
}

func (s *managedCredentialStore) savedCredential(t *testing.T) PendingCredential {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.credential
}

func TestManagedSessionRefreshesExpiringTokenBeforeRequest(t *testing.T) {
	const (
		oldAccess  = "access-for-expiry"
		oldRefresh = "refresh-for-expiry"
		newAccess  = "access-renewed"
		newRefresh = "refresh-renewed"
		uid        = "uid-for-expiry"
		domain     = "copilot.tencent.com"
	)
	now := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	store := newManagedCredentialStore(PendingCredential{
		Target: "workbuddy-cn", AccessToken: oldAccess, RefreshToken: oldRefresh,
		AccessExpiresAt: now.Add(30 * time.Second).UnixMilli(),
		Domain:          domain, UID: uid,
	})

	var refreshCalls int
	var requestAccess string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/plugin/auth/token/refresh":
			refreshCalls++
			if r.Method != http.MethodPost {
				t.Errorf("刷新方法错误: %s", r.Method)
			}
			if got := r.Header.Get("X-Refresh-Token"); got != oldRefresh {
				t.Errorf("刷新 token 头错误: %q", got)
			}
			if got := r.Header.Get("X-Auth-Refresh-Source"); got != "plugin" {
				t.Errorf("刷新来源头错误: %q", got)
			}
			if got := r.Header.Get("X-Domain"); got != domain {
				t.Errorf("刷新 domain 错误: %q", got)
			}
			writeEnvelope(t, w, map[string]any{
				"accessToken": newAccess, "refreshToken": newRefresh,
				"expiresIn": 3600, "domain": domain,
			})
		case "/billing/meter/get-user-resource-summary":
			requestAccess = r.Header.Get("Authorization")
			writeEnvelope(t, w, map[string]any{"IsPaidUser": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer target.Close()

	session := NewManagedSession(ManagedSessionConfig{
		BaseURL: target.URL, Store: store, Now: func() time.Time { return now },
	})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		target.URL+"/billing/meter/get-user-resource-summary", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := session.Do(context.Background(), req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	resp.Body.Close()

	if refreshCalls != 1 {
		t.Errorf("临期 token 应在请求前刷新一次，实际 %d 次", refreshCalls)
	}
	if requestAccess != "Bearer "+newAccess {
		t.Errorf("业务请求应使用刷新后的 token，实际 %q", requestAccess)
	}
	saved := store.savedCredential(t)
	if saved.AccessToken != newAccess || saved.RefreshToken != newRefresh {
		t.Errorf("刷新结果未安全持久化: %+v", saved)
	}
	if saved.AccessExpiresAt <= now.UnixMilli() {
		t.Errorf("刷新后的到期时间未更新: %d", saved.AccessExpiresAt)
	}
	if store.healthy != 1 || store.reauth != 0 {
		t.Errorf("刷新成功应恢复来源健康，healthy=%d reauth=%d", store.healthy, store.reauth)
	}
}

func TestManagedSessionRetriesOnceAfter401(t *testing.T) {
	const (
		oldAccess  = "access-before-401"
		oldRefresh = "refresh-before-401"
		newAccess  = "access-after-401"
		uid        = "uid-after-401"
		domain     = "copilot.tencent.com"
	)
	now := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	store := newManagedCredentialStore(PendingCredential{
		Target: "workbuddy-cn", AccessToken: oldAccess, RefreshToken: oldRefresh,
		AccessExpiresAt: now.Add(time.Hour).UnixMilli(), Domain: domain, UID: uid,
	})

	var refreshCalls, requestCalls int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/plugin/auth/token/refresh":
			refreshCalls++
			writeEnvelope(t, w, map[string]any{
				"accessToken": newAccess, "refreshToken": "refresh-after-401", "expiresIn": 3600,
			})
		case "/billing/meter/get-user-resource-summary":
			requestCalls++
			body, err := io.ReadAll(r.Body)
			if err != nil || string(body) != "{}" {
				t.Errorf("重试请求体未保留: %q err=%v", body, err)
			}
			if requestCalls == 1 {
				if got := r.Header.Get("Authorization"); got != "Bearer "+oldAccess {
					t.Errorf("首次请求 token 错误: %q", got)
				}
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if got := r.Header.Get("Authorization"); got != "Bearer "+newAccess {
				t.Errorf("重试请求 token 错误: %q", got)
			}
			writeEnvelope(t, w, map[string]any{"IsPaidUser": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer target.Close()

	session := NewManagedSession(ManagedSessionConfig{
		BaseURL: target.URL, Store: store, Now: func() time.Time { return now },
	})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		target.URL+"/billing/meter/get-user-resource-summary", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := session.Do(context.Background(), req)
	if err != nil {
		t.Fatalf("401 后刷新重试失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("重试后应成功，实际 HTTP %d", resp.StatusCode)
	}
	if refreshCalls != 1 || requestCalls != 2 {
		t.Errorf("401 后应只刷新一次并重试一次，refresh=%d request=%d", refreshCalls, requestCalls)
	}
	if store.reauth != 0 {
		t.Errorf("刷新重试成功不应标记需重登，实际 %d", store.reauth)
	}
}

func TestManagedSessionConcurrent401RefreshesOnce(t *testing.T) {
	const (
		oldAccess  = "access-concurrent"
		oldRefresh = "refresh-concurrent"
		newAccess  = "access-concurrent-renewed"
		uid        = "uid-concurrent"
		domain     = "copilot.tencent.com"
	)
	now := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	store := newManagedCredentialStore(PendingCredential{
		Target: "workbuddy-cn", AccessToken: oldAccess, RefreshToken: oldRefresh,
		AccessExpiresAt: now.Add(time.Hour).UnixMilli(), Domain: domain, UID: uid,
	})

	var mu sync.Mutex
	var refreshCalls, requestCalls int
	oldSeen := make(chan struct{})
	oldRequests := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/plugin/auth/token/refresh":
			mu.Lock()
			refreshCalls++
			mu.Unlock()
			select {
			case <-oldSeen:
			case <-time.After(2 * time.Second):
				t.Error("并发请求未在刷新前同时使用旧 token")
			}
			writeEnvelope(t, w, map[string]any{
				"accessToken": newAccess, "refreshToken": "{{SECRET_4sdycmje}}", "expiresIn": 30,
			})
		case "/billing/meter/get-user-resource-summary":
			mu.Lock()
			requestCalls++
			isOld := r.Header.Get("Authorization") == "Bearer "+oldAccess
			if isOld {
				oldRequests++
				if oldRequests == 2 {
					close(oldSeen)
				}
			}
			mu.Unlock()
			if isOld {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			writeEnvelope(t, w, map[string]any{"IsPaidUser": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer target.Close()

	start := make(chan struct{})
	results := make(chan error, 2)
	session := NewManagedSession(ManagedSessionConfig{
		BaseURL: target.URL, Store: store, Now: func() time.Time { return now },
	})
	for range 2 {
		go func() {
			<-start
			req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
				target.URL+"/billing/meter/get-user-resource-summary", strings.NewReader("{}"))
			if err != nil {
				results <- err
				return
			}
			resp, err := session.Do(context.Background(), req)
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					err = errors.New("并发请求未成功")
				}
			}
			results <- err
		}()
	}
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if refreshCalls != 1 {
		t.Errorf("并发请求不得重复消费 refresh token，实际刷新 %d 次", refreshCalls)
	}
	if requestCalls != 4 {
		t.Errorf("两个请求各应重试一次，实际业务请求 %d 次", requestCalls)
	}
}

func TestManagedSessionSecond401MarksReauthWithoutThirdAttempt(t *testing.T) {
	const (
		oldAccess  = "access-double-401"
		oldRefresh = "refresh-double-401"
		newAccess  = "access-double-401-renewed"
		uid        = "uid-double-401"
		domain     = "copilot.tencent.com"
	)
	now := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	store := newManagedCredentialStore(PendingCredential{
		Target: "workbuddy-cn", AccessToken: oldAccess, RefreshToken: oldRefresh,
		AccessExpiresAt: now.Add(time.Hour).UnixMilli(), Domain: domain, UID: uid,
	})

	var refreshCalls, requestCalls int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/plugin/auth/token/refresh":
			refreshCalls++
			writeEnvelope(t, w, map[string]any{
				"accessToken": newAccess, "refreshToken": "{{SECRET_9d7nq4ka}}", "expiresIn": 3600,
			})
		case "/billing/meter/get-user-resource-summary":
			requestCalls++
			w.WriteHeader(http.StatusUnauthorized)
		default:
			http.NotFound(w, r)
		}
	}))
	defer target.Close()

	session := NewManagedSession(ManagedSessionConfig{
		BaseURL: target.URL, Store: store, Now: func() time.Time { return now },
	})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		target.URL+"/billing/meter/get-user-resource-summary", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = session.Do(context.Background(), req)
	if !errors.Is(err, ErrManagedReauthRequired) {
		t.Fatalf("第二次 401 应要求重新登录，实际 %v", err)
	}
	if refreshCalls != 1 || requestCalls != 2 {
		t.Errorf("再次 401 后不得继续重试，refresh=%d request=%d", refreshCalls, requestCalls)
	}
	if store.reauth != 1 {
		t.Errorf("再次 401 应标记需重登，实际 %d", store.reauth)
	}
}

func TestManagedSessionAllowUnauthorizedExposesFinal401WithoutMarkingReauth(t *testing.T) {
	const (
		oldAccess  = "access-endpoint-401"
		oldRefresh = "refresh-endpoint-401"
		newAccess  = "access-endpoint-401-renewed"
		uid        = "uid-endpoint-401"
		domain     = "copilot.tencent.com"
	)
	now := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	store := newManagedCredentialStore(PendingCredential{
		Target: "workbuddy-cn", AccessToken: oldAccess, RefreshToken: oldRefresh,
		AccessExpiresAt: now.Add(time.Hour).UnixMilli(), Domain: domain, UID: uid,
	})

	var refreshCalls, requestCalls int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/plugin/auth/token/refresh":
			refreshCalls++
			writeEnvelope(t, w, map[string]any{
				"accessToken": newAccess, "refreshToken": "{{SECRET_m9q4z8wa}}", "expiresIn": 3600,
			})
		case "/v2/billing/meter/daily-checkin":
			requestCalls++
			w.WriteHeader(http.StatusUnauthorized)
		default:
			http.NotFound(w, r)
		}
	}))
	defer target.Close()

	session := NewManagedSession(ManagedSessionConfig{
		BaseURL: target.URL, Store: store, Now: func() time.Time { return now },
	})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		target.URL+"/v2/billing/meter/daily-checkin", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := session.DoAllowUnauthorized(context.Background(), req)
	if err != nil {
		t.Fatalf("允许暴露 401 的请求不应变成错误: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("应把刷新重试后的原始 401 交给调用方，实际 HTTP %d", resp.StatusCode)
	}
	if refreshCalls != 1 || requestCalls != 2 {
		t.Errorf("仍应刷新一次并重试一次，refresh=%d request=%d", refreshCalls, requestCalls)
	}
	if store.reauth != 0 {
		t.Errorf("单个接口 401 不得直接标记登录态失效，实际 reauth=%d", store.reauth)
	}
}

func TestManagedSessionRetryableRefreshFailureLeavesHealthUnchanged(t *testing.T) {
	for _, status := range []int{
		http.StatusRequestTimeout,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			now := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
			store := newManagedCredentialStore(PendingCredential{
				Target: "workbuddy-cn", AccessToken: "access-retryable",
				RefreshToken:    "refresh-retryable",
				AccessExpiresAt: now.Add(10 * time.Second).UnixMilli(),
				Domain:          "copilot.tencent.com", UID: "uid-retryable",
			})

			var requestCalls int
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v2/plugin/auth/token/refresh":
					w.WriteHeader(status)
				case "/billing/meter/get-user-resource-summary":
					requestCalls++
					writeEnvelope(t, w, map[string]any{"IsPaidUser": true})
				default:
					http.NotFound(w, r)
				}
			}))
			defer target.Close()

			session := NewManagedSession(ManagedSessionConfig{
				BaseURL: target.URL, Store: store, Now: func() time.Time { return now },
			})
			req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
				target.URL+"/billing/meter/get-user-resource-summary", strings.NewReader("{}"))
			if err != nil {
				t.Fatal(err)
			}
			_, err = session.Do(context.Background(), req)
			if !errors.Is(err, ErrManagedRetryable) {
				t.Fatalf("HTTP %d 应返回可重试错误，实际 %v", status, err)
			}
			if errors.Is(err, ErrManagedReauthRequired) {
				t.Fatalf("HTTP %d 不得要求重新登录", status)
			}
			if requestCalls != 0 {
				t.Errorf("临期刷新失败时不得继续使用旧 token 发请求，实际 %d 次", requestCalls)
			}
			if store.reauth != 0 || store.healthy != 0 {
				t.Errorf("可重试刷新失败不得改变来源健康，healthy=%d reauth=%d",
					store.healthy, store.reauth)
			}
		})
	}
}

func TestManagedSessionRetryableRefreshAfter401LeavesHealthUnchanged(t *testing.T) {
	const (
		oldAccess  = "access-401-retryable"
		oldRefresh = "refresh-401-retryable"
		uid        = "uid-401-retryable"
		domain     = "copilot.tencent.com"
	)
	now := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	store := newManagedCredentialStore(PendingCredential{
		Target: "workbuddy-cn", AccessToken: oldAccess, RefreshToken: oldRefresh,
		AccessExpiresAt: now.Add(time.Hour).UnixMilli(), Domain: domain, UID: uid,
	})

	var refreshCalls, requestCalls int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/plugin/auth/token/refresh":
			refreshCalls++
			w.WriteHeader(http.StatusTooManyRequests)
		case "/billing/meter/get-user-resource-summary":
			requestCalls++
			w.WriteHeader(http.StatusUnauthorized)
		default:
			http.NotFound(w, r)
		}
	}))
	defer target.Close()

	session := NewManagedSession(ManagedSessionConfig{
		BaseURL: target.URL, Store: store, Now: func() time.Time { return now },
	})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		target.URL+"/billing/meter/get-user-resource-summary", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = session.Do(context.Background(), req)
	if !errors.Is(err, ErrManagedRetryable) {
		t.Fatalf("401 后刷新遇到 429 应可重试，实际 %v", err)
	}
	if errors.Is(err, ErrManagedReauthRequired) {
		t.Fatal("401 后刷新遇到 429 不得要求重新登录")
	}
	if refreshCalls != 1 || requestCalls != 1 {
		t.Errorf("应只重试刷新一次且不再发业务请求，refresh=%d request=%d",
			refreshCalls, requestCalls)
	}
	if store.reauth != 0 || store.healthy != 0 {
		t.Errorf("可重试刷新失败不得改变来源健康，healthy=%d reauth=%d",
			store.healthy, store.reauth)
	}
}

func TestManagedSessionNetworkRefreshFailureLeavesHealthUnchanged(t *testing.T) {
	now := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	store := newManagedCredentialStore(PendingCredential{
		Target: "workbuddy-cn", AccessToken: "access-network",
		RefreshToken:    "refresh-network",
		AccessExpiresAt: now.Add(10 * time.Second).UnixMilli(),
		Domain:          "copilot.tencent.com", UID: "uid-network",
	})
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	baseURL := target.URL
	target.Close()

	session := NewManagedSession(ManagedSessionConfig{
		BaseURL: baseURL, Store: store, Now: func() time.Time { return now },
	})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		baseURL+"/billing/meter/get-user-resource-summary", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = session.Do(context.Background(), req)
	if !errors.Is(err, ErrManagedRetryable) {
		t.Fatalf("网络失败应返回可重试错误，实际 %v", err)
	}
	if errors.Is(err, ErrManagedReauthRequired) {
		t.Fatal("网络失败不得要求重新登录")
	}
	if store.reauth != 0 || store.healthy != 0 {
		t.Errorf("网络失败不得改变来源健康，healthy=%d reauth=%d",
			store.healthy, store.reauth)
	}
}

func TestManagedSessionRefreshRejectionMarksReauth(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "HTTP 401", status: http.StatusUnauthorized},
		{name: "HTTP 403", status: http.StatusForbidden},
		{name: "business 12153", status: http.StatusOK,
			body: `{"code":12153,"msg":"refresh token failed"}`},
		{name: "invalid_grant", status: http.StatusBadRequest,
			body: `{"code":10000,"msg":"invalid_grant: Invalid refresh token"}`},
		{name: "nested invalid_grant", status: http.StatusBadRequest,
			body: `{"error":{"message":"invalid_grant: Invalid refresh token"}}`},
		{name: "token format error", status: http.StatusBadRequest,
			body: `{"code":10000,"msg":"token format error"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
			store := newManagedCredentialStore(PendingCredential{
				Target: "workbuddy-cn", AccessToken: "access-rejected",
				RefreshToken:    "refresh-rejected",
				AccessExpiresAt: now.Add(10 * time.Second).UnixMilli(),
				Domain:          "copilot.tencent.com", UID: "uid-rejected",
			})
			var requestCalls int
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v2/plugin/auth/token/refresh":
					w.WriteHeader(tt.status)
					_, _ = io.WriteString(w, tt.body)
				case "/billing/meter/get-user-resource-summary":
					requestCalls++
					writeEnvelope(t, w, map[string]any{"IsPaidUser": true})
				default:
					http.NotFound(w, r)
				}
			}))
			defer target.Close()

			session := NewManagedSession(ManagedSessionConfig{
				BaseURL: target.URL, Store: store, Now: func() time.Time { return now },
			})
			req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
				target.URL+"/billing/meter/get-user-resource-summary", strings.NewReader("{}"))
			if err != nil {
				t.Fatal(err)
			}
			_, err = session.Do(context.Background(), req)
			if !errors.Is(err, ErrManagedReauthRequired) {
				t.Fatalf("刷新凭据被拒绝应要求重新登录，实际 %v", err)
			}
			if errors.Is(err, ErrManagedRetryable) {
				t.Fatal("凭据被拒绝不得归类为可重试")
			}
			if requestCalls != 0 {
				t.Errorf("刷新被拒绝后不得继续业务请求，实际 %d 次", requestCalls)
			}
			if store.reauth != 1 || store.healthy != 0 {
				t.Errorf("刷新被拒绝应仅标记需重登，healthy=%d reauth=%d",
					store.healthy, store.reauth)
			}
		})
	}
}

func TestManagedSessionUnparseableRefreshResponseIsRetryable(t *testing.T) {
	now := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	store := newManagedCredentialStore(PendingCredential{
		Target: "workbuddy-cn", AccessToken: "access-unparseable",
		RefreshToken:    "refresh-unparseable",
		AccessExpiresAt: now.Add(10 * time.Second).UnixMilli(),
		Domain:          "copilot.tencent.com", UID: "uid-unparseable",
	})
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/plugin/auth/token/refresh" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, "temporarily unavailable")
	}))
	defer target.Close()

	session := NewManagedSession(ManagedSessionConfig{
		BaseURL: target.URL, Store: store, Now: func() time.Time { return now },
	})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		target.URL+"/billing/meter/get-user-resource-summary", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = session.Do(context.Background(), req)
	if !errors.Is(err, ErrManagedRetryable) {
		t.Fatalf("暂时无法解析刷新响应应可重试，实际 %v", err)
	}
	if store.reauth != 0 || store.healthy != 0 {
		t.Errorf("解析失败不得改变来源健康，healthy=%d reauth=%d",
			store.healthy, store.reauth)
	}
}

func TestManagedSessionExpiredRefreshTokenMarksReauth(t *testing.T) {
	const (
		oldAccess  = "access-expired-refresh"
		oldRefresh = "refresh-expired"
		uid        = "uid-expired-refresh"
		domain     = "copilot.tencent.com"
	)
	now := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	store := newManagedCredentialStore(PendingCredential{
		Target: "workbuddy-cn", AccessToken: oldAccess, RefreshToken: oldRefresh,
		AccessExpiresAt:  now.Add(10 * time.Second).UnixMilli(),
		RefreshExpiresAt: now.Add(-time.Minute).UnixMilli(),
		Domain:           domain, UID: uid,
	})

	var refreshCalls, requestCalls int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/plugin/auth/token/refresh":
			refreshCalls++
			writeEnvelope(t, w, map[string]any{"accessToken": "unexpected"})
		case "/billing/meter/get-user-resource-summary":
			requestCalls++
			writeEnvelope(t, w, map[string]any{"IsPaidUser": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer target.Close()

	session := NewManagedSession(ManagedSessionConfig{
		BaseURL: target.URL, Store: store, Now: func() time.Time { return now },
	})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		target.URL+"/billing/meter/get-user-resource-summary", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = session.Do(context.Background(), req)
	if !errors.Is(err, ErrManagedReauthRequired) {
		t.Fatalf("refresh token 失效应要求重新登录，实际 %v", err)
	}
	if refreshCalls != 0 || requestCalls != 0 {
		t.Errorf("refresh token 失效时不得发网络请求，refresh=%d request=%d", refreshCalls, requestCalls)
	}
	if store.reauth != 1 {
		t.Errorf("refresh token 失效应标记需重登，实际 %d", store.reauth)
	}
}
