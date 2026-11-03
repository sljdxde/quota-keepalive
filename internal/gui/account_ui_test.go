package gui

import (
	"strings"
	"testing"
)

func accountPage(t *testing.T) string {
	t.Helper()
	data, err := frontendFS.ReadFile("frontend/main.html")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestCheckinManagementUsesGenericProviderLifecycle(t *testing.T) {
	page := accountPage(t)
	for _, want := range []string{
		"Bound.ProvidersList()",
		"Bound.ProvidersSetAccountEnabled",
		"Bound.ProvidersSetTaskEnabled",
		"Bound.ProvidersRun",
		"Bound.ProvidersTestModel",
		"Bound.ProvidersHistory",
		"Bound.ProvidersDeleteAccount",
		"account.masked_identity",
		"account.enabled",
		"taskEnabledFor(account, operation)",
		"账号已启用",
		"账号已停用",
		"账号已从 Quota Keepalive 删除，供应商数据未被修改",
		"账号设置保存失败，请重试。",
		"任务偏好保存失败，请重试。",
		"删除失败，请稍后重试。",
		"throw err",
		"let accountsRenderGeneration = 0;",
		"const request = ++accountsRenderGeneration;",
		"if (request !== accountsRenderGeneration) return;",
		`import { resolveAccountProviderView } from "./assets/account-view.js"`,
		"resolveAccountProviderView(",
		"populateProviderPicker(view, resolved.selectedProvider);",
		"if (!providers.length)",
		"providerModelPanel(provider)",
		"account-group-body",
		`add.textContent = "添加账号"`,
		`free.textContent = "免费"`,
		"provider-model-chip",
		`test.textContent = "测试模型"`,
		"model-test-result",
		`const head = document.createElement("button")`,
		`head.type = "button"`,
		`head.className = "account-group-head"`,
		`head.onclick = toggleGroup`,
		`head.setAttribute("aria-expanded"`,
		`head.setAttribute("aria-label"`,
		`cursor: pointer; user-select: none`,
		`const collapse = document.createElement("span")`,
		// 模型测试结论标在 chip 的点上，而不是铺一屏结果列表。
		`dot.className = "model-test-dot"`,
		`chip.insertBefore(dot, label)`,
		`function applyModelStatus(modelId, status, detail)`,
		`entry.dot.dataset.testState = state`,
		`applyModelStatus(model.id, tested.ok ? "ok" : "fail", detail)`,
		`applyModelStatus(model.id, "fail", detail)`,
		`function renderTestSummary(rows)`,
		`悬停模型名查看原因`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("签到管理页缺少通用供应商行为 %q", want)
		}
	}
}

// TestModelTestUsesChipDotsNotResultList 锁住展示方式：模型测试结果只体现在
// chip 的状态点上，不允许再退回「每个模型一行结果」的长列表。
func TestModelTestUsesChipDotsNotResultList(t *testing.T) {
	page := accountPage(t)
	for _, gone := range []string{
		`model-test-rows`,
		`model-test-row `,
		`renderBatchResults`,
	} {
		if strings.Contains(page, gone) {
			t.Errorf("模型测试不应再使用结果列表 %q", gone)
		}
	}
	if !strings.Contains(page, `.model-test-dot[data-test-state="ok"]`) ||
		!strings.Contains(page, `.model-test-dot[data-test-state="fail"]`) {
		t.Error("缺少模型测试状态点的绿/红样式")
	}
}

func TestCheckinManagementDoesNotExposeUnsafeCredentialInputs(t *testing.T) {
	page := accountPage(t)
	for _, forbidden := range []string{
		`id="target-select"`,
		`value="qoder-cn"`,
		`value="doubao-work"`,
		"导入来源",
		"桌面登录",
		`type="password"`,
		`placeholder="密码"`,
		`placeholder="验证码"`,
		`placeholder="扫码`,
	} {
		if strings.Contains(page, forbidden) {
			t.Errorf("签到管理页不应出现凭据输入或固定目标入口 %q", forbidden)
		}
	}
}

