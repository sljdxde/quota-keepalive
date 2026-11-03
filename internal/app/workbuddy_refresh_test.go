package app

import (
	"bytes"
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
	"qka/internal/capabilities"
	"qka/internal/store"
	"qka/internal/workbuddy"
)

func workBuddyCredential(t *testing.T, access, refresh, uid, domain string, expiresAt time.Time) []byte {
	t.Helper()
	var buf bytes.Buffer
	err := json.NewEncoder(&buf).Encode(workbuddy.PendingCredential{
		Target: "workbuddy-cn", AccessToken: access, RefreshToken: refresh,
		AccessExpiresAt: expiresAt.UnixMilli(), Domain: domain, UID: uid,
	})
	if err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeWorkBuddyEnvelope(t *testing.T, w http.ResponseWriter, data any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"code": 0,
		"data": data,
	}); err != nil {
		t.Error(err)
	}
}

func TestWorkBuddyManagedRefreshFailureIsolatesAccountAndReloginRecovers(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	now := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	acct := accounts.NewService(st.DB())
	if err := acct.Discover(ctx, "workbuddy-cn", []accounts.DiscoveredAccount{
		{
			ID: "wb_one", DisplayName: "****1111", MaskedIdentity: "****1111",
			SourceID: "wbm_one", SourceKind: accounts.KindManaged,
		},
		{
			ID: "wb_two", DisplayName: "****2222", MaskedIdentity: "****2222",
			SourceID: "wbm_two", SourceKind: accounts.KindManaged,
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := acct.SetEnabled(ctx, "workbuddy-cn", "wb_one", true); err != nil {
		t.Fatal(err)
	}

	file := newAppSecretStore()
	creds := capabilities.NewCredentialService(
		failingAppSecretStore{},
		file,
		capabilities.Grant{
			"credentials.managed": true,
			"credentials.resolve": true,
		},
		func() time.Time { return now },
	)
	firstRef := capabilities.CredentialRef{
		Target: "workbuddy-cn", AccountID: "wb_one", SourceID: "wbm_one", Kind: capabilities.Managed,
	}
	secondRef := capabilities.CredentialRef{
		Target: "workbuddy-cn", AccountID: "wb_two", SourceID: "wbm_two", Kind: capabilities.Managed,
	}
	if err := creds.Persist(ctx, firstRef, string(workBuddyCredential(
		t, "{{SECRET_old1}}", "{{SECRET_ref1}}", "uid-one", "copilot.tencent.com",
		now.Add(10*time.Second),
	))); err != nil {
		t.Fatal(err)
	}
	if err := creds.Persist(ctx, secondRef, string(workBuddyCredential(
		t, "{{SECRET_old2}}", "{{SECRET_ref2}}", "uid-two", "copilot.tencent.com",
		now.Add(time.Hour),
	))); err != nil {
		t.Fatal(err)
	}

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/plugin/auth/token/refresh" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer target.Close()

	a := &App{
		cfg:         Config{BaseURLs: map[string]string{"workbuddy-cn": target.URL}},
		now:         func() time.Time { return now },
		accounts:    acct,
		credentials: creds,
	}
	session, err := a.newWorkBuddyManagedSession(ctx, "wb_one", "wbm_one")
	if err != nil {
		t.Fatalf("创建受管会话失败: %v", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		target.URL+"/billing/meter/get-user-resource-summary", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Do(ctx, req); !errors.Is(err, workbuddy.ErrManagedReauthRequired) {
		t.Fatalf("刷新失败应要求重新登录，实际 %v", err)
	}

	first, err := acct.Get(ctx, "workbuddy-cn", "wb_one")
	if err != nil {
		t.Fatalf("刷新失败不得删除账号: %v", err)
	}
	if !first.Enabled || len(first.Sources) != 1 ||
		first.Sources[0].Health != accounts.HealthReauthRequired {
		t.Errorf("当前账号应保留并标记需重登: %+v", first)
	}
	second, err := acct.Get(ctx, "workbuddy-cn", "wb_two")
	if err != nil {
		t.Fatal(err)
	}
	if second.Sources[0].Health != accounts.HealthHealthy {
		t.Errorf("其他账号来源不得受影响: %+v", second.Sources)
	}

	sink := newWorkBuddyVerificationSink(acct, creds)
	if _, err := sink.AcceptVerifiedAccount(ctx, workbuddy.VerifiedAccount{
		Target: "workbuddy-cn", AccountID: "wb_one", SourceID: "wbm_one",
		DisplayName: "****1111", MaskedIdentity: "****1111",
	}, workBuddyCredential(
		t, "{{SECRET_new1}}", "{{SECRET_refnew1}}", "uid-one", "copilot.tencent.com",
		now.Add(time.Hour),
	)); err != nil {
		t.Fatalf("重新登录恢复失败: %v", err)
	}

	recovered, err := acct.Get(ctx, "workbuddy-cn", "wb_one")
	if err != nil {
		t.Fatal(err)
	}
	if !recovered.Enabled || len(recovered.Sources) != 1 ||
		recovered.Sources[0].Health != accounts.HealthHealthy {
		t.Errorf("重新登录应恢复同一账号和来源: %+v", recovered)
	}
	list, err := acct.List(ctx, "workbuddy-cn")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Errorf("重新登录不得产生重复账号，实际 %d 个", len(list))
	}
	resolved, err := creds.Resolve(ctx, firstRef)
	if err != nil {
		t.Fatal(err)
	}
	var renewed workbuddy.PendingCredential
	if err := json.Unmarshal([]byte(resolved.Secret), &renewed); err != nil {
		t.Fatal(err)
	}
	if renewed.AccessToken != "{{SECRET_new1}}" || renewed.RefreshToken != "{{SECRET_refnew1}}" {
		t.Error("重新登录应替换同一来源的受管凭据")
	}
}

func TestWorkBuddyManagedRetryableRefreshKeepsAccountsAndHealth(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	now := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	acct := accounts.NewService(st.DB())
	if err := acct.Discover(ctx, "workbuddy-cn", []accounts.DiscoveredAccount{
		{
			ID: "wb_retry", DisplayName: "****5555", MaskedIdentity: "****5555",
			SourceID: "wbm_retry", SourceKind: accounts.KindManaged,
		},
		{
			ID: "wb_other", DisplayName: "****6666", MaskedIdentity: "****6666",
			SourceID: "wbm_other", SourceKind: accounts.KindManaged,
		},
	}); err != nil {
		t.Fatal(err)
	}
	for _, accountID := range []string{"wb_retry", "wb_other"} {
		if err := acct.SetEnabled(ctx, "workbuddy-cn", accountID, true); err != nil {
			t.Fatal(err)
		}
	}

	file := newAppSecretStore()
	creds := capabilities.NewCredentialService(
		failingAppSecretStore{},
		file,
		capabilities.Grant{
			"credentials.managed": true,
			"credentials.resolve": true,
		},
		func() time.Time { return now },
	)
	ref := capabilities.CredentialRef{
		Target: "workbuddy-cn", AccountID: "wb_retry",
		SourceID: "wbm_retry", Kind: capabilities.Managed,
	}
	if err := creds.Persist(ctx, ref, string(workBuddyCredential(
		t, "retry-access", "retry-refresh", "uid-retry", "copilot.tencent.com",
		now.Add(10*time.Second),
	))); err != nil {
		t.Fatal(err)
	}

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/plugin/auth/token/refresh" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		http.NotFound(w, r)
	}))
	defer target.Close()

	a := &App{
		cfg:         Config{BaseURLs: map[string]string{"workbuddy-cn": target.URL}},
		now:         func() time.Time { return now },
		accounts:    acct,
		credentials: creds,
	}
	session, err := a.newWorkBuddyManagedSession(ctx, "wb_retry", "wbm_retry")
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		target.URL+"/billing/meter/get-user-resource-summary", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Do(ctx, req); !errors.Is(err, workbuddy.ErrManagedRetryable) {
		t.Fatalf("500 刷新失败应可重试，实际 %v", err)
	}

	list, err := acct.List(ctx, "workbuddy-cn")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("可重试刷新失败不得删除账号，实际 %d 个", len(list))
	}
	for _, account := range list {
		if !account.Enabled || len(account.Sources) != 1 ||
			account.Sources[0].Health != accounts.HealthHealthy {
			t.Errorf("可重试刷新失败不得改变账号或来源健康: %+v", account)
		}
	}
}

