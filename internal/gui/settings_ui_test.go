package gui

import (
	"strings"
	"testing"
)

func TestSettingsPageRendersThreeWayAppearance(t *testing.T) {
	data, err := frontendFS.ReadFile("frontend/settings.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	for _, want := range []string{
		`f.type === "select"`,
		"select-control",
		"select-control-shell",
		"appearance: none",
		`group.setAttribute("role", "radiogroup")`,
		`Bound.SettingsSetString`,
		`if (f.key === "theme") applyTheme(option.value)`,
		`Events.On("theme-changed"`,
		`document.documentElement.dataset.theme = theme`,
		"background: var(--bg)",
		`id="schedule-toggle"`,
		"Bound.ScheduleGet",
		"Bound.ScheduleSetEnabled",
		`Events.Emit("schedule-changed")`,
		"上次结果",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("设置页缺少三档外观或主题响应 %q", want)
		}
	}
}

func TestSettingsPageUsesFullProductNameAndAlignedSections(t *testing.T) {
	mainData, err := frontendFS.ReadFile("frontend/main.html")
	if err != nil {
		t.Fatal(err)
	}
	settingsData, err := frontendFS.ReadFile("frontend/settings.html")
	if err != nil {
		t.Fatal(err)
	}
	mainPage := string(mainData)
	settingsPage := string(settingsData)

	requireStrings(t, mainPage, "主窗口设置页",
		`<span class="brand-name">Quota Keepalive</span>`,
		`id="settings-appearance-title">外观与启动</b>`,
		`class="settings-section-head"`,
		`row.appendChild(settingsCopy(field));`,
		`row.appendChild(wrap);`,
		`class="select-shell`,
		`appearance: none`,
		`.select-shell::after`,
		`@media (max-width: 700px)`,
		`.brand { display: none; }`,
	)
	requireStrings(t, settingsPage, "独立设置页",
		"Quota Keepalive 使用同一份声明式配置",
		`row.appendChild(copyBlock(f));`,
		`row.appendChild(switchWrap);`,
		`shell.className = "select-control-shell";`,
		`appearance: none`,
		`.select-control-shell::after`,
	)
	for pageName, page := range map[string]string{
		"main.html":     mainPage,
		"settings.html": settingsPage,
	} {
		if strings.Contains(page, ">QKA<") {
			t.Errorf("%s 仍把 QKA 缩写作为产品名称展示", pageName)
		}
	}
}
