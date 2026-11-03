package gui

import (
	"strings"
	"testing"
)

func readMenuPanelAssets(t *testing.T) (string, string) {
	t.Helper()
	pageData, err := frontendFS.ReadFile("frontend/panel.html")
	if err != nil {
		t.Fatal(err)
	}
	scriptData, err := frontendFS.ReadFile("frontend/assets/panel.js")
	if err != nil {
		t.Fatal(err)
	}
	return string(pageData), string(scriptData)
}

func requireStrings(t *testing.T, source, label string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(source, want) {
			t.Errorf("%s缺少 %q", label, want)
		}
	}
}

func TestMenuPanelHasThreeCompleteWorkspaces(t *testing.T) {
	page, script := readMenuPanelAssets(t)
	requireStrings(t, page, "菜单栏面板",
		"width: 392px",
		"background: var(--bg)",
		`class="brand-name">Quota Keepalive</span>`,
		`data-panel-tab="checkin"`,
		`data-panel-tab="speed"`,
		`data-panel-tab="pet"`,
		`id="page-checkin"`,
		`id="page-speed"`,
		`id="page-pet"`,
		`href="assets/title-system.css"`,
		`id="panel-speed-sparkline"`,
		`id="panel-sources"`,
		`class="agent-list" id="panel-sources"`,
		`id="pet-stage"`,
		`data-pet-id="bulbasaur"`,
		`data-pet-id="charmander"`,
		`data-pet-id="squirtle"`,
		`data-pet-id="pikachu"`,
		`<script type="module" src="assets/panel.js"></script>`,
		`id="open-main"`,
	)
	requireStrings(t, script, "菜单栏脚本",
		`const panelTabIds = ["checkin", "speed", "pet"]`,
		`const panelTabStorageKey = "qka.panel.tab"`,
		`function activateTab(`,
		`aria-selected`,
		`tab.tabIndex = active ? 0 : -1`,
		`page.hidden = !active`,
		`event.key === "ArrowRight"`,
		`event.key === "ArrowLeft"`,
		`event.key === "Home"`,
		`event.key === "End"`,
		`function updateFooter(`,
	)
	if strings.Contains(page, `class="brand-tag">QKA</span>`) {
		t.Fatal("菜单栏品牌不应继续显示 QKA 缩写")
	}
	if strings.Contains(script, "setInterval(") {
		t.Fatal("菜单栏动态效果不得依赖隐藏的 setInterval 定时器")
	}
	for _, forbidden := range []string{"title-icons.js", "title-icons.css", "speed-aurora"} {
		if strings.Contains(page, forbidden) || strings.Contains(script, forbidden) {
			t.Errorf("菜单栏不应保留旧称号或极光实现 %q", forbidden)
		}
	}
	if strings.Contains(page, "backdrop-filter: blur(34px)") {
		t.Fatal("菜单栏面板必须使用不透明底，避免桌面文字透出形成幻影")
	}
}

func TestMenuPanelCheckinUsesProviderNamesAndIconsSafely(t *testing.T) {
	page, script := readMenuPanelAssets(t)
	requireStrings(t, page, "签到页",
		`id="summary-total"`,
		`id="summary-done"`,
		`id="summary-attention"`,
		`id="checkin-badge"`,
		`id="checkin-updated"`,
		`id="targets"`,
		`id="message"`,
	)
	requireStrings(t, script, "签到脚本",
		`Bound.Status()`,
		`Bound.ProvidersList()`,
		`function providerMarkNode(provider)`,
		`import { populateProviderIcon, providerIconSpec } from "./provider-icons.js"`,
		`if (providerIconSpec(value))`,
		`return populateProviderIcon(mark, provider)`,
		`provider.name || provider.id || target`,
		`function summaryFor(accounts)`,
		`account.source_health === "reauth_required"`,
		`if (expanded[target] === undefined) expanded[target] = true`,
		`pending++`,
		`return;`,
		`function accountNode(account)`,
		`account.masked_identity || account.display_name`,
		`"登录已失效，需重新登录"`,
		`className = "acct-ops"`,
		`op-chip `,
		`fillEmpty(`,
	)
	if strings.Contains(script, "error.message") {
		t.Fatal("面板错误提示不得把原始异常文本拼入 HTML")
	}
}

