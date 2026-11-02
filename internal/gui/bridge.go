package gui

import (
	"context"
	"errors"

	"qka/internal/accounts"
	"qka/internal/status"
	"qka/internal/workbuddy"
)

var errUnsupportedAccountAction = errors.New("账号页只支持签到与额度刷新")

// AccountView is the account-management projection. It contains only
// user-visible account/source metadata plus today's operation state.
type AccountView struct {
	accounts.Account
	SourceHealth    string            `json:"source_health"`
	Operations      []status.OpStatus `json:"operations"`
	TaskOperations  []string          `json:"task_operations"`
	TaskPreferences map[string]bool   `json:"task_preferences"`
}

// AccountBridge 是账号管理页所需的后端能力（由应用装配实现）。
type AccountBridge interface {
	// List 返回所有目标应用的账号。
	List(ctx context.Context) ([]AccountView, error)
	// Discover 经适配器发现目标应用的账号（脱敏、无凭据）。
	Discover(ctx context.Context, target string) error
	// BeginLogin 发起目标应用的官方登录并返回可见状态。
	BeginLogin(ctx context.Context, target string) (workbuddy.LoginView, error)
	// LoginStatus 返回当前或最近一次官方登录状态。
	LoginStatus(ctx context.Context, target string) workbuddy.LoginView
	// CancelLogin 取消进行中的官方登录。
	CancelLogin(ctx context.Context, target string) (workbuddy.LoginView, error)
	// ImportSource 导入一个只读来源。
	ImportSource(ctx context.Context, target, accountID, sourceID string) error
	// SetEnabled 逐账号启停。
	SetEnabled(ctx context.Context, target, accountID string, enabled bool) error
	// Delete 删除 QKA 映射（仅宿主数据）。
	Delete(ctx context.Context, target, accountID string) error
	// PinSource 设置来源优先级（置顶/取消）。
	PinSource(ctx context.Context, target, accountID, sourceID string, pinned bool) error
	// Run 对单个账号执行一次操作（checkin / refresh）。
	Run(ctx context.Context, target, accountID, operation string) error
	// SetTaskEnabled 设置自动执行是否包含该账号的某一任务。
	SetTaskEnabled(ctx context.Context, target, accountID, operation string, enabled bool) error
}
