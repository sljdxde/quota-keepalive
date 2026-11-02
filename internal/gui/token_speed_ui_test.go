package gui

import (
	"strings"
	"testing"
)

func TestTokenSpeedPageUsesBackendSnapshot(t *testing.T) {
	data, err := frontendFS.ReadFile("frontend/main.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	for _, want := range []string{
		`data-tab="speed"`,
		`id="speed"`,
		"Token 速度",
		"Bound.TokenSpeedGet()",
		`Events.On("tokenspeed-changed"`,
		"output_tokens_per_second",
		"input_tokens_per_second",
		"source.today_total_tokens",
		"generating_agents",
		"title_theme",
		"title_theme_id",
		"title_catalog",
		"会话明细",
		"speed-sessions",
		`id="speed-title-picker"`,
		`id="title-picker-dialog"`,
		`id="title-theme-grid"`,
		`id="title-ladder-grid"`,
		`id="title-ladder-preview"`,
		`assets/title-system.js`,
		`href="assets/title-system.css"`,
		`title-rank-mark`,
		`title-rank-sigil`,
		"title-icon-current",
		`id="speed-orb-icon"`,
		`class="speed-rank-icon"`,
		`class="speed-rank-head"`,
		`id="speed-rank-title"`,
		`id="speed-rank-theme"`,
		`position: fixed; inset: 0; margin: auto;`,
		`Bound.SettingsSetString("title_theme"`,
		"session.generating",
		"输出速度按每个响应首个正文到完成的时间计算",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("Token 速度页缺少 %q", want)
		}
	}
	titleSystem, err := frontendFS.ReadFile("frontend/assets/title-system.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"titleThemeSigilMarkup",
		"titleThemeLabel",
		"stroke-width=\"1.75\"",
		"export function titleLevelProgress",
	} {
		if !strings.Contains(string(titleSystem), want) {
			t.Errorf("共享称号体系缺少 %q", want)
		}
	}
	if strings.Contains(page, "setInterval(") {
		t.Error("Token 速度页不应在前端重复实现采样计时器")
	}
	if strings.Contains(page, `<b>当前称号</b>`) {
		t.Error("速度称号面板应直接展示当前称号，而不是泛化的占位标签")
	}
	for _, forbidden := range []string{
		"title-icons.js",
		"title-icons.css",
		"title-vehicles/",
		"speed-orbit",
		"motion-breathe",
	} {
		if strings.Contains(page, forbidden) {
			t.Errorf("Token 速度页不应保留旧称号实现 %q", forbidden)
		}
	}
}

func TestTokenSpeedBindingUsesStableMethodID(t *testing.T) {
	data, err := frontendFS.ReadFile("frontend/bindings/qka/internal/gui/boundservice.js")
	if err != nil {
		t.Fatal(err)
	}
	bindings := string(data)
	if !strings.Contains(bindings, "export function TokenSpeedGet()") ||
		!strings.Contains(bindings, "$Call.ByID(1388247339)") {
		t.Fatal("TokenSpeedGet Wails 绑定缺失或方法 ID 已漂移")
	}
}
