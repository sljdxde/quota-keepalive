package gui

// Tab 是主窗口的一个页签。
type Tab struct {
	ID    string
	Label string
}

// TabOrder 是主窗口固定的五页签顺序（不随运行时重排）。
var TabOrder = []Tab{
	{ID: "apps", Label: "应用管理"},
	{ID: "logs", Label: "日志"},
	{ID: "accounts", Label: "账号管理"},
	{ID: "agent", Label: "Agent 接入"},
	{ID: "plugins", Label: "插件"},
}