func TestProviderIconsUseBundledMagpieAssets(t *testing.T) {
	page, script := readMenuPanelAssets(t)
	icons, err := frontendFS.ReadFile("frontend/assets/provider-icons.js")
	if err != nil {
		t.Fatal(err)
	}
	iconScript := string(icons)
	requireStrings(t, script, "供应商图标脚本",
		`return populateProviderIcon(mark, provider)`,
	)
	requireStrings(t, page, "供应商图标样式",
		`.provider-mark .provider-glyph`,
		`background: var(--card-2)`,
	)
	for _, want := range []string{
		`codearts:`,
		`"workbuddy-ai":`,
		`"minimax-code-global":`,
		`"opencode-zen-free":`,
		`"qoder-cn":`,
		`"qwenwork-enterprise":`,
		`"trae-global":`,
		`mode: "mask"`,
	} {
		if !strings.Contains(iconScript, want) {
			t.Errorf("供应商图标映射缺少 %q", want)
		}
	}
	for _, path := range []string{
		"frontend/assets/provider-icons/codearts.svg",
		"frontend/assets/provider-icons/workbuddy.svg",
		"frontend/assets/provider-icons/codebuddy.svg",
		"frontend/assets/provider-icons/minimax.svg",
		"frontend/assets/provider-icons/opencode.svg",
		"frontend/assets/provider-icons/qoder.svg",
		"frontend/assets/provider-icons/qwen.svg",
		"frontend/assets/provider-icons/trae.svg",
		"frontend/assets/provider-icons/zcode.png",
	} {
		data, err := frontendFS.ReadFile(path)
		if err != nil || len(data) < 100 {
			t.Errorf("本地供应商图标 %s 缺失或为空: bytes=%d err=%v", path, len(data), err)
		}
	}
	mainData, err := frontendFS.ReadFile("frontend/main.html")
	if err != nil {
		t.Fatal(err)
	}
	mainPage := string(mainData)
	requireStrings(t, mainPage, "主窗口供应商图标",
		`import { populateProviderIcon, providerIconSpec } from "./assets/provider-icons.js"`,
		`return populateProviderIcon(icon, provider)`,
		`.app-icon.provider-icon .provider-glyph`,
	)
	if strings.Contains(iconScript, "https://") {
		t.Fatal("供应商图标映射不应依赖远程地址")
	}
}

func TestPokemonHeadTrackingUsesDampedBoneMotion(t *testing.T) {
	data, err := frontendFS.ReadFile("frontend/assets/pokemon-3d.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	requireStrings(t, source, "3D 宠物头颈动画",
		`function smoothDamp(`,
		`LOOK_SMOOTH_TIME_X`,
		`BODY_FOLLOW_LAMBDA`,
		`HEAD_FOLLOW_LAMBDA`,
		`NECK_FOLLOW_LAMBDA`,
		`entry.bone.quaternion.slerp(desired, item.alpha)`,
		`holder.rotation.y += bodyAimX * 0.095`,
		`applyBoneAdjust(root, settings.boneAdjust)`,
		`material.metalness = 0`,
	)
	if strings.Contains(source, "entry.bone.quaternion.copy(desired)") {
		t.Fatal("头颈骨骼不应每帧硬覆盖目标姿态")
	}
}

