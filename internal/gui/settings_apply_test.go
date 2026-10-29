package gui

import (
	"context"
	"testing"

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

// 静态保证 context 被引用（测试文件内其他用例需要）。
var _ = context.Background
