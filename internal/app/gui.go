package app

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"qka/internal/autostart"
	"qka/internal/capabilities"
	"qka/internal/feedback"
	"qka/internal/gui"
	"qka/internal/install"
	"qka/internal/logtail"
	"qka/internal/openurl"
	"qka/internal/providers"
	"qka/internal/runlock"
	"qka/internal/schedule"
	"qka/internal/scheduler"
	"qka/internal/settings"
	"qka/internal/tokenspeed"
	"qka/internal/workbuddy"
)

// targetOrder 是 MVP 当前实际接通的目标应用。
// 其余 V1 目标在适配器、账号和验收闭环完成前不在界面中展示。
var targetOrder = []string{"workbuddy-cn"}

var (
	appExecutable = os.Executable
	userHomeDir   = os.UserHomeDir
	openBrowser   = openurl.Open
)

// guiState 持有 GUI 运行期可变状态（设置、开发者模式、受限加载审计）。
type guiState struct {
	app *App

	mu       sync.Mutex
	settings *settings.Settings
	devMode  bool
	audit    []gui.AuditLine
}

// GUI 装配并返回菜单栏 GUI（托盘 + 吸附面板 + 五页签主窗口）。
// 调用方负责通过 Run() 进入事件循环。
func (a *App) GUI() *gui.App {
	return gui.New(a.guiDeps())
}

func (a *App) guiDeps() gui.Deps {
	st, err := settings.Load(a.cfg.ConfigDir)
	if err != nil {
		st = settings.Default()
	}

	state := &guiState{
		app:      a,
		settings: st,
		audit:    []gui.AuditLine{},
	}
	exe, _ := appExecutable()
	scheduleManager := schedule.NewManager(a.cfg.ConfigDir, exe, scheduler.New(), a.now)
	tokenSpeed := tokenspeed.New(tokenspeed.DefaultConfig())
	tokenSpeed.SetTitleTheme(st.GetString("title_theme"))
	updateService := a.updateService()
	feedbackService := feedback.New(a.cfg.Version)
	feedbackService.Logs = state.feedbackLogs
	// 设置页允许填自定义反馈地址；每次提交都重新读一次，改完立即生效。
	feedbackService.EndpointProvider = func() string {
		return st.GetString("feedback_endpoint")
	}

	bridge := gui.NewAccountBridge(
		a.accounts,
		a, // app 实现 gui.HostDiscoverer
		a.workBuddyLoginService(),
	).WithRuntime(a.status.Status, a).WithTaskCapabilities(a.capabilitiesFor)

	deps := gui.Deps{
		CurrentVersion: a.cfg.Version,
		Targets:        targetOrder,
		Status:         a.status.Status,
		Plan:           a.status.PlanValidate,
		Run:            a.RunAll,
		Settings:       state.currentSettings,
		SaveSettings:   state.saveSettings,
		AfterSaveSettings: func(st *settings.Settings) error {
			tokenSpeed.SetTitleTheme(st.GetString("title_theme"))
			return state.afterSaveSettings(st)
		},
		ScheduleGet: scheduleManager.Get,
		ScheduleSetEnabled: func(ctx context.Context, enabled bool) (schedule.View, error) {
			return scheduleManager.SetEnabled(ctx, enabled)
		},
		ScheduleSetConfig: scheduleManager.SetConfig,
		UpdateCheck:       updateService.Check,
		UpdateInstall:     updateService.DownloadAndApply,
		QuitApp: func() {
			if app := application.Get(); app != nil {
				app.Quit()
			}
		},
		TokenSpeedGet: func(context.Context) (tokenspeed.Snapshot, error) {
			return tokenSpeed.Snapshot(), nil
		},
		TokenSpeed:    tokenSpeed,
		Accounts:      bridge,
		ProvidersList: a.providers.List,
		ProviderBeginLogin: func(ctx context.Context, provider string, method int, inputs map[string]string) (providers.LoginView, error) {
			view, err := a.providers.BeginLogin(ctx, provider, method, inputs)
			if err == nil && view.Status == "authorized" && view.URL != "" {
				_ = openBrowser(view.URL)
			}
			return view, err
		},
		ProviderFinishLogin:       a.providers.FinishLogin,
		ProviderLoginAPIKey:       a.providers.LoginAPIKey,
		ProviderSetEnabled:        a.providers.SetEnabled,
		ProviderSetAccountEnabled: a.providers.SetAccountEnabled,
		ProviderSetTaskEnabled:    a.providers.SetTaskEnabled,
		ProviderRun: func(ctx context.Context, provider, account, operation string) error {
			return a.RunProviderAccount(ctx, provider, account, operation)
		},
		ProviderTestModel:     a.providers.TestModel,
		ProviderDeleteAccount: a.providers.DeleteAccount,
		ProviderHistory:       a.providers.History,
		Feedback:              feedbackService,
		Logs:                  state.logs,
		AgentStatus:           state.agentStatus,
		Plugins:               state.plugins,
		SetDevMode:            state.setDevMode,
	}
	return deps
}

