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
