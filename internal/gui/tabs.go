package gui

// Tab 是主窗口的一个页签。
type Tab struct {
	ID    string
	Label string
}

// TabOrder 是主窗口固定的六页签顺序（不随运行时重排）。
var TabOrder = []Tab{
	{ID: "apps", Label: "应用管理"},
	{ID: "checkin", Label: "签到管理"},
	{ID: "speed", Label: "Token 速度"},
	{ID: "pet", Label: "宠物设置"},
	{ID: "logs", Label: "日志"},
	{ID: "settings", Label: "设置"},
}
