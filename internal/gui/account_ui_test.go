package gui

import (
	"strings"
	"testing"
)

func TestAccountManagementShowsLifecycleFeedback(t *testing.T) {
	data, err := frontendFS.ReadFile("frontend/main.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
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
