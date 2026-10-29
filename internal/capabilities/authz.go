// Package capabilities 提供适配器经受限钩子调用的通用能力：凭据、出站 HTTP、状态与日志。
// 钩子按 manifest 的 needs 授权，越权调用直接拒绝。
package capabilities

import (
	"errors"
	"fmt"
)

// ErrHookNotGranted 表示调用了未在 manifest needs 中声明的钩子。
var ErrHookNotGranted = errors.New("钩子未被 manifest needs 授权，拒绝调用")

// Grant 是一个适配器被授权的钩子集合。
type Grant map[string]bool

// GrantFromNeeds 用 manifest 的 needs 列表构造授权集合。
func GrantFromNeeds(needs []string) Grant {
	g := Grant{}
	for _, n := range needs {
		g[n] = true
	}
	return g
}

// Authorize 校验钩子是否被授权；未授权返回 ErrHookNotGranted。
func (g Grant) Authorize(hook string) error {
	if g[hook] {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrHookNotGranted, hook)
}
