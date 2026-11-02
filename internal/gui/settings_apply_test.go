package gui

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"qka/internal/schedule"
	"qka/internal/settings"
)

func TestSettingsSetAppliesSideEffects(t *testing.T) {
	cur := settings.Default()
	var appliedKey string
	var appliedValue bool

	svc := NewBoundService(Deps{
		Settings:     func() *settings.Settings { return cur },
		SaveSettings: func(*settings.Settings) error { return nil },
		AfterSaveSettings: func(s *settings.Settings) error {
			appliedKey = "launch_at_login"
			appliedValue = s.Get("launch_at_login")
			return nil
		},
	})

	if err := svc.SettingsSet("launch_at_login", true); err != nil {
		t.Fatal(err)
	}
	if appliedKey != "launch_at_login" || !appliedValue {
		t.Fatal("保存 launch_at_login 后应触发应用副作用（autostart）")
	}
}

func TestSettingsSetAppliesDockVisibility(t *testing.T) {
	cur := settings.Default()
	var visible bool
	svc := NewBoundService(Deps{
		Settings:        func() *settings.Settings { return cur },
		SaveSettings:    func(*settings.Settings) error { return nil },
		DockIconVisible: func(show bool) { visible = show },
	})

	if err := svc.SettingsSet("show_dock_icon", false); err != nil {
		t.Fatal(err)
	}
	if visible {
		t.Fatal("关闭 show_dock_icon 后应即时隐藏 Dock 图标")
	}
	if err := svc.SettingsSet("show_dock_icon", true); err != nil {
		t.Fatal(err)
	}
	if !visible {
		t.Fatal("开启 show_dock_icon 后应即时显示 Dock 图标")
	}
}

func TestSettingsSetStringPersistsAndAppliesSideEffects(t *testing.T) {
	cur := settings.Default()
	saves := 0
	appliedValue := ""

	svc := NewBoundService(Deps{
		Settings:     func() *settings.Settings { return cur },
		SaveSettings: func(*settings.Settings) error { saves++; return nil },
		AfterSaveSettings: func(s *settings.Settings) error {
			appliedValue = s.GetString("theme")
			return nil
		},
	})

	if err := svc.SettingsSetString("theme", "dark"); err != nil {
		t.Fatal(err)
	}
	if saves != 1 || appliedValue != "dark" || cur.GetString("theme") != "dark" {
		t.Fatalf("保存外观后应立即持久化并应用副作用: saves=%d applied=%q current=%q",
			saves, appliedValue, cur.GetString("theme"))
	}
}

func TestSettingsGetIncludesRunningVersionMetadata(t *testing.T) {
	svc := NewBoundService(Deps{
		CurrentVersion: "0.1.0",
		Settings:       func() *settings.Settings { return settings.Default() },
	})
	raw, err := svc.SettingsGet()
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]any
	if err := json.Unmarshal(raw, &values); err != nil {
		t.Fatal(err)
	}
	if values["__qka_version"] != "0.1.0" {
		t.Fatalf("settings metadata version = %v", values["__qka_version"])
	}
	if _, ok := values["theme"]; !ok {
		t.Fatal("settings metadata must not replace schema values")
	}
}

func TestScheduleBindingsReturnRedactedView(t *testing.T) {
	view := schedule.View{
		Enabled: true, IntervalMinutes: 30, Action: "all",
		Timezone: "local", Installed: true,
		NextRun: "2026-10-31 09:10",
		LastRun: &schedule.LastRun{
			At: "2026-10-30T09:10:00+08:00", Result: "success",
			Attempted: 1, Succeeded: 1,
		},
	}
	svc := NewBoundService(Deps{
		ScheduleGet: func(context.Context) (schedule.View, error) {
			return view, nil
		},
		ScheduleSetEnabled: func(context.Context, bool) (schedule.View, error) {
			return view, nil
		},
	})
	raw, err := svc.ScheduleGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 || !json.Valid(raw) {
		t.Fatalf("ScheduleGet 应返回 JSON: %s", raw)
	}
	raw, err = svc.ScheduleSetEnabled(true)
	if err != nil || !json.Valid(raw) {
		t.Fatalf("ScheduleSetEnabled 应返回最新 JSON: %s %v", raw, err)
	}
	if view.Now != (time.Time{}) {
		t.Fatal("测试视图不应携带内部时钟字段")
	}
}
