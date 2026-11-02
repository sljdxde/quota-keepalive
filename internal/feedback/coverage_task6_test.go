package feedback

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestEffectiveEndpointPrefersProvider 覆盖设置页给出的自定义地址优先于
// Endpoint/DefaultEndpoint，并且改完立即生效（每次 Submit 都重新问一次）。
func TestEffectiveEndpointPrefersProvider(t *testing.T) {
	var called bool
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/api/feedback" {
			t.Errorf("path = %q", req.URL.Path)
		}
		called = true
		rw.Header().Set("Content-Type", "application/json")
		_, _ = rw.Write([]byte(`{"ok":true,"number":7,"url":"https://github.com/o/r/issues/7"}`))
	}))
	defer server.Close()

	svc := &Service{
		Endpoint: "https://builtin.invalid/api/feedback",
		SiteKey:  DefaultSiteKey,
		Version:  "test",
		Platform: "test",
		Client:   server.Client(),
		Now:      time.Now,
		EndpointProvider: func() string {
			return server.URL + "/api/feedback"
		},
	}
	result, err := svc.Submit(context.Background(), Request{Description: "自定义地址提交测试"})
	if err != nil {
		t.Fatalf("自定义地址提交失败: %v", err)
	}
	if !called {
		t.Fatal("应当请求自定义地址")
	}
	if !result.OK || result.Number != 7 {
		t.Fatalf("结果异常: %+v", result)
	}
}

// TestEffectiveEndpointFallsBack 覆盖 Provider 返回空时继续用 Endpoint，
// 再回落到内置地址。
func TestEffectiveEndpointFallsBack(t *testing.T) {
	svc := &Service{Endpoint: "https://example.test/api/feedback"}
	if got := svc.effectiveEndpoint(); got != "https://example.test/api/feedback" {
		t.Fatalf("empty provider 应回落到 Endpoint，实际 %q", got)
	}
	svc.EndpointProvider = func() string { return "   " }
	if got := svc.effectiveEndpoint(); got != "https://example.test/api/feedback" {
		t.Fatalf("空白 provider 应回落到 Endpoint，实际 %q", got)
	}
	svc.Endpoint = ""
	if got := svc.effectiveEndpoint(); got != DefaultEndpoint {
		t.Fatalf("都为空应回落到内置地址，实际 %q", got)
	}
}

// TestDescribeNetworkError 用真实的 net 错误值断言文案，不构造假错误类型。
func TestDescribeNetworkError(t *testing.T) {
	dnsErr := &net.DNSError{Err: "no such host", Name: "qka-feedback.invalid", IsNotFound: true}
	if msg := describeNetworkError(dnsErr); !strings.Contains(msg, "域名解析失败") {
		t.Fatalf("DNS 错误文案不对: %q", msg)
	}

	timeoutErr := timeoutErr{}
	if msg := describeNetworkError(timeoutErr); !strings.Contains(msg, "超时") ||
		!strings.Contains(msg, "自定义反馈地址") {
		t.Fatalf("超时文案不对: %q", msg)
	}

	plain := errors.New("connection reset by peer")
	if msg := describeNetworkError(plain); !strings.HasPrefix(msg, "网络异常") {
		t.Fatalf("普通错误文案不对: %q", msg)
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }
