package gui

import "qka/internal/status"

// AppRow 是应用管理页的一行（按目标应用聚合）。
type AppRow struct {
	Target          string `json:"target"`
	Enabled         bool   `json:"enabled"`
	AccountCount    int    `json:"account_count"`
	AggregateHealth string `json:"aggregate_health"`
	TodayResult     string `json:"today_result"`
	LastInvocation  string `json:"last_invocation"`
}

// healthRank 健康越差排名越高（聚合取最差）。
func healthRank(health string) int {
	switch health {
	case "reauth_required":
		return 2
	case "disabled":
		return 1
	default:
		return 0
	}
}

// worstToday 聚合今日结果：任一失败即失败，否则全部确认才确认，其余为进行中。
func worstToday(states []string) string {
	hasProvisional, hasFailed := false, false
	for _, s := range states {
		switch {
		case BadgeClass(s) == "failed":
			hasFailed = true
		case BadgeClass(s) == "pending":
			hasProvisional = true
		}
	}
	switch {
	case hasFailed:
		return "failed"
	case hasProvisional:
		return "provisional"
	default:
		return "confirmed"
	}
}

// AggregateApps 把状态视图按目标应用聚合为应用管理页行。
// 始终为 targets 中的每个目标输出一行（即使 0 账号），保证新安装也能看到受支持目标。
func AggregateApps(view status.View, targets []string) []AppRow {
	byTarget := map[string]*AppRow{}
	order := make([]string, 0, len(targets))

	// 先按目标清单播种空行。
	for _, t := range targets {
		if _, ok := byTarget[t]; ok {
			continue
		}
		// 新目标默认停用、无账号：健康度标为 no_account，今日结果 none。
		byTarget[t] = &AppRow{
			Target:          t,
			Enabled:         false,
			AggregateHealth: "no_account",
			TodayResult:     "none",
		}
		order = append(order, t)
	}

	for _, a := range view.Accounts {
		row, ok := byTarget[a.Target]
		if !ok {
			row = &AppRow{Target: a.Target, AggregateHealth: "healthy", TodayResult: "confirmed"}
			byTarget[a.Target] = row
			order = append(order, a.Target)
		}
		// 有账号后，空账号占位健康度让位于真实聚合。
		if row.AggregateHealth == "no_account" {
			row.AggregateHealth = "healthy"
		}
		if row.TodayResult == "none" {
			row.TodayResult = "confirmed"
		}
		row.AccountCount++
		if a.Enabled {
			row.Enabled = true
		}
		if healthRank(a.SourceHealth) > healthRank(row.AggregateHealth) {
			row.AggregateHealth = a.SourceHealth
		}
		states := make([]string, 0, len(a.Operations))
		for _, op := range a.Operations {
			states = append(states, op.State)
		}
		row.TodayResult = worstToday(append([]string{row.TodayResult}, states...))
	}

	rows := make([]AppRow, 0, len(order))
	for _, target := range order {
		row := byTarget[target]
		row.LastInvocation = view.LastInvocation
		rows = append(rows, *row)
	}
	return rows
}
