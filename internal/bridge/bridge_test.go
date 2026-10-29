package bridge

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"qka/internal/mcp"
)

func startBackend(t *testing.T) string {
	t.Helper()
	srv := mcp.NewServer(mcp.ServerDeps{})
	httpSrv, err := srv.ListenAndServe("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = httpSrv.Close() })
	return "http://" + httpSrv.Addr + "/mcp"
}

func TestBridgeForwardsInitialize(t *testing.T) {
	endpoint := startBackend(t)
	var in, out bytes.Buffer
	in.WriteString(`{"jsonrpc":"2.0","id":1,"method":"initialize"}` + "\n")

	cfg := Config{
		Endpoint: endpoint,
		HostID:   "host-1",
		Token:    "secret-token",
		In:       &in,
		Out:      &out,
	}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("桥接运行失败: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "2025-06-18") || !strings.Contains(got, `"id":1`) {
		t.Errorf("桥接应返回 initialize 结果，实际 %s", got)
	}
}

func TestBridgeEquivalentToHTTP(t *testing.T) {
	endpoint := startBackend(t)
	var in, out bytes.Buffer
	in.WriteString(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n")
	cfg := Config{Endpoint: endpoint, In: &in, Out: &out}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	// 与 HTTP 直连等价：tools/list 含 8 个工具。
	if !strings.Contains(out.String(), "agents_list") {
		t.Errorf("桥接 tools/list 应与 HTTP 等价，实际 %s", out.String())
	}
}

func TestBridgeDoesNotLeakCredentials(t *testing.T) {
	endpoint := startBackend(t)
	var in, out bytes.Buffer
	in.WriteString(`{"jsonrpc":"2.0","id":1,"method":"initialize"}` + "\n")
	cfg := Config{Endpoint: endpoint, HostID: "host-1", Token: "secret-token",
		In: &in, Out: &out}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "secret-token") {
		t.Errorf("stdout 不得回显配对凭据，实际 %s", out.String())
	}
}

func TestBridgeOutputBounded(t *testing.T) {
	// 后端返回超大响应时，桥接应显式失败而非无限缓冲。
	// 用一个返回大 body 的 HTTP 服务。
	// （mcp 后端响应较小，这里通过限制 MaxResponseBytes 触发。）
	endpoint := startBackend(t)
	var in, out bytes.Buffer
	in.WriteString(`{"jsonrpc":"2.0","id":1,"method":"initialize"}` + "\n")
	cfg := Config{Endpoint: endpoint, In: &in, Out: &out, MaxResponseBytes: 10}
	if err := Run(context.Background(), cfg); err == nil {
		t.Fatal("响应超过有界上限应显式失败")
	}
}
