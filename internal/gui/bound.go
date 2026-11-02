package gui

import (
	"context"
	"encoding/json"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"qka/internal/features"
	"qka/internal/feedback"
	"qka/internal/providers"
	"qka/internal/schedule"
	"qka/internal/selfupdate"
	"qka/internal/settings"
	"qka/internal/status"
	"qka/internal/tokenspeed"
)

// Deps 是 GUI 所需的后端能力（由应用装配注入）。
type Deps struct {
	// CurrentVersion is the running application version shown by the updater.
	CurrentVersion string
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
	// DockIconVisible 在 macOS 上即时显示或隐藏 Dock 图标。
	DockIconVisible func(bool)
	// ScheduleGet 返回 QKA 内置周期自动执行与系统调度状态。
	ScheduleGet func(ctx context.Context) (schedule.View, error)
	// ScheduleSetEnabled 安装或移除系统调度，并与 schedule.json 同步。
	ScheduleSetEnabled func(ctx context.Context, enabled bool) (schedule.View, error)
	// ScheduleSetConfig 更新自动执行间隔与执行动作。
	ScheduleSetConfig func(ctx context.Context, intervalMinutes int, action string) (schedule.View, error)
	// UpdateCheck reads the official feed without downloading an artifact.
	UpdateCheck func(ctx context.Context) (selfupdate.Status, error)
	// UpdateInstall downloads, verifies and starts the platform update handoff.
	UpdateInstall func(ctx context.Context) (selfupdate.Status, error)
	// QuitApp exits QKA after the update helper can safely replace the bundle.
	QuitApp func()
	// TokenSpeedGet 返回本机 Agent 日志的实时 token 吞吐快照。
	TokenSpeedGet func(ctx context.Context) (tokenspeed.Snapshot, error)
	// TokenSpeed 是本机 Agent 日志速度服务；为空时不启动菜单栏实时标签。
	TokenSpeed *tokenspeed.Service
	// Accounts 是账号管理桥接。
	Accounts AccountBridge
	// Providers 是随包供应商插件的统一管理投影。
	ProvidersList func(ctx context.Context) (providers.View, error)
	// ProviderBeginLogin starts or advances a provider login.
	ProviderBeginLogin func(ctx context.Context, provider string, method int, inputs map[string]string) (providers.LoginView, error)
	// ProviderFinishLogin completes an OAuth callback.
	ProviderFinishLogin func(ctx context.Context, session, code string) (providers.LoginView, error)
	// ProviderLoginAPIKey stores an API key through the provider plugin.
	ProviderLoginAPIKey       func(ctx context.Context, provider string, method int, inputs map[string]string, key string) (providers.LoginView, error)
	ProviderSetEnabled        func(ctx context.Context, provider string, enabled bool) error
	ProviderSetAccountEnabled func(ctx context.Context, provider, account string, enabled bool) error
	ProviderSetTaskEnabled    func(ctx context.Context, provider, account, operation string, enabled bool) error
	ProviderRun               func(ctx context.Context, provider, account, operation string) error
	ProviderTestModel         func(ctx context.Context, provider, account, model string) (providers.ModelTestView, error)
	ProviderDeleteAccount     func(ctx context.Context, provider, account string) error
	ProviderHistory           func(ctx context.Context, provider, account string, limit int) ([]providers.RunItemView, []providers.QuotaReadingView, error)
	// Feedback is the privacy-safe GitHub issue submission service.
	Feedback *feedback.Service
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
	values := s.deps.Settings().Values()
	// This metadata key is not in the settings schema, so the generic form
	// renderer ignores it while the updater can still show the running version.
	values["__qka_version"] = s.deps.CurrentVersion
	return json.Marshal(values)
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
	var sideEffectErr error
	if s.deps.AfterSaveSettings != nil {
		sideEffectErr = s.deps.AfterSaveSettings(cur)
	}
	if key == "show_dock_icon" && s.deps.DockIconVisible != nil {
		s.deps.DockIconVisible(value)
	}
	return sideEffectErr
}

// SettingsSetString 更新一个有限选项设置并保存，随后广播外观变化。
func (s *BoundService) SettingsSetString(key, value string) error {
	cur := s.deps.Settings()
	if err := cur.SetString(key, value); err != nil {
		return err
	}
	if err := s.deps.SaveSettings(cur); err != nil {
		return err
	}
	if s.deps.AfterSaveSettings != nil {
		if err := s.deps.AfterSaveSettings(cur); err != nil {
			return err
		}
	}
	if key == "theme" {
		if app := application.Get(); app != nil {
			app.Event.Emit("theme-changed", value)
		}
	}
	if key == "background" || key == "glass" {
		if app := application.Get(); app != nil {
			app.Event.Emit("appearance-changed", map[string]string{"key": key, "value": value})
		}
	}
	if key == "title_theme" {
		if app := application.Get(); app != nil {
			app.Event.Emit("title-theme-changed", value)
		}
		if s.deps.TokenSpeedGet != nil {
			if snapshot, err := s.deps.TokenSpeedGet(context.Background()); err == nil {
				if app := application.Get(); app != nil {
					app.Event.Emit("tokenspeed-changed", snapshot)
				}
			}
		}
	}
	return nil
}

