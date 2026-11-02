//go:build darwin || linux

// Package runlock serializes task-run and manual RunAll executions per QKA
// config directory. The lock is held across processes and released on close.
package runlock

import (
	"os"

	"golang.org/x/sys/unix"
)

// Lock is an acquired cross-process execution lock.
type Lock struct {
	file *os.File
}

func acquireFile(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &Lock{file: f}, nil
}

// Close releases the lock. It is safe to call once.
func (l *Lock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	f := l.file
	l.file = nil
	unlockErr := unix.Flock(int(f.Fd()), unix.LOCK_UN)
	closeErr := f.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
