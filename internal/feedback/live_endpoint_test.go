package feedback

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// feedbackLiveEnv 打开真实端点连通性检查。
// 默认跳过：单元测试不依赖公网，但发布前必须用 QKA_FEEDBACK_LIVE=1 跑一次。
const feedbackLiveEnv = "QKA_FEEDBACK_LIVE"

// endpointUnderTest 允许检查自定义地址（例如刚部署完的新域名），
// 未设置时检查客户端内置的 DefaultEndpoint。
func endpointUnderTest() string {
	if custom := strings.TrimSpace(os.Getenv("QKA_FEEDBACK_URL")); custom != "" {
		return custom
	}
	return DefaultEndpoint
}

// TestLiveFeedbackEndpointIsDeployed 是唯一能发现「内置地址根本不存在」的测试。
//
// 背景：service_test/coverage_test 全部用 httptest 假服务，只能证明客户端逻辑正确，
// 无法证明线上地址真的存在。曾经 DefaultEndpoint 指向一个从未部署的
// *.workers.dev 子域，所有单元测试都是绿的，用户点提交却必然失败。
// 因此这里做两件事，且必须是真实网络：
//
//  1. DNS 解析该主机名；NXDOMAIN 直接失败（服务没部署）。
//  2. 建立 TLS 连接并发一个请求；任何 HTTP 状态码都算「可服务」，
//     只有网络层失败才算不通过。
//
// 运行方式：
//
//	QKA_FEEDBACK_LIVE=1 go test ./internal/feedback -run TestLiveFeedbackEndpointIsDeployed -v
func TestLiveFeedbackEndpointIsDeployed(t *testing.T) {
	if strings.TrimSpace(os.Getenv(feedbackLiveEnv)) == "" {
		t.Skipf("设置 %s=1 后才会检查真实反馈端点（需要公网）", feedbackLiveEnv)
	}
	endpoint := endpointUnderTest()
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" {
		t.Fatalf("内置反馈地址不是合法 URL: %q (%v)", endpoint, err)
	}

	// 1. DNS：区分「没部署」与「网络受限」，这是本次缺口的核心断言。
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupHost(ctx, parsed.Hostname())
	if err != nil {
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
			t.Fatalf("反馈端点 %s 不存在（NXDOMAIN）：服务尚未部署，客户端内置地址必须指向真实服务",
				parsed.Hostname())
		}
		t.Fatalf("解析反馈端点 %s 失败: %v", parsed.Hostname(), err)
	}
	if len(addrs) == 0 {
		t.Fatalf("反馈端点 %s 没有解析到任何地址", parsed.Hostname())
	}
	t.Logf("DNS 解析 %s -> %v", parsed.Hostname(), addrs)

	// 2. 真实请求：只要拿到 HTTP 响应就说明 Worker 在服务（GET 返回 404/405 都正常）。
	client := &http.Client{Timeout: 20 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	req.Header.Set("User-Agent", "Quota-Keepalive/endpoint-probe")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("反馈端点 %s 不可达: %v", endpoint, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	t.Logf("反馈端点响应: HTTP %d, body=%q", resp.StatusCode, strings.TrimSpace(string(body)))

	// 「拿到 HTTP 响应」不等于「Worker 存在」：域名被劫持/污染的中间设备同样会
	// 返回 404，而 Cloudflare 对未部署的 Worker 会回 `error code: 1042`。
	// 这两种情况都必须判失败，否则测试会给假绿灯。
	if bodyIsWorkerMissing(string(body)) {
		t.Fatalf("反馈端点 %s 上没有可用的 Worker（Cloudflare 返回 %q）：服务尚未部署",
			endpoint, strings.TrimSpace(string(body)))
	}
	if resp.Header.Get("Server") == "" {
		t.Fatalf("反馈端点 %s 的响应没有 Server 头，可能来自透明代理或劫持", endpoint)
	}
	if resp.StatusCode >= 500 {
		t.Fatalf("反馈端点 %s 返回 %d，服务端异常", endpoint, resp.StatusCode)
	}
}
