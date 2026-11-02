package gui

import (
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"qka/internal/settings"
)

func TestActivationPolicyReflectsDockSetting(t *testing.T) {
	// 关闭设置后仅菜单栏驻留（accessory）。
	if got := activationPolicy(false); got != application.ActivationPolicyAccessory {
		t.Errorf("show_dock_icon=false 应 accessory，实际 %v", got)
	}
	// 默认显示 Dock 图标（regular）。
	if got := activationPolicy(true); got != application.ActivationPolicyRegular {
		t.Errorf("show_dock_icon=true 应 regular，实际 %v", got)
	}
}

func TestClosingMainWindowHidesDockUntilMainReopens(t *testing.T) {
	current := settings.Default()
	var states []bool
	app := New(Deps{
		Settings: func() *settings.Settings { return current },
		DockIconVisible: func(show bool) {
			states = append(states, show)
		},
	})

	app.ensureMainWindow()
	app.main.HandleWindowEvent(uint(events.Common.WindowClosing))
	if len(states) != 1 || states[0] {
		t.Fatalf("closing main window should hide the Dock icon, states=%v", states)
	}

	app.showMain()
	if len(states) != 2 || !states[1] {
		t.Fatalf("reopening main window should restore the configured Dock icon, states=%v", states)
	}
}

func TestDockPreferenceUpdatesAndAppliesImmediately(t *testing.T) {
	var applied []bool
	app := New(Deps{DockIconVisible: func(show bool) {
		applied = append(applied, show)
	}})
	app.setDockPreference(false)
	if app.dockShow.Load() {
		t.Fatal("Dock preference should be false")
	}
	app.setDockPreference(true)
	if !app.dockShow.Load() {
		t.Fatal("Dock preference should be true")
	}
	if len(applied) != 2 || applied[0] || !applied[1] {
		t.Fatalf("Dock changes should apply immediately, applied=%v", applied)
	}
}

func TestApplicationIconIsPNG(t *testing.T) {
	icon := applicationIcon(64)
	if len(icon) < 8 || string(icon[1:4]) != "PNG" {
		t.Fatal("应用图标应为有效 PNG")
	}
}

func TestPanelUsesMacMenuBarBehavior(t *testing.T) {
	opts := panelOptions("system")
	if opts.Width != 392 || opts.Height != 620 ||
		opts.MinWidth != opts.Width || opts.MaxWidth != opts.Width ||
		opts.MinHeight != opts.Height || opts.MaxHeight != opts.Height {
		t.Fatalf("面板尺寸应固定为 392×620，实际 %+v", opts)
	}
	if !opts.Frameless || !opts.AlwaysOnTop || !opts.DisableResize ||
		!opts.HideOnFocusLost || !opts.HideOnEscape {
		t.Errorf("面板缺少菜单栏弹层行为: %+v", opts)
	}
	if opts.BackgroundType != application.BackgroundTypeTranslucent ||
		opts.Mac.Backdrop != application.MacBackdropTranslucent {
		t.Errorf("面板应使用半透明材质，实际 %v/%v", opts.BackgroundType, opts.Mac.Backdrop)
	}
	if opts.Mac.WindowClass != application.MacWindowClassPanel ||
		!opts.Mac.PanelPreferences.NonActivating {
		t.Errorf("面板应为非激活 NSPanel，实际 class=%v nonActivating=%v",
			opts.Mac.WindowClass, opts.Mac.PanelPreferences.NonActivating)
	}
	wantSpace := application.MacWindowCollectionBehaviorCanJoinAllSpaces |
		application.MacWindowCollectionBehaviorFullScreenAuxiliary
	if opts.Mac.CollectionBehavior&wantSpace != wantSpace {
		t.Errorf("面板应覆盖所有 Space 与全屏应用，实际 %v", opts.Mac.CollectionBehavior)
	}
}

func TestMainAndSettingsUseSolidNativeSurfaces(t *testing.T) {
	for name, opts := range map[string]application.WebviewWindowOptions{
		"main":     mainOptions("system"),
		"settings": settingsOptions("system"),
	} {
		if opts.BackgroundType != application.BackgroundTypeSolid ||
			opts.Mac.Backdrop != application.MacBackdropNormal {
			t.Errorf("%s 窗口应使用实底表面，实际 %v/%v",
				name, opts.BackgroundType, opts.Mac.Backdrop)
		}
		if !opts.Mac.TitleBar.AppearsTransparent ||
			!opts.Mac.TitleBar.FullSizeContent {
			t.Errorf("%s 窗口标题栏应透明并铺满内容，实际 %+v", name, opts.Mac.TitleBar)
		}
	}
}

func TestWindowTitlesUseFullProductName(t *testing.T) {
	if got := mainOptions("system").Title; got != "Quota Keepalive" {
		t.Errorf("主窗口标题应为 Quota Keepalive，实际 %q", got)
	}
	if got := panelOptions("system").Title; got != "Quota Keepalive" {
		t.Errorf("菜单栏面板标题应为 Quota Keepalive，实际 %q", got)
	}
	if got := settingsOptions("system").Title; got != "Quota Keepalive 设置" {
		t.Errorf("设置窗口标题应为 Quota Keepalive 设置，实际 %q", got)
	}
}

func TestThemeIsForwardedToWindows(t *testing.T) {
	cases := map[string]application.MacAppearanceType{
		"system": application.DefaultAppearance,
		"light":  application.NSAppearanceNameAqua,
		"dark":   application.NSAppearanceNameDarkAqua,
	}
	for theme, appearance := range cases {
		main := mainOptions(theme)
		settings := settingsOptions(theme)
		panel := panelOptions(theme)
		if main.Mac.Appearance != appearance || settings.Mac.Appearance != appearance ||
			panel.Mac.Appearance != appearance {
			t.Errorf("%s 主题未传给三个窗口: main=%v settings=%v panel=%v",
				theme, main.Mac.Appearance, settings.Mac.Appearance, panel.Mac.Appearance)
		}
		if theme == "system" {
			if main.URL != "/main.html" || settings.URL != "/settings.html" ||
				panel.URL != "/panel.html" {
				t.Errorf("system 主题不应在 URL 中强制主题: %q %q %q",
					main.URL, settings.URL, panel.URL)
			}
			continue
		}
		if main.URL != "/main.html?theme="+theme ||
			settings.URL != "/settings.html?theme="+theme ||
			panel.URL != "/panel.html?theme="+theme {
			t.Errorf("%s 主题未在首屏 URL 中生效: %q %q %q",
				theme, main.URL, settings.URL, panel.URL)
		}
	}
}
