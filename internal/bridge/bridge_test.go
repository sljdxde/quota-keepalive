package bridge

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"qka/internal/mcp"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errors.New("read") }

func TestBridgeSkipsBlankAndReportsWriteFailure(t *testing.T) {
	writeErr := errors.New("write")
	err := Run(context.Background(), Config{
		In: strings.NewReader("\n \n"), Out: failingWriter{err: writeErr},
		Endpoint: "http://127.0.0.1:1", HTTPClient: http.DefaultClient,
	})
	if err != nil {
		t.Fatalf("仅空行不应触发网络或写入，实际 %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	err = Run(context.Background(), Config{
		In: strings.NewReader("{}\n"), Out: failingWriter{err: writeErr},
		Endpoint: server.URL,
	})
	if !errors.Is(err, writeErr) {
		t.Fatalf("写响应失败应原样返回，实际 %v", err)
	}
}

func TestBridgeForwardFailures(t *testing.T) {
	if _, err := forward(context.Background(), http.DefaultClient, "://bad", "", "", []byte("{}"), 10); err == nil {
		t.Fatal("非法 endpoint 应失败")
	}
	networkErr := errors.New("network")
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, networkErr
	})}
	if _, err := forward(context.Background(), client, "http://example.test", "", "", []byte("{}"), 10); !errors.Is(err, networkErr) {
		t.Fatalf("HTTP client 错误应原样返回，实际 %v", err)
	}
	client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{Body: io.NopCloser(errorReader{}), StatusCode: 200}, nil
	})}
	if _, err := forward(context.Background(), client, "http://example.test", "", "", []byte("{}"), 10); err == nil {
		t.Fatal("响应体读取失败应返回错误")
	}
}

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
