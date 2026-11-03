package feedback

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDefaultEndpointMatchesWorkerConfig 断言客户端内置地址与 Worker 部署配置
// 完全一致。
//
// 这个测试存在的理由：曾经 DefaultEndpoint 指向
// qka-feedback.sljdxde.workers.dev，而账号真实的 workers.dev 子域是
// neighborhood-share-sljdxde，Worker 也从没部署过。所有单测都绿，用户点提交
// 却必然失败。把「部署配置」当成唯一真源后，两边任何一边改错都会在这里红。
func TestDefaultEndpointMatchesWorkerConfig(t *testing.T) {
	path := filepath.Join("..", "..", "feedback-worker", "wrangler.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 Worker 配置失败: %v", err)
	}
	publicURL := ""
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "PUBLIC_URL") {
			continue
		}
		_, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		publicURL = strings.Trim(strings.TrimSpace(value), `"`)
	}
	if publicURL == "" {
		t.Fatal("feedback-worker/wrangler.toml 缺少 PUBLIC_URL：客户端内置地址必须与部署配置同源")
	}
	if publicURL != DefaultEndpoint {
		t.Fatalf("客户端内置地址与 Worker 配置不一致:\n  客户端 DefaultEndpoint = %q\n  Worker  PUBLIC_URL   = %q",
			DefaultEndpoint, publicURL)
	}
	// 额外锁住角色：地址必须是 https 且以 /api/feedback 结尾。
	if !strings.HasPrefix(DefaultEndpoint, "https://") ||
		!strings.HasSuffix(DefaultEndpoint, "/api/feedback") {
		t.Fatalf("内置地址形状不符合预期: %q", DefaultEndpoint)
	}
}
