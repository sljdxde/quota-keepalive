// Package scheduler installs QKA's repeating task-run invocation in the current
// user's native scheduler. Business execution stays in app.RunAll; this package
// only decides when the executable is launched.
package scheduler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
)

// ErrUnsupported reports that this platform has no scheduler backend.
var ErrUnsupported = errors.New("scheduler: platform unsupported")

var taskHashWindows = runtime.GOOS == "windows"

// Spec describes one QKA auto-run task.
type Spec struct {
	Executable      string
	ConfigDir       string
	IntervalMinutes int
	Enabled         bool
}

// Status is the native scheduler state shown by the settings UI.
type Status struct {
	Installed   bool   `json:"installed"`
	NextRun     string `json:"next_run,omitempty"`
	LastError   string `json:"last_error,omitempty"`
	PowerPolicy string `json:"power_policy,omitempty"`
}

// Scheduler is the platform-neutral contract.
type Scheduler interface {
	Install(ctx context.Context, spec Spec) error
	Remove(ctx context.Context, spec Spec) error
	Status(ctx context.Context, spec Spec) (Status, error)
}

// TaskHash derives a stable short identifier from the canonical config path.
func TaskHash(configDir string) string {
	canonical := filepath.Clean(configDir)
	if abs, err := filepath.Abs(canonical); err == nil {
		canonical = abs
	}
	if resolved, err := filepath.EvalSymlinks(canonical); err == nil {
		canonical = resolved
	}
	if taskHashWindows {
		canonical = strings.ToLower(canonical)
	}
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:4])
}

// LaunchdLabel is the user LaunchAgent label for one config directory.
func LaunchdLabel(configDir string) string {
	return "com.qka.agent." + TaskHash(configDir)
}

// WindowsTaskName is the Task Scheduler name for one config directory.
func WindowsTaskName(configDir string) string {
	return "QKA Auto Run " + TaskHash(configDir)
}

func executableOK(value string) bool {
	return strings.TrimSpace(value) != "" && !strings.ContainsRune(value, '\x00')
}
