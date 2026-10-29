package gui

import (
	"context"

	"qka/internal/accounts"
)

// AccountBridge 是账号管理页所需的后端能力（由应用装配实现）。
type AccountBridge interface {
	// List 返回所有目标应用的账号。
	List(ctx context.Context) ([]accounts.Account, error)
	// Discover 经适配器发现目标应用的账号（脱敏、无凭据）。
	Discover(ctx context.Context, target string) error
	// BeginLogin 发起目标应用的官方登录（浏览器打开官方入口）。
	BeginLogin(ctx context.Context, target string) error
	// ImportSource 导入一个只读来源。
	ImportSource(ctx context.Context, target, accountID, sourceID string) error
	// SetEnabled 逐账号启停。
	SetEnabled(ctx context.Context, target, accountID string, enabled bool) error
	// Delete 删除 QKA 映射（仅宿主数据）。
	Delete(ctx context.Context, target, accountID string) error
	// PinSource 设置来源优先级（置顶/取消）。
	PinSource(ctx context.Context, target, accountID, sourceID string, pinned bool) error
}
