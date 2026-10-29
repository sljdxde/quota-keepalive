// Package devmode 实现未签名适配器的受限加载：默认禁用，显式开启开发者模式后
// 每次加载需确认并写审计，仅授予当场指定的单个账号/来源、仅当次调用，
// 不缓存不落盘，不能创建受管来源或触发官方刷新。
package devmode

import (
	"errors"
	"time"
)

var (
	// ErrDevModeDisabled 表示未开启开发者模式，未签名适配器不可加载。
	ErrDevModeDisabled = errors.New("devmode: 未开启开发者模式，未签名适配器不可加载")
	// ErrLoadDenied 表示用户在确认环节拒绝。
	ErrLoadDenied = errors.New("devmode: 用户拒绝加载")
)

// Confirmation 是每次加载需展示给用户确认的信息。
type Confirmation struct {
	PackageName string
	SourcePath  string
	Target      string
	AccountID   string
	SourceID    string
}

// UnverifiedLoad 是一次未签名适配器加载请求。
type UnverifiedLoad struct {
	PackageName string
	SourcePath  string
	Target      string
	AccountID   string
	SourceID    string
}

// AuditEvent 是一次加载相关的审计事件。
type AuditEvent struct {
	PackageName string
	SourcePath  string
	Target      string
	AccountID   string
	SourceID    string
	Action      string
	TS          time.Time
}

// ScopedGrant 是仅当次调用有效的单次授权（单账号、单已有来源）。
type ScopedGrant struct {
	Target    string
	AccountID string
	SourceID  string
}

// Allows 报告该单次授权是否覆盖指定账号/来源。
func (g ScopedGrant) Allows(target, accountID, sourceID string) bool {
	return g.Target == target && g.AccountID == accountID && g.SourceID == sourceID
}

// Gate 控制未签名适配器的受限加载。
type Gate struct {
	devMode bool
	confirm func(Confirmation) bool
	audit   func(AuditEvent)
	now     func() time.Time
}

// NewGate 组装开发者模式门禁。
func NewGate(devMode bool, confirm func(Confirmation) bool, audit func(AuditEvent)) *Gate {
	if confirm == nil {
		confirm = func(Confirmation) bool { return false }
	}
	if audit == nil {
		audit = func(AuditEvent) {}
	}
	return &Gate{
		devMode: devMode,
		confirm: confirm,
		audit:   audit,
		now:     func() time.Time { return time.Now() },
	}
}

// RequestLoad 处理一次加载请求；每次都需确认，授权不缓存。
func (g *Gate) RequestLoad(load UnverifiedLoad) (*ScopedGrant, error) {
	if !g.devMode {
		g.audit(AuditEvent{
			PackageName: load.PackageName, SourcePath: load.SourcePath,
			Target: load.Target, AccountID: load.AccountID, SourceID: load.SourceID,
			Action: "load_blocked_devmode_off", TS: g.now(),
		})
		return nil, ErrDevModeDisabled
	}

	confirmed := g.confirm(Confirmation{
		PackageName: load.PackageName,
		SourcePath:  load.SourcePath,
		Target:      load.Target,
		AccountID:   load.AccountID,
		SourceID:    load.SourceID,
	})
	if !confirmed {
		g.audit(AuditEvent{
			PackageName: load.PackageName, SourcePath: load.SourcePath,
			Target: load.Target, AccountID: load.AccountID, SourceID: load.SourceID,
			Action: "load_denied", TS: g.now(),
		})
		return nil, ErrLoadDenied
	}

	g.audit(AuditEvent{
		PackageName: load.PackageName, SourcePath: load.SourcePath,
		Target: load.Target, AccountID: load.AccountID, SourceID: load.SourceID,
		Action: "load_confirmed", TS: g.now(),
	})
	// 仅授予当场指定的单账号/单来源，且仅当次调用（返回值不被 Gate 缓存）。
	return &ScopedGrant{
		Target:    load.Target,
		AccountID: load.AccountID,
		SourceID:  load.SourceID,
	}, nil
}