func TestCheckinManagementHasPerTaskActionsAndQuotaHistory(t *testing.T) {
	page := accountPage(t)
	for _, want := range []string{
		`const ops = provider.checkin ? ["checkin"] : []`,
		"立即签到",
		"今日已完成",
		"刷新额度",
		"历史 / 额度",
		"当前额度",
		"function claimGrowth(op)",
		`"今日已领取 +" + formatQuota(growth)`,
		"Math.abs(value - Number(op.balance)) > 0.000001",
		"provider-history",
		"quotaSummary(op)",
		"terminal",
		"trigger.disabled = !account.enabled || terminal",
		"账号已停用",
		"已启用 · 按签到开关执行",
		"今日尚未执行",
		"自动执行",
		"仅手动",
		"overflow-x: hidden",
		"background: var(--wall-gradient)",
		`document.documentElement.dataset.theme = theme`,
		`Events.On("theme-changed"`,
		`Events.On("schedule-changed", renderSchedule)`,
		"自动执行：已启用",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("签到管理页缺少状态或操作 %q", want)
		}
	}
}

func TestProviderLoginSupportsPromptOAuthAndAPIKey(t *testing.T) {
	page := accountPage(t)
	for _, want := range []string{
		`id="provider-login-dialog"`,
		"Bound.ProvidersBeginLogin",
		"Bound.ProvidersFinishLogin",
		"Bound.ProvidersLoginAPIKey",
		`view.status === "prompt"`,
		`view.status === "api_key"`,
		`view.status === "authorized"`,
		`view.status === "verified"`,
		"登录完成，账号已添加并默认启用",
		"Quota Keepalive 不接收密码、验证码或扫码内容",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("供应商登录弹窗缺少 %q", want)
		}
	}
}

func TestProviderLoginHasInteractivePikachu(t *testing.T) {
	page := accountPage(t)
	for _, want := range []string{
		`id="provider-login-pokemon"`,
		`id="provider-login-pokemon-art"`,
		`id="provider-login-pokemon-bubble"`,
		`id="provider-login-pokemon-particles"`,
		`pokemonById,`,
		`import { createPokemon3D } from "./assets/pokemon-3d.js"`,
		`const providerLoginPikachu = pokemonById("pikachu")`,
		`class="pokemon-3d-canvas"`,
		`models: [providerLoginPikachu.stages[0].model]`,
		`modelRotations: [providerLoginPikachu.stages[0].rotation || null]`,
		`createPokemon3D(`,
		`providerLoginPokemonViewer.setPointer(nx, ny)`,
		`providerLoginPokemonViewer.playSkill("electric", 3)`,
		`providerLoginPokemonArt.classList.add("is-3d")`,
		`function aimProviderLoginPokemon(`,
		`setProperty("--login-look-x"`,
		`.provider-login-pokemon-art .pokemon-stage-image`,
		`[data-stage="1"]`,
		`function spawnProviderLoginPokemonSparks(`,
		`function triggerProviderLoginPokemonPlay(`,
		`classList.add("zapping")`,
		`provider-login-spark`,
		`document.visibilityState !== "hidden"`,
		`@media (prefers-reduced-motion: reduce)`,
		`function finishAuthorizedProviderLogin(`,
		`Bound.ProvidersFinishLogin(session, "")`,
		`完成后会自动验证并关闭登录窗口`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("供应商登录弹窗缺少皮卡丘交互 %q", want)
		}
	}
	for _, forbidden := range []string{
		`id="provider-login-cat"`,
		`import("./assets/cat-3d.js")`,
		`createCat3D`,
		`登录成功，喵`,
		`provider-login-wand`,
		`wand-sway`,
	} {
		if strings.Contains(page, forbidden) {
			t.Errorf("供应商登录弹窗不应保留旧橘猫实现 %q", forbidden)
		}
	}
}

func TestFrontendProviderCallsExistInGeneratedBindings(t *testing.T) {
	page := accountPage(t)
	data, err := frontendFS.ReadFile("frontend/bindings/qka/internal/gui/boundservice.js")
	if err != nil {
		t.Fatal(err)
	}
	bindings := string(data)
	for _, method := range []string{
		"ProvidersList",
		"ProvidersBeginLogin",
		"ProvidersFinishLogin",
		"ProvidersLoginAPIKey",
		"ProvidersSetAccountEnabled",
		"ProvidersSetTaskEnabled",
		"ProvidersRun",
		"ProvidersTestModel",
		"ProvidersDeleteAccount",
		"ProvidersHistory",
		"ScheduleGet",
		"ScheduleSetEnabled",
		"ScheduleSetConfig",
		"UpdateCheck",
		"UpdateInstall",
	} {
		if strings.Contains(page, "Bound."+method+"(") &&
			!strings.Contains(bindings, "export function "+method+"(") {
			t.Errorf("前端调用 Bound.%s 但生成绑定中不存在该方法", method)
		}
	}
}
