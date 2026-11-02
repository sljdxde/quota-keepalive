package gui

import (
	"strings"
	"testing"
)

func TestMainUsesSixTabProductShell(t *testing.T) {
	data, err := frontendFS.ReadFile("frontend/main.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	orderedTabs := []string{
		`data-tab="apps"`,
		`data-tab="checkin"`,
		`data-tab="speed"`,
		`data-tab="pet"`,
		`data-tab="logs"`,
		`data-tab="settings"`,
	}
	lastIndex := -1
	for _, marker := range orderedTabs {
		index := strings.Index(page, marker)
		if index < 0 {
			t.Fatalf("主导航缺少 %q", marker)
		}
		if index <= lastIndex {
			t.Fatalf("主导航顺序错误：%q 必须排在上一项之后", marker)
		}
		lastIndex = index
	}
	for _, want := range []string{
		`data-tab="apps"`,
		`data-tab="checkin"`,
		`data-tab="logs"`,
		`data-tab="speed"`,
		`data-tab="pet"`,
		`data-tab="settings"`,
		`id="checkin"`,
		`id="pet"`,
		`id="settings"`,
		`id="settings-body"`,
		"Bound.SettingsSchema()",
		`id="settings-schedule-toggle"`,
		"Bound.ScheduleSetEnabled",
		`Events.On("open-settings"`,
		`Events.On("open-checkin"`,
		`openTab("checkin")`,
		`id="accounts-collapse-all"`,
		`function finishAuthorizedProviderLogin(`,
		"scrollbar-width: none",
		`values.__qka_version`,
		`"约" + scheduleIntervalText`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("六页签主窗口缺少 %q", want)
		}
	}
	for _, forbidden := range []string{
		`data-tab="accounts"`,
		`data-tab="agent"`,
		`data-tab="plugins"`,
		`id="agent"`,
		`id="plugins"`,
		"Bound.AgentStatus()",
		"Bound.PluginsList()",
	} {
		if strings.Contains(page, forbidden) {
			t.Errorf("主窗口不应再出现旧入口 %q", forbidden)
		}
	}
}