// ScheduleGet 返回周期自动执行的启用状态、间隔与最近结果。
func (s *BoundService) ScheduleGet() (json.RawMessage, error) {
	if s.deps.ScheduleGet == nil {
		return nil, errNotWired
	}
	view, err := s.deps.ScheduleGet(context.Background())
	if err != nil {
		return nil, err
	}
	return json.Marshal(view)
}

// ScheduleSetEnabled 同步安装或删除系统调度，并返回最新状态。
func (s *BoundService) ScheduleSetEnabled(enabled bool) (json.RawMessage, error) {
	if s.deps.ScheduleSetEnabled == nil {
		return nil, errNotWired
	}
	view, err := s.deps.ScheduleSetEnabled(context.Background(), enabled)
	if err != nil {
		return nil, err
	}
	return json.Marshal(view)
}

// ScheduleSetConfig updates the interval and action and reinstalls the native
// task when auto-run is enabled.
func (s *BoundService) ScheduleSetConfig(
	intervalMinutes int, action string,
) (json.RawMessage, error) {
	if s.deps.ScheduleSetConfig == nil {
		return nil, errNotWired
	}
	view, err := s.deps.ScheduleSetConfig(context.Background(), intervalMinutes, action)
	if err != nil {
		return nil, err
	}
	return json.Marshal(view)
}

// UpdateCheck checks the official update feed without downloading an artifact.
func (s *BoundService) UpdateCheck() (json.RawMessage, error) {
	if s.deps.UpdateCheck == nil {
		return nil, errNotWired
	}
	status, err := s.deps.UpdateCheck(context.Background())
	if err != nil {
		return nil, err
	}
	return json.Marshal(status)
}

// UpdateInstall downloads, verifies and starts the platform update handoff.
func (s *BoundService) UpdateInstall() (json.RawMessage, error) {
	if s.deps.UpdateInstall == nil {
		return nil, errNotWired
	}
	status, err := s.deps.UpdateInstall(context.Background())
	if err != nil {
		return nil, err
	}
	if s.deps.QuitApp != nil {
		go func() {
			time.Sleep(600 * time.Millisecond)
			s.deps.QuitApp()
		}()
	}
	return json.Marshal(status)
}

// TokenSpeedGet 返回本机 Agent 日志的实时 token 吞吐快照。
func (s *BoundService) TokenSpeedGet() (json.RawMessage, error) {
	if s.deps.TokenSpeedGet == nil {
		return nil, errNotWired
	}
	snapshot, err := s.deps.TokenSpeedGet(context.Background())
	if err != nil {
		return nil, err
	}
	return json.Marshal(snapshot)
}

// AppsList 返回应用管理页聚合行。
func (s *BoundService) AppsList() (json.RawMessage, error) {
	view, err := s.deps.Status(context.Background())
	if err != nil {
		return nil, err
	}
	return json.Marshal(AggregateApps(view, s.deps.Targets))
}

// ProvidersList returns the bundled provider plugins and their accounts.
func (s *BoundService) ProvidersList() (json.RawMessage, error) {
	if s.deps.ProvidersList == nil {
		return nil, errNotWired
	}
	view, err := s.deps.ProvidersList(context.Background())
	if err != nil {
		return nil, err
	}
	return json.Marshal(view)
}

// ProvidersBeginLogin starts or advances a provider sign-in.
func (s *BoundService) ProvidersBeginLogin(
	provider string, method int, inputs map[string]string,
) (json.RawMessage, error) {
	if s.deps.ProviderBeginLogin == nil {
		return nil, errNotWired
	}
	view, err := s.deps.ProviderBeginLogin(context.Background(), provider, method, inputs)
	if err != nil {
		return nil, err
	}
	return json.Marshal(view)
}

// ProvidersFinishLogin completes OAuth or desktop sign-in.
func (s *BoundService) ProvidersFinishLogin(session, code string) (json.RawMessage, error) {
	if s.deps.ProviderFinishLogin == nil {
		return nil, errNotWired
	}
	view, err := s.deps.ProviderFinishLogin(context.Background(), session, code)
	if err != nil {
		return nil, err
	}
	return json.Marshal(view)
}

// ProvidersLoginAPIKey stores an API-key sign-in.
func (s *BoundService) ProvidersLoginAPIKey(
	provider string, method int, inputs map[string]string, key string,
) (json.RawMessage, error) {
	if s.deps.ProviderLoginAPIKey == nil {
		return nil, errNotWired
	}
	view, err := s.deps.ProviderLoginAPIKey(context.Background(), provider, method, inputs, key)
	if err != nil {
		return nil, err
	}
	return json.Marshal(view)
}

