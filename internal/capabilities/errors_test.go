package capabilities

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"qka/internal/store"
)

type scriptedSecretStore struct {
	setErr, getErr, deleteErr error
	value                     string
}

func (s scriptedSecretStore) Set(context.Context, string, string, string) error { return s.setErr }
func (s scriptedSecretStore) Get(context.Context, string, string) (string, error) {
	return s.value, s.getErr
}
func (s scriptedSecretStore) Delete(context.Context, string, string) error { return s.deleteErr }

func TestCredentialServiceBranches(t *testing.T) {
	ctx := context.Background()
	none := GrantFromNeeds(nil)
	all := GrantFromNeeds([]string{"credentials.resolve", "credentials.managed"})
	imported := CredentialRef{Kind: Imported}

	svc := NewCredentialService(scriptedSecretStore{}, scriptedSecretStore{}, none, nil)
	if _, err := svc.Resolve(ctx, managedRef()); !errors.Is(err, ErrHookNotGranted) {
		t.Fatalf("Resolve 未授权应拒绝，实际 %v", err)
	}
	if _, err := svc.ResolveImported(ctx, imported, "x"); !errors.Is(err, ErrHookNotGranted) {
		t.Fatalf("ResolveImported 未授权应拒绝，实际 %v", err)
	}
	if err := svc.Persist(ctx, managedRef(), "x"); !errors.Is(err, ErrHookNotGranted) {
		t.Fatalf("Persist 未授权应拒绝，实际 %v", err)
	}
	if err := svc.Delete(ctx, managedRef()); !errors.Is(err, ErrHookNotGranted) {
		t.Fatalf("Delete 未授权应拒绝，实际 %v", err)
	}
	if err := svc.Refresh(ctx, managedRef()); !errors.Is(err, ErrHookNotGranted) {
		t.Fatalf("Refresh 未授权应拒绝，实际 %v", err)
	}

	svc = NewCredentialService(nil, nil, all, nil)
	if _, err := svc.Resolve(ctx, imported); !errors.Is(err, ErrImportedReadOnly) {
		t.Fatalf("Resolve 导入来源应拒绝，实际 %v", err)
	}
	if _, err := svc.ResolveImported(ctx, managedRef(), "x"); err == nil {
		t.Fatal("ResolveImported 非导入来源应拒绝")
	}
	if err := svc.Persist(ctx, imported, "x"); !errors.Is(err, ErrImportedReadOnly) {
		t.Fatalf("Persist 导入来源应拒绝，实际 %v", err)
	}
	if err := svc.Persist(ctx, managedRef(), "x"); err == nil {
		t.Fatal("无可用凭据后端应失败")
	}
	if err := svc.Delete(ctx, imported); !errors.Is(err, ErrImportedReadOnly) {
		t.Fatalf("Delete 导入来源应拒绝，实际 %v", err)
	}
	if err := svc.Delete(ctx, managedRef()); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("两个后端均无凭据应返回未找到，实际 %v", err)
	}

	backendErr := errors.New("backend")
	svc = NewCredentialService(
		scriptedSecretStore{getErr: backendErr},
		scriptedSecretStore{getErr: backendErr}, all, nil)
	if _, err := svc.Resolve(ctx, managedRef()); !errors.Is(err, backendErr) {
		t.Fatalf("文件回退失败应返回，实际 %v", err)
	}
	if maskSecret("abc") != "****" || maskSecret("abcdef") != "****cdef" {
		t.Fatal("短/长凭据脱敏错误")
	}
	svc = NewCredentialService(
		scriptedSecretStore{setErr: backendErr},
		scriptedSecretStore{setErr: backendErr}, all, nil)
	if err := svc.Persist(ctx, managedRef(), "secret"); !errors.Is(err, backendErr) {
		t.Fatalf("文件保存失败应返回，实际 %v", err)
	}
	svc = NewCredentialService(
		scriptedSecretStore{deleteErr: backendErr},
		scriptedSecretStore{deleteErr: backendErr}, all, nil)
	if err := svc.Delete(ctx, managedRef()); !errors.Is(err, backendErr) {
		t.Fatalf("双后端删除失败应报告，实际 %v", err)
	}
	svc = NewCredentialService(scriptedSecretStore{}, scriptedSecretStore{}, all, nil)
	if err := svc.Delete(ctx, managedRef()); err != nil {
		t.Fatalf("任一后端删除成功应成功，实际 %v", err)
	}
}

