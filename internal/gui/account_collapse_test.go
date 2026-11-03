package gui

import (
	"strings"
	"testing"
)

// TestAccountCardsCollapseKeepsHeaderActions 断言账号折叠的可用性约定：
// 收起后仍能看到身份、启用状态与开关，签到与操作按钮收进 body；
// 折叠热区不吞开关点击，且判定逻辑走被单测覆盖的纯函数模块。
func TestAccountCardsCollapseKeepsHeaderActions(t *testing.T) {
	page := accountPage(t)
	for _, want := range []string{
		`import {
  accountStateKey,
  nextAccountExpansion,
  shouldCollapseAccount,
} from "./assets/account-collapse.js"`,
		`const accountState = accountStateKey(provider.id, account.key || account.id || "")`,
		`const collapsed = shouldCollapseAccount(expandedAccounts, accountState)`,
		`const next = nextAccountExpansion(expandedAccounts, accountState)`,
		`box.classList.toggle("collapsed", !next)`,
		`body.className = "account-body"`,
		`top.classList.add("account-collapse-head")`,
		`chevron.className = "account-collapse"`,
		`if (event.target.closest(".switch")) return`,
		`top.onkeydown = function (event)`,
		`accountStateKey(card.dataset.provider, card.dataset.accountKey)`,
		`.account-card.collapsed .account-body { display: none; }`,
		`.account-card.collapsed .account-collapse { transform: rotate(-90deg); }`,
		`@media (prefers-reduced-motion: reduce)`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("账号折叠缺少 %q", want)
		}
	}

	// DOM 挂载顺序：先 top（常驻头），再 body；body 里先签到行后操作按钮。
	// 注意源码中 body 先在函数前段创建、后段才挂载，所以断言的是 appendChild 顺序。
	topAttached := strings.Index(page, `box.appendChild(top)`)
	bodyAttached := strings.Index(page, `box.appendChild(body)`)
	taskInBody := strings.Index(page, `body.appendChild(taskList)`)
	actionsInBody := strings.Index(page, `body.appendChild(actions)`)
	if topAttached < 0 || bodyAttached < 0 || taskInBody < 0 || actionsInBody < 0 {
		t.Fatal("账号折叠结构标记缺失")
	}
	if topAttached >= bodyAttached {
		t.Fatalf("常驻头部必须在 body 之前挂载: top=%d body=%d", topAttached, bodyAttached)
	}
	if taskInBody >= actionsInBody {
		t.Fatalf("签到行应在操作按钮之前: task=%d actions=%d", taskInBody, actionsInBody)
	}
}
