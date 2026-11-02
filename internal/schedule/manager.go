package schedule

import (
	"context"
	"errors"
	"fmt"
	"time"

	"qka/internal/runlock"
	"qka/internal/scheduler"
)

var saveConfig = Save

// View is the UI-facing combination of schedule.json and the native
// scheduler's own record. It contains no credentials or account identifiers.
type View struct {
	Enabled         bool      `json:"enabled"`
	IntervalMinutes int       `json:"interval_minutes"`
	Action          string    `json:"action"`
	Timezone        string    `json:"timezone"`
	Installed       bool      `json:"installed"`
	NextRun         string    `json:"next_run,omitempty"`
	PowerPolicy     string    `json:"power_policy,omitempty"`
	LastRun         *LastRun  `json:"last_run,omitempty"`
	LastError       string    `json:"last_error,omitempty"`
	Now             time.Time `json:"-"`
}

// Manager owns the relationship between QKA's schedule.json and the native
// scheduler. Business execution remains app.RunAll.
type Manager struct {
	ConfigDir  string
	Executable string
	Scheduler  scheduler.Scheduler
	Now        func() time.Time
}

// NewManager constructs the production manager.
func NewManager(configDir, executable string, s scheduler.Scheduler, now func() time.Time) *Manager {
	if now == nil {
		now = time.Now
	}
	return &Manager{ConfigDir: configDir, Executable: executable, Scheduler: s, Now: now}
}

func (m *Manager) spec(cfg Config) scheduler.Spec {
	return scheduler.Spec{
		Executable:      m.Executable,
		ConfigDir:       m.ConfigDir,
		IntervalMinutes: cfg.IntervalMinutes,
		Enabled:         cfg.Enabled,
	}
}

// Get returns the current schedule plus the native scheduler's record.
func (m *Manager) Get(ctx context.Context) (View, error) {
	if m.Scheduler == nil {
		return View{}, fmt.Errorf("schedule: missing scheduler")
	}
	lock, err := runlock.AcquireNamed(m.ConfigDir, lockName)
	if err != nil {
		return View{}, err
	}
	defer lock.Close()
	return m.get(ctx)
}

func (m *Manager) get(ctx context.Context) (View, error) {
	cfg, err := Load(m.ConfigDir)
	if err != nil {
		return View{}, err
	}
	now := m.Now()
	st, err := m.Scheduler.Status(ctx, m.spec(cfg))
	if err != nil {
		return View{}, err
	}
	return View{
		Enabled:         cfg.Enabled,
		IntervalMinutes: cfg.IntervalMinutes,
		Action:          cfg.Action,
		Timezone:        cfg.Timezone,
		Installed:       st.Installed,
		PowerPolicy:     st.PowerPolicy,
		LastRun:         cfg.LastRun,
		LastError:       st.LastError,
		Now:             now,
	}, nil
}

// SetEnabled installs the native task before persisting enabled=true, and
// removes it before persisting enabled=false. Failed system changes never
// leave schedule.json claiming the opposite of the system's state.
func (m *Manager) SetEnabled(ctx context.Context, enabled bool) (View, error) {
	if m.Scheduler == nil {
		return View{}, fmt.Errorf("schedule: missing scheduler")
	}
	lock, err := runlock.AcquireNamed(m.ConfigDir, lockName)
	if err != nil {
		return View{}, err
	}
	defer lock.Close()
	cfg, err := Load(m.ConfigDir)
	if err != nil {
		return View{}, err
	}
	previous := cfg
	if enabled {
		if m.Executable == "" {
			return View{}, fmt.Errorf("schedule: executable path is unavailable")
		}
		cfg.Enabled = true
		if err := m.Scheduler.Install(ctx, m.spec(cfg)); err != nil {
			return View{}, err
		}
		if err := saveConfig(m.ConfigDir, cfg); err != nil {
			return View{}, errors.Join(err, m.restore(ctx, previous))
		}
	} else {
		cfg.Enabled = false
		if err := m.Scheduler.Remove(ctx, m.spec(cfg)); err != nil {
			return View{}, err
		}
		if err := saveConfig(m.ConfigDir, cfg); err != nil {
			return View{}, errors.Join(err, m.restore(ctx, previous))
		}
	}
	return m.get(ctx)
}

// SetConfig validates and applies the interval/action selection. When enabled,
// the native task is reinstalled before schedule.json is changed so the two
// records cannot disagree after a failed system operation.
func (m *Manager) SetConfig(
	ctx context.Context, intervalMinutes int, action string,
) (View, error) {
	if m.Scheduler == nil {
		return View{}, fmt.Errorf("schedule: missing scheduler")
	}
	lock, err := runlock.AcquireNamed(m.ConfigDir, lockName)
	if err != nil {
		return View{}, err
	}
	defer lock.Close()
	cfg, err := Load(m.ConfigDir)
	if err != nil {
		return View{}, err
	}
	previous := cfg
	cfg.IntervalMinutes = intervalMinutes
	cfg.Action = action
	cfg.normalize()
	if err := cfg.validate(); err != nil {
		return View{}, err
	}
	if cfg.Enabled {
		if m.Executable == "" {
			return View{}, fmt.Errorf("schedule: executable path is unavailable")
		}
		if err := m.Scheduler.Install(ctx, m.spec(cfg)); err != nil {
			return View{}, err
		}
	}
	if err := saveConfig(m.ConfigDir, cfg); err != nil {
		if cfg.Enabled {
			return View{}, errors.Join(err, m.restore(ctx, previous))
		}
		return View{}, err
	}
	return m.get(ctx)
}

func (m *Manager) restore(ctx context.Context, cfg Config) error {
	if cfg.Enabled {
		return m.Scheduler.Install(ctx, m.spec(cfg))
	}
	return m.Scheduler.Remove(ctx, m.spec(cfg))
}