func TestMenuPanelSpeedMatchesAgentSpeedDensity(t *testing.T) {
	page, script := readMenuPanelAssets(t)
	requireStrings(t, page, "速度页",
		`id="panel-speed"`,
		`id="panel-speed-state"`,
		`id="panel-speed-output"`,
		`id="panel-speed-updated"`,
		`id="panel-speed-agents"`,
		`id="panel-speed-today"`,
		`id="panel-speed-title"`,
		`id="panel-speed-icon"`,
		`id="panel-source-count"`,
		`id="panel-session-count"`,
		`class="agent-list" id="panel-sources"`,
	)
	requireStrings(t, script, "速度脚本",
		`Bound.TokenSpeedGet()`,
		`Events.On("tokenspeed-changed"`,
		`title_theme_id`,
		`titleIconMarkup(themeId, level`,
		`function renderSpeedDirectory(`,
		`const agentExpansion = new Map()`,
		`agentExpansion.has(stateKey)`,
		`agentExpansion.set(stateKey, next)`,
		`head.setAttribute("aria-expanded"`,
		`body.hidden = !next`,
		`function appendSessionRows(`,
		`agent-card`,
		`agent-sessions`,
		`output_tokens_per_second`,
		`today_total_tokens`,
		`generating_agents`,
		`requestAnimationFrame(step)`,
		`classList.add("rate-bump")`,
		`speedHistory`,
		`function seedSpeedHistory(output)`,
		`source-live-dot`,
		`session.generating`,
		`formatUpdated(view.updated_at)`,
		`run.hidden = true`,
		`document.getElementById("refresh").hidden = selected === "speed"`,
		`count.textContent = "自动刷新"`,
	)
	if strings.Contains(page, "刷新速度") || strings.Contains(script, "刷新速度") {
		t.Fatal("Token 速度页应自动刷新，不应保留手动刷新按钮")
	}
	sourceLoop := strings.Index(script, `sourceRows.forEach(function (source) {`)
	if sourceLoop < 0 {
		t.Fatal("Token 速度页缺少 Agent 渲染循环")
	}
	sourceRender := script[sourceLoop:]
	if strings.Index(sourceRender, `row.appendChild(rendered.head)`) >
		strings.Index(sourceRender, `row.appendChild(rendered.body)`) {
		t.Fatal("Token 速度页应先渲染 Agent 卡片，再在卡片内嵌套会话")
	}
	if !strings.Contains(script, `related.some(function (session) {`) {
		t.Fatal("活跃 Agent 首次出现时应默认展开其会话")
	}
	if strings.Contains(script, `appendSessionRows(row, related)`) {
		t.Fatal("会话应挂在 Agent 卡片内部，而不是直接平铺到目录")
	}
}

func TestMenuPanelPetUnlocksPersistAndPlay(t *testing.T) {
	page, script := readMenuPanelAssets(t)
	requireStrings(t, page, "宠物页",
		`id="pet-avatar"`,
		`.pokemon-3d-canvas {`,
		`id="pet-stage"`,
		`id="pet-bubble"`,
		`id="pet-particles"`,
		`id="pet-skill-layer"`,
		`id="pet-today-tokens"`,
		`id="pet-progress"`,
		`id="pet-unlocks"`,
		`cursor: default`,
		`.pet-avatar.is-3d .pokemon-sprites`,
		`.pokemon-stage-image`,
		`[data-evolution="3"] .pokemon-stage-image[data-stage="3"]`,
		`--pet-look-x`,
		`background: var(--bg)`,
	)
	requireStrings(t, script, "宠物脚本",
		`const petSelectedStorageKey = "qka.pet.selected"`,
		`POKEMON_EVOLUTION_THRESHOLDS`,
		`POKEMON_MAX_DAILY_TOKENS`,
		`const petCatalog = POKEMON_CATALOG`,
		`import { createPokemon3D } from "./pokemon-3d.js"`,
		`import { petAimPoint } from "./pet-gaze.js"`,
		`function currentSkillTier(`,
		`today_total_tokens`,
		`latestTodayTokens`,
		`evolutionStageForDailyTokens`,
		`function ensurePetViewer(`,
		`createPokemon3D(canvas`,
		`modelRotations: pet.stages.map(`,
		`petViewer.setPointer(nx, ny)`,
		`petViewer.playSkill(pet.effect, ultimate ? 5 : petEffectIntensity(),`,
		`ensurePetViewer(pet)`,
		`avatar.dataset.evolution = "1"`,
		`stageNode.dataset.evolution = "1"`,
		`function updatePetForDailyTokens(`,
		`function selectPet(petId)`,
		`function spawnPetEffect(`,
		`function triggerPetPlay(x, y, options)`,
		`avatar.dataset.renderedPet`,
		`avatar.dataset.evolution`,
		`querySelectorAll("button[data-pet-id]")`,
		`pointermove`,
		`pointerleave`,
		`const latestPointer = { x: null, y: null }`,
		`latestPointer.x = event.clientX - rect.left`,
		`classList.add(ultimate ? "ultimate" : "pounce")`,
		`className = "pet-particle fx-" + pet.effect`,
		`aria-pressed`,
		`event.key !== "Enter"`,
		`writeStoredValue(petSelectedStorageKey`,
		// 长短按：短按小技能，按住超过阈值进入蓄力并放大招。
		`const PANEL_LONG_PRESS_MS = 520`,
		`petViewer.setCharging(true)`,
		`petViewer.setCharging(false)`,
		`classList.add("charge")`,
		`{ ultimate: true }`,
		`function spawnPanelShockwave(`,
		`pet-shockwave`,
	)
	requireStrings(t, script, "宝可梦视线与技能交互",
		`function updatePetGaze(`,
		`petAimPoint(`,
		`function resetPetGaze(`,
		`setProperty("--pet-look-roll"`,
		`stage.classList.add("skill-" + pet.effect)`,
		`function petSkillSvg(`,
		`skill-vine`,
		`skill-flame-stream`,
		`skill-water-stream`,
		`skill-bolt`,
		`function clearPetSkill(`,
	)
	if strings.Contains(page, "cursor: none") {
		t.Fatal("宠物舞台必须保留系统鼠标指针，不能隐藏光标")
	}
	if strings.Contains(script, "petViewer.setStage(stage") ||
		strings.Contains(script, "function animatePetEvolution(") {
		t.Fatal("当日 Token 只能升级技能，不能切换宠物形象")
	}
	for _, forbidden := range []string{
		"cat3d", "pet-cat", "pet-dog", "pet-rabbit", "pet-mouse",
		"pet-wand", "wand-sway", "wandFeather", "pet-moon",
		"evolutionStageForSpeed", "petPeakStorageKey",
	} {
		if strings.Contains(page, forbidden) || strings.Contains(script, forbidden) {
			t.Errorf("宠物页不应保留旧宠物实现 %q", forbidden)
		}
	}
}

