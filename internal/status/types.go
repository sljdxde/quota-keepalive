// Package status 提供只读的当前状态查询与计划预演（plan_validate）。
// status 报告今日状态与来源健康；plan_validate 只读预演，零网络、零写入。
package status

// OpStatus 是单账号×操作的今日状态。
type OpStatus struct {
	Target    string `json:"target"`
	AccountID string `json:"account_id"`
	Operation string `json:"operation"`
	State     string `json:"state"`
}

// AccountStatus 是单账号的状态视图。
type AccountStatus struct {
	Target         string     `json:"target"`
	AccountID      string     `json:"account_id"`
	DisplayName    string     `json:"display_name"`
	Enabled        bool       `json:"enabled"`
	SourceHealth   string     `json:"source_health"`
	Operations     []OpStatus `json:"operations"`
	PendingReasons []string   `json:"pending_reasons"`
}

// View 是 status 结果（不含任何计划/未来调度字段）。
type View struct {
	Enabled        bool            `json:"enabled"`
	Accounts       []AccountStatus `json:"accounts"`
	LastInvocation string          `json:"last_invocation"`
}

// PlanItem 是计划预演的一项。
type PlanItem struct {
	Target     string   `json:"target"`
	AccountID  string   `json:"account_id"`
	Operation  string   `json:"operation"`
	CostHint   float64  `json:"cost_hint"`
	Conflicts  []string `json:"conflicts"`
	Cooldown   bool     `json:"cooldown"`
	Transition string   `json:"transition"`
}

// Plan 是 plan_validate 结果（只读预演）。
type Plan struct {
	Items []PlanItem `json:"items"`
}
