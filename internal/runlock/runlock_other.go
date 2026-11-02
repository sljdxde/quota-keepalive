//go:build !darwin && !linux && !windows

package runlock

import "errors"

// ErrUnsupported reports that this platform has no cross-process lock backend.
var ErrUnsupported = errors.New("runlock: platform unsupported")

// Lock is a no-op placeholder for unsupported platforms.
type Lock struct{}

// acquireFile refuses to run without a real cross-process lock.
func acquireFile(string) (*Lock, error) {
	return nil, ErrUnsupported
}

// Close is a no-op.
func (*Lock) Close() error { return nil }
