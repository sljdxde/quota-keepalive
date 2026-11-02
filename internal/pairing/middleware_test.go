package pairing

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func setupGuard(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	p := New()
	token, _ := p.Issue("host-1")
	g := NewGuard(p, "")

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewUnstartedServer(g.Wrap(inner))
	// 启动前用真实监听地址设置预期 Host。
	g.expectedHost = srv.Listener.Addr().String()
	srv.Start()
	t.Cleanup(srv.Close)
	return srv, token
}

func doRequest(t *testing.T, srv *httptest.Server, token string, modify func(*http.Request)) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-QKA-Host", "host-1")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if modify != nil {
		modify(req)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	return resp
}

func TestGuardAllowsCorrectPairedClient(t *testing.T) {
	srv, token := setupGuard(t)
	if resp := doRequest(t, srv, token, nil); resp.StatusCode != http.StatusOK {
		t.Errorf("正确配对应放行，实际 %d", resp.StatusCode)
	}
}

func TestGuardRejectsMissingCredential(t *testing.T) {
	srv, _ := setupGuard(t)
	if resp := doRequest(t, srv, "", nil); resp.StatusCode != http.StatusForbidden {
		t.Errorf("无凭据应拒绝，实际 %d", resp.StatusCode)
	}
}

func TestGuardRejectsBadOrigin(t *testing.T) {
	srv, token := setupGuard(t)
	resp := doRequest(t, srv, token, func(r *http.Request) {
		r.Header.Set("Origin", "https://evil.example.com")
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("非 loopback Origin 应拒绝，实际 %d", resp.StatusCode)
	}
}

func TestGuardAllowsLoopbackOrigin(t *testing.T) {
	srv, token := setupGuard(t)
	resp := doRequest(t, srv, token, func(r *http.Request) {
		r.Header.Set("Origin", "http://127.0.0.1:1234")
	})
	if resp.StatusCode != http.StatusOK {
		t.Errorf("loopback Origin 应放行，实际 %d", resp.StatusCode)
	}
}

func TestGuardChecksRemoteAddr(t *testing.T) {
	p := New()
	_, _ = p.Issue("host-1")
	g := NewGuard(p, "127.0.0.1:43189")

	r := &http.Request{
		RemoteAddr: "192.168.1.5:1234",
		Host:       "127.0.0.1:43189",
		Header:     http.Header{},
	}
	if err := g.check(r); !errors.Is(err, ErrNotLoopback) {
		t.Fatalf("LAN 来源应拒绝，实际 %v", err)
	}
}

func TestGuardChecksHost(t *testing.T) {
	p := New()
	token, _ := p.Issue("host-1")
	g := NewGuard(p, "127.0.0.1:43189")

	r := &http.Request{
		RemoteAddr: "127.0.0.1:5000",
		Host:       "evil.example.com:43189",
		Header:     http.Header{},
	}
	r.Header.Set("X-QKA-Host", "host-1")
	r.Header.Set("Authorization", "Bearer "+token)
	if err := g.check(r); !errors.Is(err, ErrBadHost) {
		t.Fatalf("错误 Host 应拒绝，实际 %v", err)
	}
}

func TestHostAndOriginParsingFailurePaths(t *testing.T) {
	if hostMatches("127.0.0.1:bad", "127.0.0.1:8080") {
		t.Fatal("非法端口不应匹配")
	}
	if hostMatches("example.com", "example.com:8080") {
		t.Fatal("端口不同不应匹配")
	}
	if originLoopback("http://%zz") {
		t.Fatal("非法 Origin 不应视为本机")
	}
}
