// Package adapterwiring 把 capabilities 提供的能力接线为适配器可调用的反向 JSON-RPC 钩子。
package adapterwiring

import (
	"context"
	"encoding/json"
	"fmt"
)

// HookFunc 处理单个钩子方法。
type HookFunc func(ctx context.Context, params json.RawMessage) (json.RawMessage, error)

// HookMux 按方法名分发钩子，实现 adapterhost.HookHandler。
type HookMux struct {
	hooks map[string]HookFunc
	// Init 是下发给适配器 initialize 的配置（如 baseURL）。
	Init map[string]any
}

// NewHookMux 创建空多路复用器。
func NewHookMux() *HookMux {
	return &HookMux{hooks: map[string]HookFunc{}, Init: map[string]any{}}
}

// On 注册一个钩子方法。
func (m *HookMux) On(method string, fn HookFunc) {
	m.hooks[method] = fn
}

// HandleHook 实现 adapterhost.HookHandler。
func (m *HookMux) HandleHook(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	fn, ok := m.hooks[method]
	if !ok {
		return nil, fmt.Errorf("未注册的钩子: %s", method)
	}
	return fn(ctx, params)
}

// InitParams 实现 adapterhost 的 initConfigProvider，把配置下发给适配器。
func (m *HookMux) InitParams() map[string]any {
	return m.Init
}
