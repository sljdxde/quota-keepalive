package mcp

// toolNames 是约定的工具面：名字与顺序稳定，且不含任何 schedule 读写/兜底工具。
var toolNames = []string{
	"agents_list",
	"accounts_manage",
	"checkin_run",
	"status",
	"plan_validate",
	"log_tail",
	"doctor_run",
	"feed_list",
}

var toolDescriptions = map[string]string{
	"agents_list":      "列出目标应用的自描述视图（身份、能力、可用性、账号、来源）",
	"accounts_manage":  "管理目标账号、登录态来源与来源优先级",
	"checkin_run":      "执行一次签到或保活的每日操作",
	"status":           "查看目标账号每日操作状态与证据",
	"plan_validate":    "校验计划是否符合成本、幂等与效应约束",
	"log_tail":         "读取结构化日志（含过滤与游标）",
	"doctor_run":       "运行环境与配置自检",
	"feed_list":        "列出活动/奖励等可订阅信息流",
}

func toolDefinitions() []toolDef {
	out := make([]toolDef, 0, len(toolNames))
	for _, name := range toolNames {
		out = append(out, toolDef{
			Name:        name,
			Description: toolDescriptions[name],
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		})
	}
	return out
}
