//go:build !windows

package cli

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"
)

// TestServeHandlerStopsOnSignal 覆盖常驻 serve 收到 SIGINT 后的优雅退出分支。
// 先注册测试自己的信号通道，确保信号不会透传到进程默认处理而中断 go test。
func TestServeHandlerStopsOnSignal(t *testing.T) {
	env, _, _ := coverageEnv(t)
	env.Stdout = io.Discard
	env.Stderr = io.Discard

	guard := make(chan os.Signal, 4)
	signal.Notify(guard, syscall.SIGINT)
	defer signal.Stop(guard)

	type result struct {
		resp *Response
		err  error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := serveHandler(context.Background(), env,
			[]string{"--addr", "127.0.0.1:0", "--json"})
		done <- result{resp: resp, err: err}
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		select {
		case got := <-done:
			if got.resp != nil || got.err != nil {
				t.Fatalf("serveHandler 信号退出: resp=%v err=%v", got.resp, got.err)
			}
			return
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("serveHandler 未在收到 SIGINT 后退出")
		}
		if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
			t.Fatalf("发送 SIGINT 失败: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