func TestWorkBuddyManagedSessionRejectsImportedSource(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	acct := accounts.NewService(st.DB())
	if err := acct.Discover(ctx, "workbuddy-cn", []accounts.DiscoveredAccount{{
		ID: "wb_imported", DisplayName: "****3333", MaskedIdentity: "****3333",
		SourceID: "wbm_imported", SourceKind: accounts.KindManaged,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := acct.ImportSource(ctx, "workbuddy-cn", "wb_imported", "imported_1"); err != nil {
		t.Fatal(err)
	}

	a := &App{
		cfg:         Config{BaseURLs: map[string]string{"workbuddy-cn": workbuddy.DefaultBaseURL}},
		now:         time.Now,
		accounts:    acct,
		credentials: capabilities.NewCredentialService(nil, nil, capabilities.Grant{}, nil),
	}
	if _, err := a.newWorkBuddyManagedSession(ctx, "wb_imported", "imported_1"); err == nil {
		t.Fatal("导入来源不得创建可刷新会话")
	}
}

func TestWorkBuddyManagedSessionsShareRefreshForSameSource(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	now := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	acct := accounts.NewService(st.DB())
	if err := acct.Discover(ctx, "workbuddy-cn", []accounts.DiscoveredAccount{{
		ID: "wb_shared", DisplayName: "****4444", MaskedIdentity: "****4444",
		SourceID: "wbm_shared", SourceKind: accounts.KindManaged,
	}}); err != nil {
		t.Fatal(err)
	}

	file := newAppSecretStore()
	creds := capabilities.NewCredentialService(
		failingAppSecretStore{},
		file,
		capabilities.Grant{
			"credentials.managed": true,
			"credentials.resolve": true,
		},
		func() time.Time { return now },
	)
	ref := capabilities.CredentialRef{
		Target: "workbuddy-cn", AccountID: "wb_shared",
		SourceID: "wbm_shared", Kind: capabilities.Managed,
	}
	const (
		oldAccess  = "shared-old-access"
		oldRefresh = "shared-old-refresh"
		newAccess  = "shared-new-access"
	)
	if err := creds.Persist(ctx, ref, string(workBuddyCredential(
		t, oldAccess, oldRefresh, "uid-shared", "copilot.tencent.com",
		now.Add(time.Hour),
	))); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var refreshCalls, requestCalls, oldRequests int
	bothOld := make(chan struct{})
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/plugin/auth/token/refresh":
			mu.Lock()
			refreshCalls++
			mu.Unlock()
			if got := r.Header.Get("X-Refresh-Token"); got != oldRefresh {
				t.Errorf("刷新应只消费原 refresh token，实际 %q", got)
			}
			writeWorkBuddyEnvelope(t, w, map[string]any{
				"accessToken": newAccess, "refreshToken": "shared-new-refresh",
				"expiresIn": 3600,
			})
		case "/billing/meter/get-user-resource-summary":
			mu.Lock()
			requestCalls++
			isOld := r.Header.Get("Authorization") == "Bearer "+oldAccess
			if isOld {
				oldRequests++
				if oldRequests == 2 {
					close(bothOld)
				}
			}
			mu.Unlock()
			if isOld {
				<-bothOld
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			writeWorkBuddyEnvelope(t, w, map[string]any{"IsPaidUser": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer target.Close()

	a := &App{
		cfg:         Config{BaseURLs: map[string]string{"workbuddy-cn": target.URL}},
		now:         func() time.Time { return now },
		accounts:    acct,
		credentials: creds,
	}
	first, err := a.newWorkBuddyManagedSession(ctx, "wb_shared", "wbm_shared")
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.newWorkBuddyManagedSession(ctx, "wb_shared", "wbm_shared")
	if err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	sessions := []*workbuddy.ManagedSession{first, second}
	for i := range sessions {
		go func() {
			<-start
			req, err := http.NewRequestWithContext(ctx, http.MethodPost,
				target.URL+"/billing/meter/get-user-resource-summary", strings.NewReader("{}"))
			if err == nil {
				var resp *http.Response
				resp, err = sessions[i].Do(ctx, req)
				if err == nil {
					resp.Body.Close()
					if resp.StatusCode != http.StatusOK {
						err = errors.New("并发请求未成功")
					}
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
		t.Errorf("并发请求必须只消费一次 refresh token，实际刷新 %d 次", refreshCalls)
	}
	if requestCalls != 4 {
		t.Errorf("两个请求各应重试一次，实际业务请求 %d 次", requestCalls)
	}
}
