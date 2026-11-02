// Package gui 组装 QKA 的菜单栏托盘、面板与主窗口（Wails v3）。
package gui

import (
	"context"
	"embed"
	"io/fs"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"qka/internal/status"
	"qka/internal/tokenspeed"
)

//go:embed frontend
var frontendFS embed.FS

// App 持有 Wails 应用与运行期窗口。
type App struct {
	wails      *application.App
	tray       *application.SystemTray
	panel      *application.WebviewWindow
	main       *application.WebviewWindow
	theme      string
	tokenSpeed *tokenspeed.Service
	status     func(context.Context) (status.View, error)
	windowMu   sync.Mutex
	startedAt  atomic.Int64
	dockShow   atomic.Bool
	failureOn  atomic.Bool
	dockApply  func(bool)
}

const panelGap = 8

func assetHandler() http.Handler {
	sub, _ := fs.Sub(frontendFS, "frontend")
	return http.FileServer(http.FS(sub))
}

// activationPolicy 把 show_dock_icon 设置映射为 macOS 激活策略：
// 开启显示 Dock 图标（regular），关闭后仅在菜单栏驻留（accessory）。
func activationPolicy(showDockIcon bool) application.ActivationPolicy {
	if showDockIcon {
		return application.ActivationPolicyRegular
	}
	return application.ActivationPolicyAccessory
}

// New 创建 Wails 应用：根据 show_dock_icon 选择激活策略（默认 regular 显示 Dock 图标），
// 组装托盘、面板与五页签主窗口。
func New(deps Deps) *App {
	showDock := true
	theme := "system"
	if deps.Settings != nil {
		current := deps.Settings()
		showDock = current.Get("show_dock_icon")
		theme = current.GetString("theme")
	}

	dockApply := deps.DockIconVisible
	if dockApply == nil {
		dockApply = setDockIconVisible
	}
	result := &App{
		theme: theme, tokenSpeed: deps.TokenSpeed,
		status: deps.Status, dockApply: dockApply,
	}
	result.dockShow.Store(showDock)
	deps.DockIconVisible = result.setDockPreference
	bound := NewBoundService(deps)

	wails := application.New(application.Options{
		Name: "Quota Keepalive",
		Icon: applicationIcon(256),
		Services: []application.Service{
			application.NewService(bound),
		},
		Assets: application.AssetOptions{
			Handler: assetHandler(),
		},
		Mac: application.MacOptions{
			ActivationPolicy: activationPolicy(showDock),
		},
	})

	result.wails = wails

	// 菜单栏面板的设置入口统一回到主窗口的第五页签。
	wails.Event.On("open-settings", func(*application.CustomEvent) {
		result.showMain()
	})
	wails.Event.On("open-checkin", func(*application.CustomEvent) {
		result.showMain()
	})

	// 面板「打开主窗口」入口。
	wails.Event.On("open-main", func(*application.CustomEvent) {
		result.showMain()
	})
	wails.Event.On("quit", func(*application.CustomEvent) {
		wails.Quit()
	})
	wails.Event.On("collapse-panel", func(*application.CustomEvent) {
		result.hidePanel()
	})

	// 测试钩子：QKA_DEBUG_SHOW=main/settings/both 时，事件循环启动后显示对应窗口（不影响正式行为）。
	if debugShow := os.Getenv("QKA_DEBUG_SHOW"); debugShow != "" {
		go func() {
			time.Sleep(750 * time.Millisecond)
			switch debugShow {
			case "panel":
				result.showPanel()
			case "main":
				result.showMain()
			case "settings":
				result.showMain()
				wails.Event.Emit("open-settings")
			case "both":
				result.showMain()
			}
		}()
	}

	// 托盘菜单。
	menu := application.NewMenu()
	menu.Add("打开主窗口").OnClick(func(*application.Context) {
		result.showMain()
	})
	menu.Add("设置").OnClick(func(*application.Context) {
		result.showMain()
		wails.Event.Emit("open-settings")
	})
	menu.AddSeparator()
	menu.Add("退出").OnClick(func(*application.Context) {
		wails.Quit()
	})

	// 托盘：模板图标（深浅色自动着色）、吸附面板（点击切换、失焦即隐）、菜单。
	tray := wails.SystemTray.New()
	tray.SetTemplateIcon(templateIcon())
	tray.SetTooltip("Quota Keepalive")
	tray.SetLabel("0 tok/s")
	tray.OnClick(result.togglePanel)
	tray.SetMenu(menu)

	result.tray = tray
	result.installPlatformWindowBehavior()
	wails.Event.OnApplicationEvent(events.Common.ApplicationStarted, result.onApplicationStarted)
	return result
}

