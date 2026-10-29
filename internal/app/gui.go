package app

import (
	"context"
	"os"
	"os/exec"
	"sync"

	"qka/internal/autostart"
	"qka/internal/gui"
	"qka/internal/install"
	"qka/internal/login"
	"qka/internal/logtail"
	"qka/internal/settings"
)

// targetInfo 是单个目标应用的开发态目录（基址与官方登录入口）。
// 与 adapters/* 中的 example.com 基址一致，用于受控假目标；真实站点由发布配置覆盖。
type targetInfo struct {
	baseURL  string
	loginURL string
}

var targetCatalog = map[string]targetInfo{
	"workbuddy-cn": {"https://workbuddy.example.com", "https://workbuddy.example.com/login"},
	"qoder-cn":     {"https://qoder.example.com", "https://qoder.example.com/login"},
	"qoder":        {"https://qoder-intl.example.com", "https://qoder-intl.example.com/login"},
	"doubao-work":  {"https://work.doubao.example.com", "https://work.doubao.example.com/login"},
}

// targetOrder 是 V1 目标的推进/展示顺序（与设计文档 §V1 一致）。
var targetOrder = []string{"workbuddy-cn", "qoder-cn", "qoder", "doubao-work"}

// loginEntries 实现 gui.LoginEntries：返回目标的官方登录入口。
type loginEntries struct{}

func (loginEntries) Entry(target string) (login.Entry, bool) {
	info, ok := targetCatalog[target]
	if !ok {
		return login.Entry{}, false
	}
	return login.Entry{
		Target:     target,
		Kind:       login.KindHTTPS,
		Location:   info.loginURL,
		Purpose:    "建立 QKA 受管登录来源（只读校验，不接收密码/验证码）",
		TimeoutSec: 120,
	}, true
}

// guiState 持有 GUI 运行期可变状态（设置、开发者模式、受限加载审计）。
type guiState struct {
	app *App

	mu       sync.Mutex
	settings *settings.Settings
	login    *login.Service
	devMode  bool
	audit    []gui.AuditLine
}

// GUI 装配并返回菜单栏 GUI（托盘 + 吸附面板 + 五页签主窗口）。
// 调用方负责通过 Run() 进入事件循环。
func (a *App) GUI() *gui.App {
	st, err := settings.Load(a.cfg.ConfigDir)
	if err != nil {
		st = settings.Default()
	}

	// 登录服务的官方域名白名单（与目标目录一致）。
	var allowed []string
	for _, info := range targetCatalog {
		allowed = append(allowed, hostOf(info.loginURL))
	}

	state := &guiState{
		app:      a,
		settings: st,
		login:    login.NewService(allowed),
		audit:    []gui.AuditLine{},
	}

	bridge := gui.NewAccountBridge(
		a.accounts,
		state.login,
		a, // app 实现 gui.HostDiscoverer
		loginEntries{},
		openURL,
	)

	deps := gui.Deps{
		Targets:           targetOrder,
		Status:            a.status.Status,
		Plan:              a.status.PlanValidate,
		Run:               a.RunAll,
		Settings:          state.currentSettings,
		SaveSettings:      state.saveSettings,
		AfterSaveSettings: state.afterSaveSettings,
		Accounts:          bridge,
		Logs:              state.logs,
		AgentStatus:       state.agentStatus,
		Plugins:           state.plugins,
		SetDevMode:        state.setDevMode,
	}
	return gui.New(deps)
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
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if st.Get("launch_at_login") {
		return autostart.On(home, exe, autostart.DefaultLabel)
	}
	return autostart.Off(home, autostart.DefaultLabel)
}

func (s *guiState) logs(ctx context.Context, filter gui.LogFilter, limit int, cursor int64) (gui.LogPage, error) {
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

// openURL 用系统默认浏览器打开官方登录页。
func openURL(raw string) error {
	return exec.Command("open", raw).Run()
}

func hostOf(rawURL string) string {
	const prefix = "https://"
	rest := rawURL
	if len(rawURL) > len(prefix) && rawURL[:len(prefix)] == prefix {
		rest = rawURL[len(prefix):]
	}
	for i := 0; i < len(rest); i++ {
		if rest[i] == '/' {
			return rest[:i]
		}
	}
	return rest
}
