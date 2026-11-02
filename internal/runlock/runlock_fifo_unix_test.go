//go:build !windows

package runlock

import (
	"path/filepath"
	"syscall"
	"testing"
)

// TestAcquireRejectsFifoLockPath 覆盖「run.lock 是 FIFO、flock 不可用」的分支。
// mkfifo 是 POSIX 能力，Windows 没有对应语义，因此单独放在 unix-only 文件里，
// 避免 Windows 上编译失败。
func TestAcquireRejectsFifoLockPath(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "run.lock"), 0o600); err != nil {
		t.Skipf("FIFO unavailable: %v", err)
	}
	lock, err := Acquire(dir)
	if err == nil {
		_ = lock.Close()
		t.Skip("this filesystem supports flock on FIFOs")
	}
}
