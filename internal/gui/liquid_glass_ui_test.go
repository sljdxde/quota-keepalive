package gui

import (
	"strings"
	"testing"
)

func TestLiquidGlassSharedAssetsAreWiredIntoBothWindows(t *testing.T) {
	mainData, err := frontendFS.ReadFile("frontend/main.html")
	if err != nil {
		t.Fatal(err)
	}
	panelData, err := frontendFS.ReadFile("frontend/panel.html")
	if err != nil {
		t.Fatal(err)
	}
	mainPage := string(mainData)
	panelPage := string(panelData)

	requireStrings(t, mainPage, "主窗口液态玻璃",
		`<link rel="stylesheet" href="assets/liquid-ui.css">`,
		`<script type="module" src="assets/liquid-ui.js"></script>`,
		`id="tabs" data-liquid-tabs`,
		`id="log-categories" data-liquid-tabs`,
		`id="command-shortcut"`,
		`id="command-dialog"`,
		`id="command-input"`,
		`id="command-results"`,
	)
	requireStrings(t, panelPage, "菜单栏液态玻璃",
		`<link rel="stylesheet" href="assets/liquid-ui.css">`,
		`<script type="module" src="assets/liquid-ui.js"></script>`,
		`class="tabs" data-liquid-tabs`,
		`data-liquid-dock`,
	)

	cssData, err := frontendFS.ReadFile("frontend/assets/liquid-ui.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(cssData)
	requireStrings(t, css, "液态玻璃样式",
		`[data-liquid-tabs]`,
		`.liquid-tab-pill`,
		`.liquid-surface`,
		`--liquid-tilt-x`,
		`--liquid-tilt-y`,
		`.liquid-sheen`,
		`[data-liquid-dock]`,
		`.command-liquid-highlight`,
		`mix-blend-mode: soft-light`,
		`mask-composite: exclude`,
		`.command-dialog::backdrop`,
		`backdrop-filter: blur(42px)`,
		`@media (prefers-reduced-motion: reduce)`,
	)

	jsData, err := frontendFS.ReadFile("frontend/assets/liquid-ui.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(jsData)
	requireStrings(t, js, "液态玻璃交互",
		`const MAX_TILT = 3.2`,
		`export function initLiquidTabs(`,
		`function initLiquidDock(`,
		`pill.animate(`,
		`function initLiquidCommandPalette(`,
		`command-liquid-highlight`,
		`new MutationObserver(`,
		`new ResizeObserver(`,
		`scaleY(1.035)`,
		`export function initLiquidSurfaces(`,
		`pointerenter`,
		`pointermove`,
		`pointerleave`,
		`requestAnimationFrame`,
		`--liquid-tilt-x`,
		`--liquid-tilt-y`,
		`document.readyState === "loading"`,
	)
	if strings.Contains(js, "setInterval(") {
		t.Fatal("液态玻璃交互不得依赖 setInterval")
	}
	for _, forbiddenSelector := range []string{
		`".summary-tile"`,
		`".metric-card"`,
		`".card:not(.table-card)"`,
		`".list:not(.empty-list)"`,
		`".pet-progress-card"`,
		`".settings-schedule"`,
	} {
		if strings.Contains(js, forbiddenSelector) {
			t.Fatalf("窗口倾斜与高光不应重新覆盖 %s", forbiddenSelector)
		}
	}
	if !strings.Contains(js, `".speed-hero"`) ||
		!strings.Contains(js, `"section.page > .summary"`) {
		t.Fatal("窗口倾斜与高光只应保留在摘要和速度主卡等关键表面")
	}
}

func TestCommandPaletteSupportsSafeSearchAndKeyboardNavigation(t *testing.T) {
	data, err := frontendFS.ReadFile("frontend/main.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	requireStrings(t, page, "命令面板",
		`placeholder="搜索页面、操作和设置…"`,
		`role="combobox"`,
		`aria-controls="command-results"`,
		`role="listbox"`,
		`id="command-key-hint"`,
		`const commandItems = [`,
		`function commandScore(`,
		`function appendHighlighted(`,
		`document.createElement("mark")`,
		`const commandHighlight =`,
		`function setCommandActive(`,
		`function renderCommands(`,
		`function runCommand(`,
		`function openCommandPalette(`,
		`commandDialog.showModal()`,
		`event.metaKey || event.ctrlKey`,
		`key === "k"`,
		`event.key === "ArrowDown"`,
		`event.key === "ArrowUp"`,
		`event.key === "Enter"`,
		`event.key === "Escape"`,
		`aria-activedescendant`,
		`scrollIntoView({ block: "nearest" })`,
	)
	if strings.Contains(page, "commandResults.innerHTML = query") {
		t.Fatal("命令搜索不得把用户输入直接拼入 HTML")
	}
}
