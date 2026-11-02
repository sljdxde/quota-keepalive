package gui

import (
	"strings"
	"testing"
)

func TestAppsPageGroupsEnabledAndDisabledProviders(t *testing.T) {
	data, err := frontendFS.ReadFile("frontend/main.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	for _, want := range []string{
		".apps-provider-group",
		".apps-enabled",
		".apps-disabled > summary",
		".apps-provider-list",
		".app-item",
		".app-detail[hidden]",
		`providers.filter(function (provider) { return provider.enabled; })`,
		`providers.filter(function (provider) { return !provider.enabled; })`,
		`item.className = "app-item"`,
		`expand.setAttribute("aria-expanded", "false")`,
		`detail.appendChild(login);`,
		`detail.appendChild(viewAccounts);`,
		`disabledGroup.className = "apps-provider-group apps-disabled"`,
		`let appsDisabledOpen = false;`,
		`disabledGroup.open = appsDisabledOpen;`,
		`appsDisabledOpen = disabledGroup.open;`,
		`hint.textContent = disabledGroup.open ? "收起管理" : "展开管理";`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("应用管理折叠分组缺少 %q", want)
		}
	}
	if strings.Contains(page, "actions.appendChild(login)") ||
		strings.Contains(page, "actions.appendChild(viewAccounts)") {
		t.Fatal("供应商主行不应继续堆叠登录和查看账号按钮；操作应移到展开区")
	}
}