func (s *guiState) feedbackLogs(ctx context.Context) ([]feedback.LogEntry, error) {
	if s == nil || s.app == nil || s.app.store == nil || s.app.store.DB() == nil {
		return nil, nil
	}
	now := time.Now()
	if s.app.now != nil {
		now = s.app.now()
	}
	cutoff := now.Add(-logtail.Retention).UTC().Format(time.RFC3339)
	rows, err := s.app.store.DB().QueryContext(ctx, `
SELECT ts, target, account_id, operation, state, level, message
FROM logs
WHERE ts >= ? AND (
  state IN ('failed_retryable', 'failed_final', 'manual_required')
  OR lower(level) IN ('error', 'warn', 'warning')
)
ORDER BY id DESC
LIMIT 80`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := make([]feedback.LogEntry, 0, 80)
	for rows.Next() {
		var entry feedback.LogEntry
		if err := rows.Scan(&entry.TS, &entry.Target, &entry.AccountID,
			&entry.Operation, &entry.State, &entry.Level, &entry.Message); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	for left, right := 0, len(entries)-1; left < right; left, right = left+1, right-1 {
		entries[left], entries[right] = entries[right], entries[left]
	}
	// 行迭代错误随结果一并返回：收敛正常时 rows.Err() 为 nil，无需一段永远
	// 走不到的 if 分支；调用方仍按 err 判断。
	return entries, rows.Err()
}

// RunProviderAccount sends one provider action through the config-directory
// lock so manual and scheduled execution cannot overlap.
func (a *App) RunProviderAccount(
	ctx context.Context, provider, account, operation string,
) error {
	unlock, err := a.acquireRunLock()
	if err != nil {
		return err
	}
	defer unlock()
	return a.providers.Run(ctx, provider, account, operation)
}

func (a *App) workBuddyLoginService() *workbuddy.LoginService {
	baseURL := workBuddyLoginBaseURL(a.cfg.BaseURLs)
	pendingStore := a.pendingCredentials
	if pendingStore == nil {
		pendingStore = capabilities.NewLoginCredentialStore(
			capabilities.NewKeychainStore(),
			capabilities.NewFileSecretStore(a.cfg.ConfigDir),
		)
	}
	credentials := a.credentials
	if credentials == nil {
		credentials = capabilities.NewCredentialService(
			capabilities.NewKeychainStore(),
			capabilities.NewFileSecretStore(a.cfg.ConfigDir),
			capabilities.Grant{
				"credentials.managed": true,
				"credentials.resolve": true,
			},
			a.now,
		)
	}
	verifier := workbuddy.NewVerificationService(workbuddy.VerificationConfig{
		BaseURL:      baseURL,
		PendingStore: pendingStore,
		Sink:         newWorkBuddyVerificationSink(a.accounts, credentials),
	})
	return workbuddy.NewLoginService(workbuddy.LoginConfig{
		BaseURL:      baseURL,
		PendingStore: pendingStore,
		Verifier:     verifier,
		OpenBrowser:  openBrowser,
		Now:          a.now,
	})
}

// RunAccount 实现 gui.AccountRunner：复用宿主调用的同一单账号执行链。
func (a *App) RunAccount(ctx context.Context, target, accountID, operation string) error {
	unlock, err := a.acquireRunLock()
	if err != nil {
		return err
	}
	defer unlock()

	if target == "workbuddy-cn" && operation == "refresh" {
		return a.refreshWorkBuddyUsage(ctx, accountID)
	}
	_, err = a.runOperation(ctx, target, accountID, operation)
	return err
}

func (a *App) acquireRunLock() (func(), error) {
	if a.cfg.ConfigDir == "" {
		return func() {}, nil
	}
	lock, err := runlock.Acquire(a.cfg.ConfigDir)
	if err != nil {
		return nil, err
	}
	return func() { _ = lock.Close() }, nil
}

func workBuddyLoginBaseURL(overrides map[string]string) string {
	if baseURL := overrides["workbuddy-cn"]; baseURL != "" {
		return baseURL
	}
	return workbuddy.DefaultBaseURL
}

func (s *guiState) currentSettings() *settings.Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings
}

func (s *guiState) saveSettings(st *settings.Settings) error {
	if err := st.Save(s.app.cfg.ConfigDir); err != nil {
		return err
	}
	s.mu.Lock()
	s.settings = st
	s.mu.Unlock()
	return nil
}

// afterSaveSettings 应用设置副作用：launch_at_login → LaunchAgent 幂等 on/off。
func (s *guiState) afterSaveSettings(st *settings.Settings) error {
	exe, err := appExecutable()
	if err != nil {
		return err
	}
	home, err := userHomeDir()
	if err != nil {
		return err
	}
	if st.Get("launch_at_login") {
		return autostart.On(home, exe, autostart.DefaultLabel)
	}
	return autostart.Off(home, autostart.DefaultLabel)
}

func (s *guiState) logs(ctx context.Context, filter gui.LogFilter, limit int, cursor int64) (gui.LogPage, error) {
	s.app.pruneLogs(ctx)
	page, err := logtail.Tail(ctx, s.app.store.DB(), logtail.Filter{
		Target:    filter.Target,
		AccountID: filter.AccountID,
		Operation: filter.Operation,
		State:     filter.State,
	}, limit, cursor)
	if err != nil {
		return gui.LogPage{}, err
	}

	out := gui.LogPage{Entries: []gui.LogEntry{}, NextCursor: page.NextCursor}
	for _, e := range page.Entries {
		out.Entries = append(out.Entries, gui.LogEntry{
			ID: e.ID, TS: e.TS, Target: e.Target, AccountID: e.AccountID,
			Operation: e.Operation, State: e.State, Level: e.Level, Message: e.Message,
		})
	}
	return out, nil
}

func (s *guiState) agentStatus(context.Context) (gui.AgentView, error) {
	rows := make([]gui.HostRow, 0, 5)
	for _, p := range install.Profiles() {
		rows = append(rows, gui.HostRow{
			HostID:        p.HostID,
			Label:         p.Label,
			Paired:        false, // 新安装尚无宿主配对；配对由安装 skill 写入后反映
			Connected:     false,
			ScheduleGuide: p.ScheduleGuide,
		})
	}
	return gui.NewAgentView(rows), nil
}

func (s *guiState) plugins(context.Context) (gui.PluginsView, error) {
	rows := make([]gui.PluginRow, 0, len(s.app.loaded))
	for _, l := range s.app.loaded {
		rows = append(rows, gui.PluginRow{
			ID:      l.Manifest.ID,
			Name:    l.Manifest.Name,
			Version: l.Manifest.Version,
			Source:  "local",
			Signed:  false, // 随包开发适配器未签名，固定标注未验证
		})
	}

	s.mu.Lock()
	devMode := s.devMode
	audit := append([]gui.AuditLine{}, s.audit...)
	s.mu.Unlock()

	return gui.NewPluginsView(rows, devMode, audit), nil
}

func (s *guiState) setDevMode(enabled bool) error {
	s.mu.Lock()
	s.devMode = enabled
	s.mu.Unlock()
	return nil
}
