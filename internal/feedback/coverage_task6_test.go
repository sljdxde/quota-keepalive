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
	// 解析器故障（非 NXDOMAIN）才落到「检查网络」这一类。
	dnsErr := &net.DNSError{Err: "server misbehaving", Name: "qka-feedback.invalid"}
	if msg := describeNetworkError(dnsErr); !strings.Contains(msg, "解析失败") {
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

// TestDescribeNetworkErrorNotFound 验证「域名不存在」与「解析失败」被区分开：
// 前者是服务没部署，让用户换网络永远修不好，文案必须说准。
func TestDescribeNetworkErrorNotFound(t *testing.T) {
	notFound := &net.DNSError{
		Err: "no such host", Name: "qka-feedback.invalid", IsNotFound: true,
	}
	msg := describeNetworkError(notFound)
	if !strings.Contains(msg, "不存在") || !strings.Contains(msg, "尚未部署") {
		t.Fatalf("NXDOMAIN 文案应说明服务未部署，实际 %q", msg)
	}
	if strings.Contains(msg, "检查网络") {
		t.Fatalf("NXDOMAIN 不应让用户去查网络: %q", msg)
	}

	lookupFail := &net.DNSError{Err: "server misbehaving", Name: "example.test"}
	if msg := describeNetworkError(lookupFail); !strings.Contains(msg, "解析失败") {
		t.Fatalf("普通解析失败文案不对: %q", msg)
	}
}

// TestLiveProbeDetectsUndeployedWorker 用 httptest 固定「假绿灯」的判定逻辑：
// 未部署的 Worker 回 error code: 1042、被劫持的中间设备没有 Server 头，
// 两者都必须判失败，不能因为拿到 HTTP 响应就通过。
func TestLiveProbeBodyIsWorkerMissing(t *testing.T) {
	if !bodyIsWorkerMissing("error code: 1042") {
		t.Fatal("1042 必须判定为 Worker 不存在")
	}
	if bodyIsWorkerMissing(`{"error":"站点密钥错误"}`) {
		t.Fatal("Worker 自身返回的业务错误不应判定为不存在")
	}
}
