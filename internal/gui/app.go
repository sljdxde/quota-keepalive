// Package gui 组装 qka 的菜单栏托盘、面板与主窗口（Wails v3）。
package gui

import (
	"embed"
	"io/fs"
	"net/http"

	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed frontend
var frontendFS embed.FS

// App 持有 Wails 应用与运行期窗口。
type App struct {
	wails    *application.App
	tray     *application.SystemTray
	panel    *application.WebviewWindow
	main     *application.WebviewWindow
	settings *application.WebviewWindow
}

func assetHandler() http.Handler {
	sub, _ := fs.Sub(frontendFS, "frontend")
	return http.FileServer(http.FS(sub))
}

// New 创建 Wails 应用：默认 accessory 策略（无 Dock 图标），组装托盘、面板与五页签主窗口。
func New(deps Deps) *App {
	bound := NewBoundService(deps)

	wails := application.New(application.Options{
		Name: "qka",
		Services: []application.Service{
			application.NewService(bound),
		},
		Assets: application.AssetOptions{
			Handler: assetHandler(),
		},
		Mac: application.MacOptions{
			ActivationPolicy: application.ActivationPolicyAccessory,
		},
	})

	// 面板：frameless、置顶、初始隐藏，吸附在托盘图标下方。
	panel := wails.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:  "panel",
		Title: "qka",
		Width: 320, Height: 420,
		Frameless:   true,
		AlwaysOnTop: true,
		Hidden:      true,
		URL:         "/panel.html",
	})

	// 主窗口：五页签，初始隐藏。
	main := wails.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:  "main",
		Title: "qka",
		Width: 800, Height: 600,
		Hidden: true,
		URL:    "/main.html",
	})

	// 设置窗口：独立齿轮入口，初始隐藏。
	settings := wails.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:  "settings",
		Title: "设置",
		Width: 400, Height: 320,
		Hidden: true,
		URL:    "/settings.html",
	})

	// 齿轮入口经事件打开设置窗口（不占用第六个一级页签）。
	wails.Event.On("open-settings", func(*application.CustomEvent) {
		settings.Show()
		settings.Focus()
	})

	// 托盘菜单。
	menu := application.NewMenu()
	menu.Add("打开主窗口").OnClick(func(*application.Context) {
		main.Show()
		main.Focus()
	})
	menu.AddSeparator()
	menu.Add("退出").OnClick(func(*application.Context) {
		wails.Quit()
	})

	// 托盘：模板图标（深浅色自动着色）、吸附面板（点击切换、失焦即隐）、菜单。
	tray := wails.SystemTray.New()
	tray.SetTemplateIcon(templateIcon())
	tray.SetTooltip("qka")
	tray.AttachWindow(panel)
	tray.SetMenu(menu)

	return &App{wails: wails, tray: tray, panel: panel, main: main, settings: settings}
}

// Run 启动 GUI 事件循环。
func (a *App) Run() error {
	return a.wails.Run()
}
