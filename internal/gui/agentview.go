package gui

import "errors"

// errNotWired 表示该后端能力在当前装配下未提供。
var errNotWired = errors.New("gui: 该能力尚未接线")

// LogFilter 是日志页的四类筛选条件。
type LogFilter struct {
	Target    string
	AccountID string
	Operation string
	State     string
}

// LogEntry 是日志页一行。
type LogEntry struct {
	ID        int64  `json:"id"`
	TS        string `json:"ts"`
	Target    string `json:"target"`
	AccountID string `json:"account_id"`
	Operation string `json:"operation"`
	State     string `json:"state"`
	Level     string `json:"level"`
	Message   string `json:"message"`
}

// LogPage 是日志页一页。
type LogPage struct {
	Entries    []LogEntry `json:"entries"`
	NextCursor int64      `json:"next_cursor"`
}

// HostRow 是 Agent 接入页一行宿主。
type HostRow struct {
	HostID         string `json:"host_id"`
	Label          string `json:"label"`
	Paired         bool   `json:"paired"`
	Connected      bool   `json:"connected"`
	LastInvocation string `json:"last_invocation"`
	ScheduleGuide  string `json:"schedule_guide"`
}

// AgentView 是 Agent 接入页视图。
type AgentView struct {
	Hosts []HostRow `json:"hosts"`
	// PlanOwnedByHost 明确计划由宿主保存，qka 不提供计划编辑器。
	PlanOwnedByHost bool `json:"plan_owned_by_host"`
	// PlanObservable 恒 false：宿主内部计划对 qka 不可观察。
	PlanObservable bool `json:"plan_observable"`
}

// NewAgentView 组装视图，固定计划归属与不可观察语义。
func NewAgentView(hosts []HostRow) AgentView {
	return AgentView{
		Hosts: hosts, PlanOwnedByHost: true, PlanObservable: false,
	}
}
