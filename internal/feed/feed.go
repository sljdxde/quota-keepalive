// Package feed 提供适配器市场查询（feed_list）。本期市场未接线，
// 固定返回空集与“未实现”状态，不调用任何目标应用或 feed 生产端。
package feed

// Result 是 feed_list 的固定结果。
type Result struct {
	// Status 恒为 unimplemented。
	Status string `json:"status"`
	// Entries 恒为空集。
	Entries []any `json:"entries"`
}

// List 固定返回空集与“未实现”状态，不触发任何目标调用。
func List() Result {
	return Result{Status: "unimplemented", Entries: []any{}}
}
