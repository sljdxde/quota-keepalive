package adapterhost

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type nopWriteCloser struct {
	io.Writer
}

func (nopWriteCloser) Close() error { return nil }

type initParamsHook struct {
	recordingHookHandler
}

func (*initParamsHook) InitParams() map[string]any {
	return map[string]any{"baseURL": "http://127.0.0.1"}
}

func TestHostErrorHelpers(t *testing.T) {
	if _, err := (denyAllHooks{}).HandleHook(context.Background(), "echo", nil); err == nil ||
		!strings.Contains(err.Error(), "echo") {
		t.Fatalf("默认钩子应拒绝并包含方法名，实际 %v", err)
	}
	if got := toRPCError(nil); got.Message != ErrAdapterCrashed.Error() {
		t.Fatalf("nil cause 应收敛为崩溃错误，实际 %+v", got)
	}
	if got := normalizeExitCause(errors.New("boom")); !errors.Is(got, ErrAdapterCrashed) {
		t.Fatalf("普通退出应归为崩溃，实际 %v", got)
	}
	if got := normalizeExitCause(nil); !errors.Is(got, ErrAdapterCrashed) {
		t.Fatalf("nil 退出原因应归为崩溃，实际 %v", got)
	}
}

func TestLaunchWithManifestAndStartFailures(t *testing.T) {
	if _, err := LaunchWith(context.Background(), "bun", t.TempDir(), nil); err == nil {
		t.Fatal("缺少 manifest 应失败")
	}

	original := newAdapterCommand
	t.Cleanup(func() { newAdapterCommand = original })
	newAdapterCommand = exec.Command
	if _, err := LaunchWith(
		context.Background(),
		filepath.Join(t.TempDir(), "missing-bun"),
		fakeAdapter,
		nil,
	); err == nil {
		t.Fatal("可执行文件不存在时应失败")
	}
}

func TestLaunchWithPipeFailures(t *testing.T) {
	cases := []struct {
		name string
		cmd  *exec.Cmd
	}{
		{"stdin", &exec.Cmd{Stdin: strings.NewReader("")}},
		{"stdout", &exec.Cmd{Stdout: io.Discard}},
		{"stderr", &exec.Cmd{Stderr: io.Discard}},
	}

	original := newAdapterCommand
	t.Cleanup(func() { newAdapterCommand = original })
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			newAdapterCommand = func(string, ...string) *exec.Cmd { return tc.cmd }
			if _, err := LaunchWith(context.Background(), "bun", fakeAdapter, nil); err == nil {
				t.Fatalf("%s pipe 预置冲突时应失败", tc.name)
			}
		})
	}
}

func TestLaunchWithInitParamsAndHandshakeFailure(t *testing.T) {
	hooks := &initParamsHook{
		recordingHookHandler: recordingHookHandler{
			result: json.RawMessage(`{"ok":true}`),
		},
	}
	h, err := LaunchWith(
		context.Background(),
		"bun",
		filepath.Join("testdata", "hook-adapter"),
		hooks,
	)
	if err != nil {
		t.Fatalf("带初始化参数的握手应成功: %v", err)
	}
	_ = h.Close()

	if _, err := LaunchWith(
		context.Background(),
		"bun",
		filepath.Join("testdata", "init-error"),
		nil,
	); err == nil || !strings.Contains(err.Error(), "initialize") {
		t.Fatalf("initialize 拒绝时应返回握手错误，实际 %v", err)
	}
}

func TestReadLoopIgnoresMalformedLinesAndDeliversResponse(t *testing.T) {
	h := &Host{pending: map[int]chan rpcResponse{}}
	ch := make(chan rpcResponse, 1)
	h.pending[1] = ch

	h.readLoop(strings.NewReader("{\n{\"id\":\"not-an-int\"}\n" +
		"{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"ok\":true}}\n"))

	select {
	case got := <-ch:
		if got.ID != 1 || string(got.Result) != `{"ok":true}` {
			t.Fatalf("响应投递错误: %+v", got)
		}
	default:
		t.Fatal("合法响应应投递给等待方")
	}
}