func (a *App) onApplicationStarted(*application.ApplicationEvent) {
	a.startedAt.Store(time.Now().UnixNano())
}

func (a *App) setDockPreference(show bool) {
	a.dockShow.Store(show)
	a.dockApply(show)
}

func (a *App) hideDockForMenuBar() {
	a.dockApply(false)
}

func (a *App) hideRestoredWindows() {
	a.hidePanel()
	a.windowMu.Lock()
	main := a.main
	a.windowMu.Unlock()
	if main != nil {
		main.Hide()
	}
}

func (a *App) ensurePanel() *application.WebviewWindow {
	a.windowMu.Lock()
	defer a.windowMu.Unlock()
	if a.panel != nil {
		return a.panel
	}
	// The panel is created on first use. Pre-creating a hidden macOS window
	// lets AppKit/Wails restore it during an application reopen event.
	a.panel = a.wails.Window.NewWithOptions(panelOptions(a.theme))
	a.tray.AttachWindow(a.panel).WindowOffset(panelGap)
	return a.panel
}

func (a *App) ensureMainWindow() *application.WebviewWindow {
	a.windowMu.Lock()
	defer a.windowMu.Unlock()
	return a.ensureMainWindowLocked()
}

func (a *App) ensureMainWindowLocked() *application.WebviewWindow {
	if a.main != nil {
		return a.main
	}
	a.main = a.wails.Window.NewWithOptions(mainOptions(a.theme))
	a.main.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		a.main.Hide()
		// Closing the main window leaves QKA resident in the menu bar, so the
		// Dock icon should disappear until the user opens the main window again.
		a.hideDockForMenuBar()
		event.Cancel()
	})
	return a.main
}

type panelController interface {
	visible() bool
	show()
	hide()
}

type panelActions struct {
	window *application.WebviewWindow
}

func (p panelActions) visible() bool { return p.window.IsVisible() }
func (p panelActions) show()         { p.window.Show().Focus() }
func (p panelActions) hide()         { p.window.Hide() }

func togglePanelWindow(panel panelController) {
	if panel.visible() {
		panel.hide()
		return
	}
	panel.show()
}

func (a *App) showPanel() {
	panel := a.ensurePanel()
	_ = a.tray.PositionWindow(panel, panelGap)
	panel.Show().Focus()
}

func (a *App) togglePanel() {
	panel := a.ensurePanel()
	_ = a.tray.PositionWindow(panel, panelGap)
	togglePanelWindow(panelActions{window: panel})
}

func (a *App) hidePanel() {
	a.windowMu.Lock()
	panel := a.panel
	a.windowMu.Unlock()
	if panel != nil {
		panel.Hide()
	}
}

func (a *App) showMain() {
	// 先隐藏置顶面板，再激活主窗口；顺序反过来会让主窗口短暂位于
	// 非激活 NSPanel 下方。
	a.hidePanel()
	a.dockApply(a.dockShow.Load())
	main := a.ensureMainWindow()
	main.Show()
	main.Focus()
}

func mainOptions(theme string) application.WebviewWindowOptions {
	return application.WebviewWindowOptions{
		Name:            "main",
		Title:           "Quota Keepalive",
		Width:           1240,
		Height:          820,
		MinWidth:        820,
		MinHeight:       600,
		InitialPosition: application.WindowCentered,
		Hidden:          true,
		BackgroundType:  application.BackgroundTypeSolid,
		URL:             themedURL("/main.html", theme),
		Mac: application.MacWindow{
			TitleBar:   application.MacTitleBarHiddenInset,
			Appearance: macAppearance(theme),
		},
	}
}

