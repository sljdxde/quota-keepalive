package runlock

import (
	"fmt"
	"path/filepath"
	"strings"

	"qka/internal/atomicfile"
)

const executionLockName = "run.lock"

// Acquire waits for the per-config execution lock.
func Acquire(configDir string) (*Lock, error) {
	return AcquireNamed(configDir, executionLockName)
}

// AcquireNamed waits for an owner-only cross-process lock with the given file
// name under configDir. Names must be a single basename so callers cannot
// escape the QKA config directory.
func AcquireNamed(configDir, name string) (*Lock, error) {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." ||
		filepath.Base(name) != name || strings.ContainsAny(name, `/\`) {
		return nil, fmt.Errorf("runlock: invalid lock name %q", name)
	}
	if err := atomicfile.EnsureDir(configDir, 0o700); err != nil {
		return nil, err
	}
	return acquireFile(filepath.Join(configDir, name))
}