func TestReadLoopShutsDownOnScannerError(t *testing.T) {
	h := &Host{
		pending: map[int]chan rpcResponse{},
		stdin:   nopWriteCloser{Writer: io.Discard},
		cmd:     &exec.Cmd{},
	}
	h.readLoop(strings.NewReader(strings.Repeat("x", MaxMessageBytes+1)))
	if !errors.Is(h.exitErr, ErrMessageTooLarge) {
		t.Fatalf("超长行应终止宿主，实际 %v", h.exitErr)
	}
}

func TestHandleHookRequestMarshalFailureIsIgnored(t *testing.T) {
	h := &Host{
		hookHandler: &recordingHookHandler{result: json.RawMessage(`{`)},
		stdin:       nopWriteCloser{Writer: io.Discard},
		pending:     map[int]chan rpcResponse{},
	}
	h.handleHookRequest(hookRequest{
		ID:     json.RawMessage(`1`),
		Method: "echo",
	})
}

func TestRequestFailureAndCancellationPaths(t *testing.T) {
	t.Run("已退出", func(t *testing.T) {
		sentinel := errors.New("closed")
		h := &Host{exited: true, exitErr: sentinel}
		if _, err := h.request(context.Background(), "x", nil); !errors.Is(err, sentinel) {
			t.Fatalf("已退出应返回权威 cause，实际 %v", err)
		}
	})

	t.Run("参数不可序列化", func(t *testing.T) {
		h := &Host{
			stdin:   nopWriteCloser{Writer: io.Discard},
			pending: map[int]chan rpcResponse{},
		}
		if _, err := h.request(context.Background(), "x", make(chan int)); err == nil {
			t.Fatal("不可序列化参数应失败")
		}
	})

	t.Run("上下文取消", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		h := &Host{
			stdin:   nopWriteCloser{Writer: io.Discard},
			pending: map[int]chan rpcResponse{},
		}
		if _, err := h.request(ctx, "x", nil); !errors.Is(err, context.Canceled) {
			t.Fatalf("取消应返回 context.Canceled，实际 %v", err)
		}
		if len(h.pending) != 0 {
			t.Fatalf("取消后应移除 pending，实际 %d", len(h.pending))
		}
	})
}

func TestStderrCollectorIsBounded(t *testing.T) {
	var collector stderrCollector
	first := strings.Repeat("a", MaxStderrBytes)
	if n, err := collector.Write([]byte(first)); err != nil || n != len(first) {
		t.Fatalf("首次写入应全部消费: n=%d err=%v", n, err)
	}
	if n, err := collector.Write([]byte("tail")); err != nil || n != 4 {
		t.Fatalf("超限写入仍应消费: n=%d err=%v", n, err)
	}
	if got := collector.String(); len(got) != MaxStderrBytes {
		t.Fatalf("stderr 应限制为 %d 字节，实际 %d", MaxStderrBytes, len(got))
	}

	small := stderrCollector{limit: 2}
	_, _ = small.Write([]byte("abcd"))
	if got := small.String(); got != "ab" {
		t.Fatalf("自定义上限应截断，实际 %q", got)
	}

	short := stderrCollector{limit: 8}
	_, _ = short.Write([]byte("abc"))
	if got := short.String(); got != "abc" {
		t.Fatalf("未满上限时应完整保留，实际 %q", got)
	}
}

func TestCallWithArgsAndStderrAccessor(t *testing.T) {
	h := launchFake(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := h.CallWith(ctx, "checkin", map[string]any{"accountId": "a"}); err != nil {
		t.Fatalf("带参数派发应成功: %v", err)
	}
	if got := h.Stderr(); got != "" {
		t.Fatalf("假适配器不应写 stderr，实际 %q", got)
	}
}

func TestLoadManifestRejectsMalformedAndInvalidFiles(t *testing.T) {
	t.Run("非法 JSON", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(
			filepath.Join(dir, "manifest.json"),
			[]byte(`{`),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadManifest(dir); err == nil {
			t.Fatal("非法 JSON 应失败")
		}
	})

	t.Run("校验失败", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(
			filepath.Join(dir, "manifest.json"),
			[]byte(`{"id":"missing-fields"}`),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadManifest(dir); err == nil {
			t.Fatal("缺少必填字段应失败")
		}
	})
}
