// Package settings 提供声明式应用设置：由配置 schema 生成表单，
// 原子写入、备份、schema 校验，面板与 MCP 呈现同一状态。
package settings

// FieldType 是设置项类型。
type FieldType string

const (
	// FieldBool 是布尔开关。
	FieldBool FieldType = "bool"
	// FieldSelect 是有限选项。
	FieldSelect FieldType = "select"
	// FieldText 是自由文本（单行）。用于地址、路径一类需要手填的值。
	FieldText FieldType = "text"
)

// Option 是 select 设置项的可选值。
type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Field 是一个声明式设置项。新增设置项只需在 Schema 中追加一条，
// 无需手写表单界面。
type Field struct {
	Key         string    `json:"key"`
	Type        FieldType `json:"type"`
	Label       string    `json:"label"`
	Description string    `json:"description"`
	// Default 是默认值。bool 项为 bool，select 项为选项值。
	Default any      `json:"default"`
	Options []Option `json:"options,omitempty"`
}

// DefaultBool 返回布尔项的默认值；非布尔项返回 false。
func (f Field) DefaultBool() bool {
	value, _ := f.Default.(bool)
	return value
}

// DefaultString 返回 select 项的默认值；非 select 项返回空字符串。
func (f Field) DefaultString() string {
	value, _ := f.Default.(string)
	return value
}

// Schema 是应用设置的声明式 schema。
var Schema = []Field{
	{
		Key:         "theme",
		Type:        FieldSelect,
		Label:       "外观",
		Description: "跟随系统，或固定使用浅色、深色外观。",
		Default:     "system",
		Options: []Option{
			{Value: "system", Label: "跟随系统"},
			{Value: "light", Label: "浅色"},
			{Value: "dark", Label: "深色"},
		},
	},
	{
		Key:         "title_theme",
		Type:        FieldSelect,
		Label:       "称号主题",
		Description: "Token 速度页与菜单栏面板使用；默认交通工具。",
		Default:     "vehicles",
		Options: []Option{
			{Value: "vehicles", Label: "交通工具"},
			{Value: "animals", Label: "动物速度"},
			{Value: "athletics", Label: "田径赛场"},
			{Value: "music", Label: "音乐速度"},
			{Value: "network", Label: "网络世代"},
			{Value: "sound", Label: "音速时代"},
			{Value: "student", Label: "学生生涯"},
			{Value: "career", Label: "职场晋升"},
			{Value: "space", Label: "宇宙速度"},
		},
	},
	{
		Key:         "background",
		Type:        FieldSelect,
		Label:       "背景",
		Description: "选择内置桌面背景；自定义图片保存在本机。",
		Default:     "mist",
		Options: []Option{
			{Value: "mist", Label: "薄雾"},
			{Value: "coast", Label: "海岸"},
			{Value: "ember", Label: "余烬"},
			{Value: "glacier", Label: "冰川"},
			{Value: "night", Label: "夜空"},
			{Value: "custom", Label: "自定义图片"},
		},
	},
	{
		Key:         "glass",
		Type:        FieldSelect,
		Label:       "毛玻璃",
		Description: "控制窗口与卡片的透明度和背景模糊强度。",
		Default:     "standard",
		Options: []Option{
			{Value: "clear", Label: "通透"},
			{Value: "standard", Label: "标准"},
			{Value: "strong", Label: "磨砂"},
		},
	},
	{
		Key:         "show_dock_icon",
		Type:        FieldBool,
		Label:       "在 Dock 中显示图标",
		Description: "主窗口打开时显示 Quota Keepalive 图标；关闭主窗口后自动隐藏并仅驻留菜单栏（Windows 任务栏始终显示）。",
		Default:     true,
	},
	{
		Key:         "launch_at_login",
		Type:        FieldBool,
		Label:       "开机自动启动",
		Description: "登录后自动启动 Quota Keepalive。",
		Default:     false,
	},
	{
		Key:         "enable_notifications",
		Type:        FieldBool,
		Label:       "系统通知",
		Description: "执行完成后发送系统通知；默认关闭，不打扰。",
		Default:     false,
	},
	{
		Key:         "feedback_endpoint",
		Type:        FieldText,
		Label:       "反馈服务地址",
		Description: "留空使用内置地址。当前网络打不开内置地址时，可填入自己的转发/代理地址（须 https）。",
		Default:     "",
	},
}
