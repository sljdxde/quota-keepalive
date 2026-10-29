package gui

import (
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestActivationPolicyReflectsDockSetting(t *testing.T) {
	// 默认无 Dock 图标（accessory）。
	if got := activationPolicy(false); got != application.ActivationPolicyAccessory {
		t.Errorf("show_dock_icon=false 应 accessory，实际 %v", got)
	}
	// 用户开启 show_dock_icon 后应显示 Dock 图标（regular）。
	if got := activationPolicy(true); got != application.ActivationPolicyRegular {
		t.Errorf("show_dock_icon=true 应 regular，实际 %v", got)
	}
}
