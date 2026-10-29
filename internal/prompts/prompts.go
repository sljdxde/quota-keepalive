// Package prompts 提供五个引导式 MCP prompts：安装配对、官方登录加账号、
// 宿主调度设置、失败账号诊断、今日结果解释。
package prompts

import (
	"errors"
	"strings"
)

var (
	// ErrUnknownPrompt 表示请求了未定义的 prompt。
	ErrUnknownPrompt = errors.New("prompts: 未知 prompt")
)

// Argument 是 prompt 参数。
type Argument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// Prompt 是一个可被宿主列出与读取的引导式 prompt。
type Prompt struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Arguments   []Argument `json:"arguments,omitempty"`
}

// Message 是 prompt 渲染后的一条消息。
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// catalog 是五个固定 prompt。
var catalog = []Prompt{
	{
		Name:        "install_and_pair",
		Description: "引导用户安装 QKA 并与宿主完成配对",
	},
	{
		Name:        "add_account_with_official_login",
		Description: "引导用户通过官方登录添加账号（不收集密码/验证码）",
		Arguments: []Argument{
			{Name: "target", Description: "目标应用 id", Required: true},
		},
	},
	{
		Name:        "setup_host_schedule",
		Description: "输出宿主侧创建/核验定时任务的步骤与调用文本（不写 QKA 状态）",
	},
	{
		Name:        "diagnose_failed_account",
		Description: "诊断失败账号并给出可执行修复步骤",
		Arguments: []Argument{
			{Name: "target", Description: "目标应用 id", Required: false},
		},
	},
	{
		Name:        "explain_today_results",
		Description: "解释今日签到与保活结果",
	},
}

// List 返回五个固定 prompt。
func List() []Prompt {
	return catalog
}

// Render 渲染指定 prompt 的引导消息。
func Render(name string, args map[string]string) ([]Message, error) {
	switch name {
	case "install_and_pair":
		return []Message{{Role: "user", Content: strings.Join([]string{
			"请按以下步骤安装并配对 QKA：",
			"1. 启动 QKA，菜单栏出现图标；",
			"2. 在宿主应用中选择连接 QKA，完成一次性配对；",
			"3. 配对成功后，宿主即可列出 QKA 的工具。",
		}, "\n")}}, nil

	case "add_account_with_official_login":
		target := args["target"]
		return []Message{{Role: "user", Content: strings.Join([]string{
			"为 " + target + " 添加账号（仅官方登录，不收集密码/验证码）：",
			"1. 在账号页选择目标 " + target + "，点击「官方登录添加」；",
			"2. 浏览器打开官方站点，由用户亲自登录；",
			"3. 登录验证通过后，QKA 建立受管来源并自动发现账号。",
		}, "\n")}}, nil

	case "setup_host_schedule":
		// 仅输出宿主侧步骤与调用文本，不写 QKA 状态。
		return []Message{{Role: "user", Content: strings.Join([]string{
			"以下操作在宿主侧完成，QKA 不创建任何调度状态：",
			"1. 在宿主的自动化/定时能力中新建每日任务；",
			"2. 任务在每个北京日早晨调用 QKA 的 checkin_run；",
			"3. 若为 WorkBuddy CN，再安排一次 keepalive 调用；",
			"4. 核验：在宿主侧确认任务已启用并查看最近一次执行结果。",
		}, "\n")}}, nil

	case "diagnose_failed_account":
		target := args["target"]
		return []Message{{Role: "user", Content: strings.Join([]string{
			"诊断 " + target + " 的失败账号：",
			"1. 运行 doctor_run 查看配对/凭据/端点；",
			"2. 查看该账号最近日志（log_tail 按 target/account 筛选）；",
			"3. 若为 reauth_required，用官方登录重新授权；",
			"4. 若为平台不支持或重试失败，按修复指引处理后手动执行一次。",
		}, "\n")}}, nil

	case "explain_today_results":
		return []Message{{Role: "user", Content: strings.Join([]string{
			"今日结果说明：",
			"- signed_confirmed：午后证据充分，已确认；",
			"- signed_provisional：午前或证据不足，待午后复核；",
			"- failed_retryable：可重试，受冷却约束；",
			"- failed_final：终态失败，需人工处理；",
			"- keepalive completed：WorkBuddy 今日真实对话已完成。",
		}, "\n")}}, nil

	default:
		return nil, ErrUnknownPrompt
	}
}
