//go:build darwin || linux

package settings

import (
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestWithConfigLockReportsFlockFailure(t *testing.T) {
	dir := t.TempDir()
	if err := unix.Mkfifo(filepath.Join(dir, "settings.lock"), 0o600); err != nil {
		t.Fatal(err)
	}
	called := false
	if err := withConfigLock(dir, func() error {
		called = true
		return nil
	}); err == nil {
		t.Fatal("flock on a FIFO should fail")
	}
	if called {
		t.Fatal("callback should not run when flock fails")
	}
}