func settingsOptions(theme string) application.WebviewWindowOptions {
	return application.WebviewWindowOptions{
		Name:            "settings",
		Title:           "Quota Keepalive 设置",
		Width:           480,
		Height:          560,
		Hidden:          true,
		InitialPosition: application.WindowCentered,
		BackgroundType:  application.BackgroundTypeSolid,
		URL:             themedURL("/settings.html", theme),
		Mac: application.MacWindow{
			TitleBar:   application.MacTitleBarHiddenInset,
			Appearance: macAppearance(theme),
		},
	}
}

func panelOptions(theme string) application.WebviewWindowOptions {
	return application.WebviewWindowOptions{
		Name:            "panel",
		Title:           "Quota Keepalive",
		Width:           392,
		Height:          620,
		MinWidth:        392,
		MinHeight:       620,
		MaxWidth:        392,
		MaxHeight:       620,
		Frameless:       true,
		AlwaysOnTop:     true,
		Hidden:          true,
		DisableResize:   true,
		HideOnFocusLost: true,
		HideOnEscape:    true,
		BackgroundType:  application.BackgroundTypeTranslucent,
		URL:             themedURL("/panel.html", theme),
		Mac: application.MacWindow{
			Backdrop:     application.MacBackdropTranslucent,
			CornerRadius: 12,
			Appearance:   macAppearance(theme),
			CollectionBehavior: application.MacWindowCollectionBehaviorCanJoinAllSpaces |
				application.MacWindowCollectionBehaviorFullScreenAuxiliary,
			WindowClass:      application.MacWindowClassPanel,
			PanelPreferences: application.MacPanelPreferences{NonActivating: true},
		},
	}
}

func themedURL(path, theme string) string {
	if theme != "light" && theme != "dark" {
		return path
	}
	return path + "?theme=" + theme
}

func macAppearance(theme string) application.MacAppearanceType {
	switch theme {
	case "light":
		return application.NSAppearanceNameAqua
	case "dark":
		return application.NSAppearanceNameDarkAqua
	default:
		return application.DefaultAppearance
	}
}

// SetFailureState updates the tray tooltip. The menu-bar glyph always remains
// the native template icon so macOS never renders a detached status dot.
func (a *App) SetFailureState(hasFailure bool) {
	if a.failureOn.Swap(hasFailure) == hasFailure {
		return
	}
	tooltip := "Quota Keepalive"
	if hasFailure {
		tooltip += " · 有待处理"
	}
	if a.tray != nil {
		a.tray.SetTooltip(tooltip)
	}
}

// Run 启动 GUI 事件循环。
func (a *App) Run() error {
	ctx, cancel := context.WithCancel(context.Background())
	var statusDone chan struct{}
	if a.tokenSpeed != nil {
		a.tokenSpeed.Start(ctx)
		go a.monitorTokenSpeed(ctx)
	}
	if a.status != nil {
		statusDone = make(chan struct{})
		go func() {
			defer close(statusDone)
			a.monitorStatus(ctx)
		}()
	}
	err := a.wails.Run()
	cancel()
	if statusDone != nil {
		<-statusDone
	}
	if a.tokenSpeed != nil {
		a.tokenSpeed.Wait()
	}
	return err
}

var statusPollInterval = 30 * time.Second

func (a *App) monitorStatus(ctx context.Context) {
	a.refreshFailureState(ctx)
	ticker := time.NewTicker(statusPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.refreshFailureState(ctx)
		}
	}
}

func (a *App) refreshFailureState(ctx context.Context) {
	if a.status == nil {
		return
	}
	view, err := a.status(ctx)
	if err != nil {
		return
	}
	a.SetFailureState(statusNeedsAttention(view))
}

func statusNeedsAttention(view status.View) bool {
	for _, account := range view.Accounts {
		if account.SourceHealth == "reauth_required" {
			return true
		}
		for _, operation := range account.Operations {
			switch operation.State {
			case "manual_required", "failed_retryable", "failed_final":
				return true
			}
		}
	}
	return false
}

func (a *App) monitorTokenSpeed(ctx context.Context) {
	ticker := time.NewTicker(750 * time.Millisecond)
	defer ticker.Stop()
	lastLabel := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			snapshot := a.tokenSpeed.Snapshot()
			label := tokenspeed.FormatSpeed(snapshot.OutputTokensPerSecond) + " tok/s"
			if label != lastLabel {
				a.tray.SetLabel(label)
				lastLabel = label
			}
			a.wails.Event.Emit("tokenspeed-changed", snapshot)
		}
	}
}
