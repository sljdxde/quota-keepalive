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

func TestAccountManagementShowsLifecycleFeedback(t *testing.T) {
	page := accountPage(t)
	for _, want := range []string{
		"a.masked_identity",
		"sourceKindLabel",
		"sourceHealthLabel",
		"await action()",
		"Bound.AccountsSetEnabled",
		"Bound.AccountsDelete",
		"账号已启用",
		"账号已停用",
		"账号已删除",
		"启用失败",
		"停用失败",
		"删除失败",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("账号管理页缺少用户可见行为 %q", want)
		}
	}
}

func TestAccountManagementOnlyShowsSupportedWorkBuddyEntry(t *testing.T) {
	page := accountPage(t)
	for _, forbidden := range []string{
		`id="target-select"`,
		`value="qoder-cn"`,
		`value="doubao-work"`,
		`data-act="import"`,
		"API key",
		"导入来源",
		"桌面登录",
		`type="password"`,
		`placeholder="密码"`,
		`placeholder="验证码"`,
		`placeholder="扫码`,
	} {
		if strings.Contains(page, forbidden) {
			t.Errorf("账号管理页不应出现 MVP 不支持的入口或字段 %q", forbidden)
		}
	}
	for _, want := range []string{
		`data-act="login"`,
		`data-act="discover"`,
		"WorkBuddy CN",
		"官方登录添加",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("账号管理页缺少 WorkBuddy CN 入口 %q", want)
		}
	}
}

func TestAccountManagementShowsPerAccountStateAndActions(t *testing.T) {
	page := accountPage(t)
	for _, want := range []string{
		"a.operations",
		"op.message",
		"op.next_action",
		"manual_required",
		"Bound.AccountsRun",
		"立即签到",
		"只读复查",
		"重新登录",
		"刷新状态",
		"正在刷新登录状态与只读验证结果",
		"正在重试只读验证",
		"throw err",
		"无健康来源",
		"网络请求失败",
		"emptyAccounts",
		"overflow-x: hidden",
		"run.disabled = !a.enabled",
		"请先启用账号",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("账号管理页缺少状态或反馈 %q", want)
		}
	}
}

func TestFrontendAccountCallsExistInGeneratedBindings(t *testing.T) {
	page := accountPage(t)
	data, err := frontendFS.ReadFile("frontend/bindings/qka/internal/gui/boundservice.js")
	if err != nil {
		t.Fatal(err)
	}
	bindings := string(data)
	for _, method := range []string{
		"AccountsList",
		"AccountsDiscover",
		"AccountsBeginLogin",
		"AccountsLoginStatus",
		"AccountsCancelLogin",
		"AccountsSetEnabled",
		"AccountsDelete",
		"AccountsRun",
	} {
		if strings.Contains(page, "Bound."+method+"(") &&
			!strings.Contains(bindings, "export function "+method+"(") {
			t.Errorf("前端调用 Bound.%s 但生成绑定中不存在该方法", method)
		}
	}
}
