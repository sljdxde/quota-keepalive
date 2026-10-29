// Package settings 提供声明式应用设置：由配置 schema 生成表单，
// 原子写入、备份、schema 校验，面板与 MCP 呈现同一状态。
package settings

// FieldType 是设置项类型。
type FieldType string

const (
	// FieldBool 是布尔开关。
	FieldBool FieldType = "bool"
)

// Field 是一个声明式设置项。新增设置项只需在 Schema 中追加一条，
// 无需手写表单界面。
type Field struct {
	Key         string    `json:"key"`
	Type        FieldType `json:"type"`
	Label       string    `json:"label"`
	Description string    `json:"description"`
	// Default 是默认值（最保守值）。
	Default bool `json:"default"`
}

// Schema 是应用设置的声明式 schema。
var Schema = []Field{
	{
		Key:         "show_dock_icon",
		Type:        FieldBool,
		Label:       "在 Dock 中显示图标",
		Description: "默认仅在菜单栏驻留，不显示 Dock 图标。",
		Default:     false,
	},
	{
		Key:         "launch_at_login",
		Type:        FieldBool,
		Label:       "开机自动启动",
		Description: "登录后自动启动 qka。",
		Default:     false,
	},
	{
		Key:         "enable_notifications",
		Type:        FieldBool,
		Label:       "系统通知",
		Description: "执行完成后发送系统通知；默认关闭，不打扰。",
		Default:     false,
	},
}