func TestMenuPanelPokemonCatalogHasEvolutionAndDistinctEffects(t *testing.T) {
	moduleData, err := frontendFS.ReadFile("frontend/assets/pokemon-art.js")
	if err != nil {
		t.Fatal(err)
	}
	module := string(moduleData)
	requireStrings(t, module, "宝可梦资源目录",
		`export const POKEMON_EVOLUTION_THRESHOLDS = [0, 200000000, 400000000]`,
		`export const POKEMON_MAX_DAILY_TOKENS = 1000000000`,
		`export const POKEMON_CATALOG`,
		`id: "bulbasaur"`,
		`id: "charmander"`,
		`id: "squirtle"`,
		`id: "pikachu"`,
		`stageNames: ["妙蛙种子", "妙蛙草", "妙蛙花"]`,
		`stageNames: ["小火龙", "火恐龙", "喷火龙"]`,
		`stageNames: ["杰尼龟", "卡咪龟", "水箭龟"]`,
		`stageNames: ["皮卡丘", "雷丘", "皮卡丘·巅峰"]`,
		`effect: "leaf"`,
		`effect: "ember"`,
		`effect: "water"`,
		`effect: "electric"`,
		`class="pokemon-stage-image"`,
		`src="./assets/pokemon/`,
		`bulbasaur.png`,
		`venusaur.png`,
		`charizard.png`,
		`blastoise.png`,
		`./assets/pokemon3d/1.glb`,
		`./assets/pokemon3d/4.glb`,
		`./assets/pokemon3d/7.glb`,
		`./assets/pokemon3d/25.glb`,
		`export function evolutionStageForDailyTokens(`,
	)
	if strings.Contains(module, "cat3d") || strings.Contains(module, "setInterval(") {
		t.Fatal("宝可梦资源不得回退到旧橘猫或隐藏定时器")
	}
	for _, model := range []string{
		"1.glb", "2.glb", "3.glb", "4.glb", "5.glb", "6.glb",
		"7.glb", "8.glb", "9.glb", "25.glb", "26.glb",
	} {
		data, err := frontendFS.ReadFile("frontend/assets/pokemon3d/" + model)
		if err != nil {
			t.Errorf("缺少 3D 宝可梦模型 %s: %v", model, err)
			continue
		}
		if len(data) < 50*1024 {
			t.Errorf("3D 宝可梦模型 %s 过小，可能不完整", model)
		}
	}
	notice, err := frontendFS.ReadFile("frontend/assets/pokemon3d/NOTICE.md")
	if err != nil || !strings.Contains(string(notice), "Pokemon-3D-api/assets") {
		t.Fatal("3D 宝可梦素材应保留来源说明")
	}
	viewer, err := frontendFS.ReadFile("frontend/assets/pokemon-3d.js")
	if err != nil {
		t.Fatal(err)
	}
	requireStrings(t, string(viewer), "3D 宝可梦渲染器",
		`GLTFLoader`,
		`DRACOLoader`,
		`setDecoderPath(new URL("./assets/vendor/draco/"`,
		`findRigBones`,
		`/head|neck/i`,
		`applyBoneAim`,
		`setPointer`,
		`playSkill`,
		`material.userData.qkaTransparent`,
		`material.userData.qkaDepthWrite`,
		`material.depthWrite = depthWrite`,
		`webglcontextlost`,
		`prefers-reduced-motion`,
	)
	for _, vendor := range []string{
		"frontend/assets/vendor/GLTFLoader.js",
		"frontend/assets/vendor/DRACOLoader.js",
		"frontend/assets/vendor/draco/draco_decoder.wasm",
		"frontend/assets/vendor/draco/draco_wasm_wrapper.js",
	} {
		if data, err := frontendFS.ReadFile(vendor); err != nil || len(data) == 0 {
			t.Errorf("缺少离线 3D 加载资源 %s", vendor)
		}
	}
}