func TestFileSecretStoreErrorBranches(t *testing.T) {
	parent := t.TempDir()
	blocker := filepath.Join(parent, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewFileSecretStore(blocker)
	if err := store.Set(context.Background(), "qka", "acct", "x"); err == nil {
		t.Fatal("父路径为文件时 Set 应失败")
	}

	root := t.TempDir()
	store = NewFileSecretStore(root)
	path := store.path("qka", "acct")
	if err := os.MkdirAll(filepath.Join(path, "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(context.Background(), "qka", "acct"); err == nil {
		t.Fatal("读取目录文件应返回底层错误")
	}
	if err := store.Delete(context.Background(), "qka", "acct"); err == nil {
		t.Fatal("删除非空目录应返回底层错误")
	}
}

type httpRoundTrip func(*http.Request) (*http.Response, error)

func (f httpRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type failingBody struct{}

func (failingBody) Read([]byte) (int, error) { return 0, errors.New("body") }
func (failingBody) Close() error             { return nil }

func TestHTTPFetchErrorBranches(t *testing.T) {
	allowed := AllowedHostsFromManifest([]string{"127.0.0.1"})
	if err := allowed.check("://bad"); err == nil {
		t.Fatal("非法 URL 应失败")
	}
	if err := allowed.check("http://127.0.0.1:9999/x"); err != nil {
		t.Fatalf("仅主机白名单应允许任意端口，实际 %v", err)
	}

	fetch := NewHTTPFetch(allowed, httpGrant())
	if _, err := fetch.Fetch(context.Background(), FetchRequest{
		Method: "GET\n", URL: "http://127.0.0.1/x",
	}); err == nil {
		t.Fatal("非法 HTTP 方法应失败")
	}
	fetch.client.Transport = httpRoundTrip(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("transport")
	})
	if _, err := fetch.Fetch(context.Background(), FetchRequest{URL: "http://127.0.0.1/x"}); err == nil {
		t.Fatal("HTTP 传输失败应返回")
	}
	fetch.client.Transport = httpRoundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200, Header: http.Header{"X-Test": {"v"}},
			Body: io.NopCloser(strings.NewReader("ok")),
		}, nil
	})
	if _, err := fetch.Fetch(context.Background(), FetchRequest{
		URL: "http://127.0.0.1/x", Method: http.MethodPost, Body: []byte("body"),
		Headers: map[string]string{"X-Input": "v"},
	}); err != nil {
		t.Fatalf("带请求体/请求头应成功，实际 %v", err)
	}

	fetch.client.Transport = httpRoundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"X-Test": {"v"}}, Body: failingBody{}}, nil
	})
	if _, err := fetch.Fetch(context.Background(), FetchRequest{URL: "http://127.0.0.1/x"}); err == nil {
		t.Fatal("响应体读取失败应返回")
	}
	if got := RedactURL("http://%zz"); got != "REDACTED" {
		t.Fatalf("非法 URL 应完全脱敏，实际 %q", got)
	}
}

func TestKeychainStoreErrorBranches(t *testing.T) {
	ctx := context.Background()
	commandErr := errors.New("command")
	for _, run := range []commandRunner{
		func(context.Context, string, ...string) (execResult, error) { return execResult{}, commandErr },
		runnerReturning(execResult{stderr: []byte("backend failed"), exitCode: 1}),
	} {
		k := NewKeychainStore().withRunner(run)
		if err := k.Set(ctx, "qka", "acct", "x"); err == nil {
			t.Fatal("Set 失败应返回")
		}
		if _, err := k.Get(ctx, "qka", "acct"); err == nil {
			t.Fatal("Get 失败应返回")
		}
		if err := k.Delete(ctx, "qka", "acct"); err == nil {
			t.Fatal("Delete 失败应返回")
		}
	}
	if !isAuthDenied(execResult{exitCode: 1, stderr: []byte("USER CANCELED")}) ||
		!isAuthDenied(execResult{exitCode: 1, stdout: []byte("user interaction is not allowed")}) ||
		isAuthDenied(execResult{exitCode: 1}) {
		t.Fatal("授权拒绝文本识别错误")
	}
	if _, err := realRunner(ctx, "false"); err != nil {
		t.Fatalf("非零退出码应包装为 execResult，实际 %v", err)
	}
	if _, err := realRunner(ctx, "true"); err != nil {
		t.Fatalf("成功命令应返回 nil 错误，实际 %v", err)
	}
	if _, err := realRunner(ctx, "/definitely/not/a/command"); err == nil {
		t.Fatal("无法启动命令应返回错误")
	}
	k := NewKeychainStore().withRunner(runnerReturning(execResult{exitCode: 0}))
	if err := k.Delete(ctx, "qka", "acct"); err != nil {
		t.Fatalf("Delete 成功应返回 nil，实际 %v", err)
	}
	k = NewKeychainStore().withRunner(runnerReturning(execResult{exitCode: 128}))
	if err := k.Delete(ctx, "qka", "acct"); !errors.Is(err, ErrAuthDenied) {
		t.Fatalf("Delete 授权拒绝应映射，实际 %v", err)
	}
}

