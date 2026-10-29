// Package privacy 提供以目标应用为粒度的隐私开关，在每个源（凭据读取、扫描、发现）
// 与每个汇（适配器派发、签到/保活、复核、上报）同时短路；停用后零凭据读取、零网络、零派发。
package privacy

import (
	"errors"
	"sync"
)

// ErrTargetDisabled 目标应用已停用，源/汇操作被短路。
var ErrTargetDisabled = errors.New("目标应用已停用")

// Gate 是目标应用隐私开关。
type Gate struct {
	mu       sync.Mutex
	disabled map[string]bool
}

// NewGate 创建默认全部启用的门。
func NewGate() *Gate {
	return &Gate{disabled: map[string]bool{}}
}

// Disable 停用一个目标应用。
func (g *Gate) Disable(target string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.disabled[target] = true
}

// Enable 重新启用一个目标应用（受管记录原样保留，重开即恢复）。
func (g *Gate) Enable(target string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.disabled, target)
}

// IsEnabled 报告目标是否启用。
func (g *Gate) IsEnabled(target string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return !g.disabled[target]
}

// Check 是源/汇门控：停用返回 ErrTargetDisabled。
func (g *Gate) Check(target string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.disabled[target] {
		return ErrTargetDisabled
	}
	return nil
}

// Guard 把一个操作包在门内：停用则不执行 fn。
func (g *Gate) Guard(target string, fn func() error) error {
	if err := g.Check(target); err != nil {
		return err
	}
	return fn()
}
