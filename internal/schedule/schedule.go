// Package schedule stores QKA's built-in interval-run preference and the last
// scheduled execution summary. It is intentionally separate from settings.json
// so numeric and action fields do not weaken the existing bool/select schema.
package schedule

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"qka/internal/atomicfile"
	"qka/internal/features"
	"qka/internal/runlock"
)

const (
	// FileName is the auto-run state file under the QKA config directory.
	FileName = "schedule.json"
	lockName = "schedule.lock"
	// DefaultIntervalMinutes is the first-version execution interval.
	DefaultIntervalMinutes = 30
	// TimezoneLocal means the system task follows the current machine timezone.
	TimezoneLocal = "local"
)

var (
	// ErrInvalidInterval reports a value outside the supported interval range.
	ErrInvalidInterval = errors.New("schedule: invalid interval")
	// ErrInvalidAction reports an unsupported action selection.
	ErrInvalidAction = errors.New("schedule: invalid action")
	// ErrInvalidTimezone reports an unsupported timezone mode.
	ErrInvalidTimezone = errors.New("schedule: invalid timezone")
)

const (
	// MinIntervalMinutes is the shortest supported interval.
	MinIntervalMinutes = 5
	// MaxIntervalMinutes is the longest supported interval (seven days).
	MaxIntervalMinutes = 7 * 24 * 60
)

const (
	ActionAll       = "all"
	ActionCheckin   = "checkin"
	ActionKeepalive = "keepalive"
)

// LastRun is a redacted summary of one task-run invocation.
type LastRun struct {
	At             string `json:"at"`
	Result         string `json:"result"`
	Attempted      int    `json:"attempted"`
	Succeeded      int    `json:"succeeded"`
	NeedsAttention int    `json:"needs_attention"`
	Skipped        int    `json:"skipped"`
	Message        string `json:"message,omitempty"`
}

// Config is persisted in schedule.json.
type Config struct {
	Enabled         bool     `json:"enabled"`
	IntervalMinutes int      `json:"interval_minutes"`
	Action          string   `json:"action"`
	Timezone        string   `json:"timezone"`
	LastRun         *LastRun `json:"last_run,omitempty"`
}

// Default returns a disabled schedule using local time. Installing a system
// task remains an explicit user action.
func Default() Config {
	return Config{
		Enabled:         false,
		IntervalMinutes: DefaultIntervalMinutes,
		Action:          ActionCheckin,
		Timezone:        TimezoneLocal,
	}
}

func (c *Config) normalize() {
	if c.IntervalMinutes == 0 {
		c.IntervalMinutes = DefaultIntervalMinutes
	}
	if c.Action == "" {
		c.Action = ActionCheckin
	}
	c.Action = features.NormalizeAction(c.Action)
	if c.Timezone == "" {
		c.Timezone = TimezoneLocal
	}
}

func (c Config) validate() error {
	if c.Timezone != TimezoneLocal {
		return ErrInvalidTimezone
	}
	if c.IntervalMinutes < MinIntervalMinutes || c.IntervalMinutes > MaxIntervalMinutes {
		return ErrInvalidInterval
	}
	switch c.Action {
	case ActionAll, ActionCheckin, ActionKeepalive:
	default:
		return ErrInvalidAction
	}
	if c.LastRun == nil {
		return nil
	}
	if c.LastRun.At == "" {
		return errors.New("schedule: last_run.at is required")
	}
	if _, err := time.Parse(time.RFC3339, c.LastRun.At); err != nil {
		return fmt.Errorf("schedule: invalid last_run.at: %w", err)
	}
	switch c.LastRun.Result {
	case "success", "needs_attention", "failed", "disabled":
	default:
		return fmt.Errorf("schedule: invalid last_run.result %q", c.LastRun.Result)
	}
	if c.LastRun.Attempted < 0 || c.LastRun.Succeeded < 0 ||
		c.LastRun.NeedsAttention < 0 || c.LastRun.Skipped < 0 {
		return errors.New("schedule: negative run count")
	}
	return nil
}

// NextRunAt returns a conservative display hint for the next interval run.
// Native schedulers own exact firing; this value avoids claiming a precise
// completion time when the first interval starts at install time.
func NextRunAt(now time.Time, intervalMinutes int) (time.Time, error) {
	if intervalMinutes < MinIntervalMinutes || intervalMinutes > MaxIntervalMinutes {
		return time.Time{}, ErrInvalidInterval
	}
	return now.Add(time.Duration(intervalMinutes) * time.Minute), nil
}

// Load reads schedule.json. A missing file returns the disabled default.
func Load(configDir string) (Config, error) {
	path := filepath.Join(configDir, FileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Config{}, err
	}
	cfg := Default()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	cfg.normalize()
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Save atomically persists schedule.json with owner-only permissions.
func Save(configDir string, cfg Config) error {
	cfg.normalize()
	if err := cfg.validate(); err != nil {
		return err
	}
	if err := atomicfile.EnsureDir(configDir, 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	return atomicfile.WriteFile(filepath.Join(configDir, FileName), data, 0o600)
}

// RecordLastRun updates only the recent-result section of schedule.json.
func RecordLastRun(configDir string, last LastRun) error {
	lock, err := runlock.AcquireNamed(configDir, lockName)
	if err != nil {
		return err
	}
	defer lock.Close()
	cfg, err := Load(configDir)
	if err != nil {
		return err
	}
	cfg.LastRun = &last
	return Save(configDir, cfg)
}