func TestMenuPanelOpenMainStillHidesPanel(t *testing.T) {
	_, script := readMenuPanelAssets(t)
	const start = "function openMain() {"
	index := strings.Index(script, start)
	if index < 0 {
		t.Fatal("菜单栏面板缺少 openMain")
	}
	body := script[index:]
	if strings.Index(body, `Events.Emit("open-main")`) < 0 ||
		strings.Index(body, "hidePanel()") < 0 {
		t.Fatal("打开主窗口时必须同时通知原生层并隐藏面板")
	}
	requireStrings(t, script, "窗口事件",
		`Events.On("accounts-updated"`,
		`Events.On("common:WindowShow"`,
		`Events.On("theme-changed"`,
		`Events.Emit("collapse-panel")`,
	)
}

func TestCheckinPanelNavigationCollapseAndHiddenScrollbars(t *testing.T) {
	page, script := readMenuPanelAssets(t)
	requireStrings(t, page, "菜单栏签到导航",
		`data-checkin-scope="all"`,
		`data-checkin-scope="done"`,
		`data-checkin-scope="attention"`,
		`id="checkin-collapse-all"`,
		`scrollbar-width: none`,
	)
	requireStrings(t, script, "菜单栏签到导航逻辑",
		`function openCheckin(payload)`,
		`Events.Emit("open-checkin"`,
		`account_key: account.account_id || account.id || ""`,
		`function syncPanelCollapseButton(`,
		`currentTargets.every(function (target)`,
		`function displayAccountIdentity(`,
	)

	main, err := frontendFS.ReadFile("frontend/main.html")
	if err != nil {
		t.Fatal(err)
	}
	requireStrings(t, string(main), "主窗口签到深链",
		`Events.On("open-checkin"`,
		`function focusCheckinRequest(payload)`,
		`dataset.accountKey === accountKey`,
		`data-attention="true"`,
		`data-done="true"`,
		`card.scrollIntoView(`,
		// 供应商组默认展开（进来就看得到全部供应商），只记录被收起的。
		`collapsedProviderGroups.delete(target)`,
		`collapsedProviderGroups.clear()`,
		`const collapsedProviderGroups = new Set()`,
		`const collapsed = collapsedProviderGroups.has(groupKey)`,
		`"card account-group" + (collapsed ? " collapsed" : "")`,
		// 账号层默认折叠，二者状态互不干扰。
		`const expandedAccounts = new Set()`,
		`shouldCollapseAccount(expandedAccounts, accountState)`,
		`function displayAccountIdentity(`,
		`scrollbar-width: none`,
	)

	settings, err := frontendFS.ReadFile("frontend/settings.html")
	if err != nil {
		t.Fatal(err)
	}
	requireStrings(t, string(settings), "独立设置窗口滚动条",
		`scrollbar-width: none`,
		`*::-webkit-scrollbar`,
	)
}