// ProvidersSetEnabled toggles one provider.
func (s *BoundService) ProvidersSetEnabled(provider string, enabled bool) error {
	if s.deps.ProviderSetEnabled == nil {
		return errNotWired
	}
	return s.deps.ProviderSetEnabled(context.Background(), provider, enabled)
}

// ProvidersSetAccountEnabled toggles one provider account.
func (s *BoundService) ProvidersSetAccountEnabled(provider, account string, enabled bool) error {
	if s.deps.ProviderSetAccountEnabled == nil {
		return errNotWired
	}
	return s.deps.ProviderSetAccountEnabled(context.Background(), provider, account, enabled)
}

// ProvidersSetTaskEnabled toggles check-in automation.
func (s *BoundService) ProvidersSetTaskEnabled(
	provider, account, operation string, enabled bool,
) error {
	if s.deps.ProviderSetTaskEnabled == nil {
		return errNotWired
	}
	if !features.OperationAvailable(operation) {
		return features.ErrKeepaliveDisabled
	}
	return s.deps.ProviderSetTaskEnabled(context.Background(), provider, account, operation, enabled)
}

// ProvidersRun executes one provider account action.
func (s *BoundService) ProvidersRun(provider, account, operation string) error {
	if s.deps.ProviderRun == nil {
		return errNotWired
	}
	switch operation {
	case "checkin", "refresh":
	default:
		if !features.OperationAvailable(operation) {
			return features.ErrKeepaliveDisabled
		}
		return errUnsupportedAccountAction
	}
	return s.deps.ProviderRun(context.Background(), provider, account, operation)
}

// ProvidersTestModel checks one provider model through its signed-in account.
func (s *BoundService) ProvidersTestModel(
	provider, account, model string,
) (json.RawMessage, error) {
	if s.deps.ProviderTestModel == nil {
		return nil, errNotWired
	}
	result, err := s.deps.ProviderTestModel(
		context.Background(), provider, account, model)
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}

// ProvidersDeleteAccount removes QKA's provider account and credentials.
func (s *BoundService) ProvidersDeleteAccount(provider, account string) error {
	if s.deps.ProviderDeleteAccount == nil {
		return errNotWired
	}
	return s.deps.ProviderDeleteAccount(context.Background(), provider, account)
}

// ProvidersHistory returns recent runs and quota observations.
func (s *BoundService) ProvidersHistory(
	provider, account string, limit int,
) (json.RawMessage, error) {
	if s.deps.ProviderHistory == nil {
		return nil, errNotWired
	}
	items, readings, err := s.deps.ProviderHistory(
		context.Background(), provider, account, limit)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"items": items, "quota": readings,
	})
}

// FeedbackContext returns the redacted recent-log preview used by the report
// dialog. It never returns credentials or full account identifiers.
func (s *BoundService) FeedbackContext() (json.RawMessage, error) {
	if s.deps.Feedback == nil {
		return nil, errNotWired
	}
	view, err := s.deps.Feedback.Preview(context.Background())
	if err != nil {
		return nil, err
	}
	return json.Marshal(view)
}

// FeedbackSubmit sends a redacted bug report through the Cloudflare proxy.
func (s *BoundService) FeedbackSubmit(
	description string, images []feedback.Image, includeLogs bool,
) (json.RawMessage, error) {
	if s.deps.Feedback == nil {
		return nil, errNotWired
	}
	result, err := s.deps.Feedback.Submit(context.Background(), feedback.Request{
		Description: description,
		Images:      images,
		IncludeLogs: includeLogs,
	})
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}

// AccountsList 返回所有账号（含来源）。
func (s *BoundService) AccountsList() (json.RawMessage, error) {
	list, err := s.deps.Accounts.List(context.Background())
	if err != nil {
		return nil, err
	}
	return json.Marshal(list)
}

// AccountsRun 对单个账号执行签到、只读复查或额度刷新。
func (s *BoundService) AccountsRun(target, accountID, operation string) error {
	switch operation {
	case "checkin", "refresh":
	default:
		if !features.OperationAvailable(operation) {
			return features.ErrKeepaliveDisabled
		}
		return errUnsupportedAccountAction
	}
	return s.deps.Accounts.Run(context.Background(), target, accountID, operation)
}

// AccountsSetTaskEnabled 设置自动执行是否包含该账号的某一任务。
func (s *BoundService) AccountsSetTaskEnabled(
	target, accountID, operation string,
	enabled bool,
) error {
	if !features.OperationAvailable(operation) {
		return features.ErrKeepaliveDisabled
	}
	return s.deps.Accounts.SetTaskEnabled(
		context.Background(), target, accountID, operation, enabled)
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
