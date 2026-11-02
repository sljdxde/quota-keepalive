//go:build darwin || linux || windows

package runlock

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestAcquireCreatesOwnerOnlyLockAndIsReusable(t *testing.T) {
	dir := t.TempDir()
	first, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "run.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Errorf("run.lock 不应对同组/其他用户开放，实际 %#o", info.Mode().Perm())
	}
}

func TestAcquireFilesystemFailures(t *testing.T) {
	t.Run("config path parent is a file", func(t *testing.T) {
		root := t.TempDir()
		file := filepath.Join(root, "not-a-dir")
		if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if lock, err := Acquire(filepath.Join(file, "config")); err == nil {
			_ = lock.Close()
			t.Fatal("EnsureDir 失败时 Acquire 必须返回错误")
		}
	})
	t.Run("lock path is a directory", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, "run.lock"), 0o700); err != nil {
			t.Fatal(err)
		}
		if lock, err := Acquire(dir); err == nil {
			_ = lock.Close()
			t.Fatal("run.lock 打开失败时 Acquire 必须返回错误")
		}
	})
	t.Run("flock error for fifo when unsupported", func(t *testing.T) {
		dir := t.TempDir()
		if err := syscall.Mkfifo(filepath.Join(dir, "run.lock"), 0o600); err != nil {
			t.Skipf("FIFO unavailable: %v", err)
		}
		lock, err := Acquire(dir)
		if err == nil {
			_ = lock.Close()
			t.Skip("this filesystem supports flock on FIFOs")
		}
	})
}

func TestCloseIsNilSafeIdempotentAndReportsUnlockFailure(t *testing.T) {
	var nilLock *Lock
	if err := nilLock.Close(); err != nil {
		t.Fatalf("nil Lock.Close 应成功: %v", err)
	}
	if err := (&Lock{}).Close(); err != nil {
		t.Fatalf("无文件 Lock.Close 应成功: %v", err)
	}

	lock, err := Acquire(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := lock.Close(); err == nil {
		t.Fatal("底层描述符已关闭时 unlock 应报告错误")
	}
	if err := lock.Close(); err != nil {
		t.Fatalf("Close 清空句柄后再次调用应成功: %v", err)
	}
}
