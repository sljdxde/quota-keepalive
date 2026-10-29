package gui

import (
	"testing"

	"qka/internal/status"
)

func TestAggregateApps(t *testing.T) {
	view := status.View{
		Enabled:        true,
		LastInvocation: "2026-10-29T09:00:00+08:00",
		Accounts: []status.AccountStatus{
			{
				Target: "workbuddy-cn", AccountID: "a1", DisplayName: "u1",
				Enabled: true, SourceHealth: "healthy",
				Operations: []status.OpStatus{{State: "signed_confirmed"}},
			},
			{
				Target: "workbuddy-cn", AccountID: "a2", DisplayName: "u2",
				Enabled: true, SourceHealth: "reauth_required",
				Operations: []status.OpStatus{{State: "failed_retryable"}},
			},
			{
				Target: "qoder-cn", AccountID: "b1", DisplayName: "q1",
				Enabled: true, SourceHealth: "healthy",
				Operations: []status.OpStatus{{State: "signed_provisional"}},
			},
		},
	}

	rows := AggregateApps(view, []string{"workbuddy-cn", "qoder-cn", "qoder", "doubao-work"})
	if len(rows) != 4 {
		t.Fatalf("应始终列出 4 个目标，实际 %d", len(rows))
	}

	wb := rows[0]
	if wb.Target != "workbuddy-cn" || wb.AccountCount != 2 {
		t.Fatalf("workbuddy 聚合错误: %+v", wb)
	}
	if wb.AggregateHealth != "reauth_required" {
		t.Errorf("聚合健康应取最差 reauth_required，实际 %s", wb.AggregateHealth)
	}
	if wb.TodayResult != "failed" {
		t.Errorf("任一失败应聚合为 failed，实际 %s", wb.TodayResult)
	}
	if wb.LastInvocation != view.LastInvocation {
		t.Error("应带最近调用时间")
	}

	if rows[1].TodayResult != "provisional" {
		t.Errorf("provisional 应聚合为 provisional，实际 %s", rows[1].TodayResult)
	}

	// 无账号的目标（qoder）应保留占位行。
	empty := rows[2]
	if empty.Target != "qoder" || empty.AccountCount != 0 {
		t.Fatalf("无账号目标应保留占位，实际 %+v", empty)
	}
	if empty.AggregateHealth != "no_account" || empty.TodayResult != "none" {
		t.Errorf("无账号目标健康/今日应为 no_account/none，实际 %s/%s",
			empty.AggregateHealth, empty.TodayResult)
	}
}
