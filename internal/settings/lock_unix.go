//go:build darwin || linux

package settings

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	"qka/internal/atomicfile"
)

// withConfigLock 在持有进程间排他文件锁时执行 fn。
func withConfigLock(configDir string, fn func() error) error {
	if err := atomicfile.EnsureDir(configDir, 0o700); err != nil {
		return err
	}
	lockPath := filepath.Join(configDir, "settings.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		return err
	}
	defer unix.Flock(int(f.Fd()), unix.LOCK_UN)
	return fn()
}
