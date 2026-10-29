package gui

import (
	"context"
	"encoding/json"
	"testing"
)

func TestAgentViewPlanSemantics(t *testing.T) {
	view := NewAgentView([]HostRow{
		{HostID: "workbuddy", Label: "WorkBuddy", Paired: true, Connected: true,
			LastInvocation: "2026-10-29T09:00:00+08:00", ScheduleGuide: "在 WorkBuddy 设置中创建"},
	})
	if !view.PlanOwnedByHost {
		t.Error("应明确计划由宿主保存")
	}
	if view.PlanObservable {
		t.Error("宿主内部计划应恒为不可观察")
	}
	if len(view.Hosts) != 1 {
		t.Fatalf("应包含 1 个宿主，实际 %d", len(view.Hosts))
	}
}

func TestBoundLogsTail(t *testing.T) {
	svc := NewBoundService(Deps{
		Logs: func(_ context.Context, f LogFilter, limit int, cursor int64) (LogPage, error) {
			if f.Target != "qoder-cn" || limit != 50 || cursor != 7 {
				t.Errorf("筛选/limit/cursor 传递错误: %+v %d %d", f, limit, cursor)
			}
			return LogPage{Entries: []LogEntry{{ID: 8, Message: "m"}}, NextCursor: 8}, nil
		},
	})
	raw, err := svc.LogsTail("qoder-cn", "", "", "", 50, 7)
	if err != nil {
		t.Fatal(err)
	}
	var page LogPage
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 1 || page.NextCursor != 8 {
		t.Fatalf("日志页序列化错误: %s", raw)
	}
}

func TestBoundAgentStatus(t *testing.T) {
	svc := NewBoundService(Deps{
		AgentStatus: func(context.Context) (AgentView, error) {
			return NewAgentView(nil), nil
		},
	})
	raw, err := svc.AgentStatus()
	if err != nil {
		t.Fatal(err)
	}
	var view AgentView
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}
	if !view.PlanOwnedByHost || view.PlanObservable {
		t.Error("Agent 接入页应固定计划归属与不可观察语义")
	}
}
