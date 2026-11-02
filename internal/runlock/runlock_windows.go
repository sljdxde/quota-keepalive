//go:build windows

package runlock

import (
	"os"

	"golang.org/x/sys/windows"
)

// Lock is an acquired cross-process execution lock.
type Lock struct {
	file       *os.File
	overlapped windows.Overlapped
}

func acquireFile(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	lock := &Lock{file: f}
	err = windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK,
		0, 1, 0,
		&lock.overlapped,
	)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return lock, nil
}

// Close releases the lock. It is safe to call once.
func (l *Lock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	f := l.file
	l.file = nil
	unlockErr := windows.UnlockFileEx(
		windows.Handle(f.Fd()), 0, 1, 0, &l.overlapped,
	)
	closeErr := f.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
