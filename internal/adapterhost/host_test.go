package adapterhost

import (
	"context"
	"errors"
	"testing"
	"time"
)

const fakeAdapter = "testdata/fake-adapter"

func launchFake(t *testing.T) *Host {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	h, err := Launch(ctx, "bun", fakeAdapter)
	if err != nil {
		t.Fatalf("启动假适配器失败: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })
	return h
}

func TestLaunchCompletesHandshake(t *testing.T) {
	h := launchFake(t)
	if h.Exited() {
		t.Fatal("握手成功后子进程不应退出")
	}
	if h.Manifest().ID != "fake" {
		t.Errorf("manifest 加载错误: %s", h.Manifest().ID)
	}
}

func TestCallDeclaredAction(t *testing.T) {
	h := launchFake(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := h.Call(ctx, "checkin")
	if err != nil {
		t.Fatalf("派发 checkin 失败: %v", err)
	}
	if string(result) == "" {
		t.Fatal("期望非空结果")
	}
}

func TestCallUndeclaredActionRejected(t *testing.T) {
	h := launchFake(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := h.Call(ctx, "keepalive")
	if !errors.Is(err, ErrUndeclaredAction) {
		t.Fatalf("未声明操作应被拒绝，实际 %v", err)
	}
}

func TestCallCrashIsolatedAndObservable(t *testing.T) {
	h := launchFake(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := h.Call(ctx, "crash")
	if err == nil {
		t.Fatal("子进程崩溃应让调用返回错误")
	}
	if !errors.Is(err, ErrAdapterCrashed) {
		t.Fatalf("期望 ErrAdapterCrashed，实际 %v", err)
	}
	if !h.Exited() {
		t.Fatal("应可观测到子进程已退出")
	}

	// 崩溃后再调用：返回崩溃错误，宿主进程仍正常工作（隔离）。
	_, err = h.Call(ctx, "checkin")
	if !errors.Is(err, ErrAdapterCrashed) {
		t.Fatalf("崩溃后调用应返回崩溃错误，实际 %v", err)
	}
}

func TestCallOversizeMessageFailsBounded(t *testing.T) {
	h := launchFake(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := h.Call(ctx, "oversize")
	if !errors.Is(err, ErrMessageTooLarge) {
		t.Fatalf("超大有界上限应显式失败，实际 %v", err)
	}
}
