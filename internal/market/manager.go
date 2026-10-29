package market

import (
	"errors"
	"sync"
)

var (
	// ErrVersionPinned 表示任务执行中，版本固定不允许切换。
	ErrVersionPinned = errors.New("market: 任务执行中，版本固定")
	// ErrNoFallback 表示无可信回退版本。
	ErrNoFallback = errors.New("market: 无可信回退版本，已禁用")
)

// InstalledRelease 是一个已验证并安装的版本。
type InstalledRelease struct {
	ID      string
	Version string
}

// Manager 管理当前版本、上一可信版本（回退）与执行中固定。
type Manager struct {
	mu       sync.Mutex
	current  *InstalledRelease
	previous *InstalledRelease
	inFlight bool
}

// NewManager 组装管理器。
func NewManager(initial *InstalledRelease) *Manager {
	return &Manager{current: initial}
}

// BeginTask 标记任务开始：期间版本固定。
func (m *Manager) BeginTask() {
	m.mu.Lock()
	m.inFlight = true
	m.mu.Unlock()
}

// EndTask 标记任务结束：可切换版本。
func (m *Manager) EndTask() {
	m.mu.Lock()
	m.inFlight = false
	m.mu.Unlock()
}

// Activate 在验证通过后激活新版本；执行中固定则拒绝。
func (m *Manager) Activate(r InstalledRelease) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.inFlight {
		return ErrVersionPinned
	}
	m.previous = m.current
	m.current = &r
	return nil
}

// Rollback 在加载/健康失败时回退上一可信版本；无回退则禁用（current 置空）。
func (m *Manager) Rollback() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.previous == nil {
		m.current = nil
		return ErrNoFallback
	}
	m.current = m.previous
	m.previous = nil
	return nil
}

// Current 返回当前版本；nil 表示已禁用（无可用可信版本）。
func (m *Manager) Current() *InstalledRelease {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.current
}
