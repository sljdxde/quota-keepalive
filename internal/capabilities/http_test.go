package capabilities

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func httpGrant() Grant {
	return GrantFromNeeds([]string{"http.outbound"})
}

func TestFetchAllowedHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	fetch := NewHTTPFetch(AllowedHostsFromManifest([]string{"127.0.0.1"}), httpGrant())
	resp, err := fetch.Fetch(context.Background(), FetchRequest{Method: "GET", URL: srv.URL})
	if err != nil {
		t.Fatalf("白名单主机请求失败: %v", err)
	}
	if resp.Status != http.StatusOK || string(resp.Body) != "ok" {
		t.Errorf("响应异常: status=%d body=%q", resp.Status, resp.Body)
	}
}

func TestFetchRejectsNonWhitelisted(t *testing.T) {
	fetch := NewHTTPFetch(AllowedHostsFromManifest([]string{"api.example.com"}), httpGrant())
	_, err := fetch.Fetch(context.Background(), FetchRequest{URL: "https://evil.example.com/x"})
	if !errors.Is(err, ErrHostNotAllowed) {
		t.Fatalf("非白名单主机应被拒绝，实际 %v", err)
	}
}

func TestFetchSetsUserAgent(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
	}))
	defer srv.Close()

	fetch := NewHTTPFetch(AllowedHostsFromManifest([]string{"127.0.0.1"}), httpGrant())
	if _, err := fetch.Fetch(context.Background(), FetchRequest{URL: srv.URL}); err != nil {
		t.Fatal(err)
	}
	if gotUA != UserAgent {
		t.Errorf("应统一 UA=%s，实际 %q", UserAgent, gotUA)
	}
}

func TestFetchTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(80 * time.Millisecond)
	}))
	defer srv.Close()

	fetch := NewHTTPFetch(AllowedHostsFromManifest([]string{"127.0.0.1"}), httpGrant()).
		WithTimeout(10 * time.Millisecond)
	_, err := fetch.Fetch(context.Background(), FetchRequest{URL: srv.URL})
	if err == nil {
		t.Fatal("慢响应应触发超时错误")
	}
}

func TestFetchRejectedWithoutGrant(t *testing.T) {
	fetch := NewHTTPFetch(AllowedHostsFromManifest([]string{"127.0.0.1"}), GrantFromNeeds(nil))
	_, err := fetch.Fetch(context.Background(), FetchRequest{URL: "https://127.0.0.1/x"})
	if !errors.Is(err, ErrHookNotGranted) {
		t.Fatalf("未授权 http.outbound 应被拒绝，实际 %v", err)
	}
}

func TestRedactURLSensitiveParams(t *testing.T) {
	out := RedactURL("https://api.example.com/x?token=supersecret&name=ok&access_key=abc")
	if strings.Contains(out, "supersecret") || strings.Contains(out, "abc") {
		t.Errorf("敏感参数值应被脱敏，实际 %q", out)
	}
	if !strings.Contains(out, "name=ok") {
		t.Errorf("非敏感参数应保留，实际 %q", out)
	}
}
