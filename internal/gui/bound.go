package gui

import (
	"context"
	"encoding/json"

	"qka/internal/settings"
	"qka/internal/status"
)

// Deps 是 GUI 所需的后端能力（由应用装配注入）。
type Deps struct {
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
	// Accounts 是账号管理桥接。
	Accounts AccountBridge
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

// SettingsSet 更新一个设置项并保存。
func (s *BoundService) SettingsSet(key string, value bool) error {
	cur := s.deps.Settings()
	if err := cur.Set(key, value); err != nil {
		return err
	}
	return s.deps.SaveSettings(cur)
}

// AppsList 返回应用管理页聚合行。
func (s *BoundService) AppsList() (json.RawMessage, error) {
	view, err := s.deps.Status(context.Background())
	if err != nil {
		return nil, err
	}
	return json.Marshal(AggregateApps(view))
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

// AccountsDelete 删除 qka 映射。
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

// AccountsBeginLogin 发起官方登录。
func (s *BoundService) AccountsBeginLogin(target string) error {
	return s.deps.Accounts.BeginLogin(context.Background(), target)
}

// AccountsImportSource 导入只读来源。
func (s *BoundService) AccountsImportSource(target, accountID, sourceID string) error {
	return s.deps.Accounts.ImportSource(context.Background(), target, accountID, sourceID)
}