func TestLogAndLoginStoreBranches(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc := NewLogService(st.DB(), GrantFromNeeds([]string{"log.write"}), nil)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	ns := Namespace{Target: "wb", AccountID: "u1", Operation: "checkin"}
	if err := svc.Write(context.Background(), ns, "info", "x"); err == nil {
		t.Fatal("关闭 DB 应导致写入失败")
	}

	login := NewLoginCredentialStore(nil, nil)
	if err := login.Save(context.Background(), "bad/id", nil); !errors.Is(err, ErrUnsafeNamespace) {
		t.Fatalf("非法 session 应拒绝保存，实际 %v", err)
	}
	if _, err := login.Load(context.Background(), "bad/id"); !errors.Is(err, ErrUnsafeNamespace) {
		t.Fatalf("非法 session 应拒绝读取，实际 %v", err)
	}
	if err := login.Delete(context.Background(), "bad/id"); !errors.Is(err, ErrUnsafeNamespace) {
		t.Fatalf("非法 session 应拒绝删除，实际 %v", err)
	}
	if err := login.Save(context.Background(), "ok", nil); err == nil {
		t.Fatal("无后端保存应失败")
	}
	if _, err := login.Load(context.Background(), "ok"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("无后端读取应未找到，实际 %v", err)
	}
	if err := login.Delete(context.Background(), "ok"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("无后端删除应未找到，实际 %v", err)
	}

	backendErr := errors.New("backend")
	login = NewLoginCredentialStore(
		scriptedSecretStore{getErr: backendErr, value: "x"},
		scriptedSecretStore{getErr: backendErr})
	if _, err := login.Load(context.Background(), "ok"); !errors.Is(err, backendErr) {
		t.Fatalf("文件读取失败应返回，实际 %v", err)
	}
	login = NewLoginCredentialStore(
		scriptedSecretStore{value: "keychain"},
		scriptedSecretStore{})
	got, err := login.Load(context.Background(), "ok")
	if err != nil || string(got) != "keychain" {
		t.Fatalf("Keychain 成功应直接返回，实际 %q %v", got, err)
	}
	if err := login.Delete(context.Background(), "ok"); err != nil {
		t.Fatalf("Keychain 删除成功应成功，实际 %v", err)
	}
}

func TestStateServiceErrorBranches(t *testing.T) {
	ctx := context.Background()
	svc := NewStateService(t.TempDir(), stateGrant())
	for _, key := range []string{"", ".", ".."} {
		if safeComponent(key) {
			t.Fatalf("%q 不应为安全组件", key)
		}
	}
	ns := Namespace{Target: "wb", AccountID: "u1", Operation: "checkin"}
	noGet := NewStateService(t.TempDir(), GrantFromNeeds([]string{"state.set"}))
	if _, err := noGet.Get(ctx, ns, "k"); !errors.Is(err, ErrHookNotGranted) {
		t.Fatalf("未授权 state.get 应拒绝，实际 %v", err)
	}
	if _, err := svc.Get(ctx, Namespace{Target: "../x"}, "k"); !errors.Is(err, ErrUnsafeNamespace) {
		t.Fatalf("非法 Get namespace 应拒绝，实际 %v", err)
	}
	if _, err := svc.Get(ctx, ns, "../x"); !errors.Is(err, ErrUnsafeNamespace) {
		t.Fatalf("非法 Get key 应拒绝，实际 %v", err)
	}

	parent := t.TempDir()
	blocker := filepath.Join(parent, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	bad := NewStateService(blocker, stateGrant())
	if err := bad.Set(ctx, ns, "k", []byte("v")); err == nil {
		t.Fatal("状态目录创建失败应返回")
	}
}
