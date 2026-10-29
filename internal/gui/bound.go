package gui

import (
	"context"
	"encoding/json"

	"qka/internal/settings"
	"qka/internal/status"
)

// Deps 是 GUI 所需的后端能力（由应用装配注入）。
type Deps struct {
	// Targets 是受支持目标应用的有序清单（应用管理页始终列出，即使 0 账号）。
	Targets []string
	// Status 返回当前状态视图。
	Status func(ctx context.Context) (status.View, error)
	// Plan 返回只读预演。
	Plan func(ctx context.Context) (status.Plan, error)
	// Run 执行一次（立即执行）。
	Run func(ctx context.Context) error
	// Settings 读取当前设置。
	Settings func() *settings.Settings
	// SaveSettings 保存设置。
	SaveSettings func(*settings.Settings) error
	// AfterSaveSettings 在保存后应用副作用（如 launch_at_login → autostart）。
	AfterSaveSettings func(*settings.Settings) error
	// Accounts 是账号管理桥接。
	Accounts AccountBridge
	// Logs 按筛选条件查询日志（logtail）。
	Logs func(ctx context.Context, filter LogFilter, limit int, cursor int64) (LogPage, error)
	// AgentStatus 返回宿主接入信息（配对/连通/最近调用/计划指引）。
	AgentStatus func(ctx context.Context) (AgentView, error)
	// Plugins 返回插件页视图（市场/签名/更新/审计）。
	Plugins func(ctx context.Context) (PluginsView, error)
	// SetDevMode 切换开发者模式。
	SetDevMode func(enabled bool) error
}

// BoundService 是暴露给前端的服务。
type BoundService struct {
	deps Deps
}

// NewBoundService 组装绑定服务。
func NewBoundService(deps Deps) *BoundService {
	return &BoundService{deps: deps}
}

// Status 返回当前状态（序列化为 JSON）。
func (s *BoundService) Status() (json.RawMessage, error) {
	view, err := s.deps.Status(context.Background())
	if err != nil {
		return nil, err
	}
	return json.Marshal(view)
}

// PlanValidate 返回只读预演。
func (s *BoundService) PlanValidate() (json.RawMessage, error) {
	plan, err := s.deps.Plan(context.Background())
	if err != nil {
		return nil, err
	}
	return json.Marshal(plan)
}

// Run 立即执行：先取预演（同一服务链），再执行。
func (s *BoundService) Run() error {
	if _, err := s.deps.Plan(context.Background()); err != nil {
		return err
	}
	return s.deps.Run(context.Background())
}

// SettingsSchema 返回声明式设置 schema（用于生成表单）。
func (s *BoundService) SettingsSchema() (json.RawMessage, error) {
	return json.Marshal(settings.Schema)
}

// SettingsGet 返回当前设置值。
func (s *BoundService) SettingsGet() (json.RawMessage, error) {
	return json.Marshal(s.deps.Settings().Values())
}

// SettingsSet 更新一个设置项并保存，随后应用副作用（如 launch_at_login）。
func (s *BoundService) SettingsSet(key string, value bool) error {
	cur := s.deps.Settings()
	if err := cur.Set(key, value); err != nil {
		return err
	}
	if err := s.deps.SaveSettings(cur); err != nil {
		return err
	}
	if s.deps.AfterSaveSettings != nil {
		return s.deps.AfterSaveSettings(cur)
	}
	return nil
}

// AppsList 返回应用管理页聚合行。
func (s *BoundService) AppsList() (json.RawMessage, error) {
	view, err := s.deps.Status(context.Background())
	if err != nil {
		return nil, err
	}
	return json.Marshal(AggregateApps(view, s.deps.Targets))
}

// AccountsList 返回所有账号（含来源）。
func (s *BoundService) AccountsList() (json.RawMessage, error) {
	list, err := s.deps.Accounts.List(context.Background())
	if err != nil {
		return nil, err
	}
	return json.Marshal(list)
}

// AccountsSetEnabled 逐账号启停。
func (s *BoundService) AccountsSetEnabled(target, accountID string, enabled bool) error {
	return s.deps.Accounts.SetEnabled(context.Background(), target, accountID, enabled)
}

// AccountsDelete 删除 QKA 映射。
func (s *BoundService) AccountsDelete(target, accountID string) error {
	return s.deps.Accounts.Delete(context.Background(), target, accountID)
}

// AccountsPinSource 设置来源优先级。
func (s *BoundService) AccountsPinSource(target, accountID, sourceID string, pinned bool) error {
	return s.deps.Accounts.PinSource(context.Background(), target, accountID, sourceID, pinned)
}

// AccountsDiscover 经适配器发现目标应用账号。
func (s *BoundService) AccountsDiscover(target string) error {
	return s.deps.Accounts.Discover(context.Background(), target)
}

// AccountsBeginLogin 发起官方登录并返回可见状态。
func (s *BoundService) AccountsBeginLogin(target string) (json.RawMessage, error) {
	view, err := s.deps.Accounts.BeginLogin(context.Background(), target)
	if err != nil {
		return nil, err
	}
	return json.Marshal(view)
}

// AccountsLoginStatus 返回当前或最近一次官方登录状态。
func (s *BoundService) AccountsLoginStatus(target string) (json.RawMessage, error) {
	return json.Marshal(s.deps.Accounts.LoginStatus(context.Background(), target))
}

// AccountsCancelLogin 取消进行中的官方登录。
func (s *BoundService) AccountsCancelLogin(target string) (json.RawMessage, error) {
	view, err := s.deps.Accounts.CancelLogin(context.Background(), target)
	if err != nil {
		return nil, err
	}
	return json.Marshal(view)
}

// AccountsImportSource 导入只读来源。
func (s *BoundService) AccountsImportSource(target, accountID, sourceID string) error {
	return s.deps.Accounts.ImportSource(context.Background(), target, accountID, sourceID)
}

// LogsTail 按四类筛选查询日志（有界、游标）。
func (s *BoundService) LogsTail(target, accountID, operation, state string,
	limit int, cursor int64) (json.RawMessage, error) {
	if s.deps.Logs == nil {
		return nil, errNotWired
	}
	page, err := s.deps.Logs(context.Background(),
		LogFilter{Target: target, AccountID: accountID, Operation: operation, State: state},
		limit, cursor)
	if err != nil {
		return nil, err
	}
	return json.Marshal(page)
}

// AgentStatus 返回宿主接入信息。
func (s *BoundService) AgentStatus() (json.RawMessage, error) {
	if s.deps.AgentStatus == nil {
		return nil, errNotWired
	}
	view, err := s.deps.AgentStatus(context.Background())
	if err != nil {
		return nil, err
	}
	return json.Marshal(view)
}

// PluginsList 返回插件页视图（市场条目、版本、签名、更新状态与审计）。
func (s *BoundService) PluginsList() (json.RawMessage, error) {
	if s.deps.Plugins == nil {
		return nil, errNotWired
	}
	view, err := s.deps.Plugins(context.Background())
	if err != nil {
		return nil, err
	}
	return json.Marshal(view)
}

// SetDevMode 切换开发者模式。
func (s *BoundService) SetDevMode(enabled bool) error {
	if s.deps.SetDevMode == nil {
		return errNotWired
	}
	return s.deps.SetDevMode(enabled)
}
