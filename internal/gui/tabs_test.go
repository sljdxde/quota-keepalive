package gui

import "testing"

func TestTabOrderFixedSixTabs(t *testing.T) {
	want := []string{"apps", "checkin", "speed", "pet", "logs", "settings"}
	if len(TabOrder) != 6 {
		t.Fatalf("主窗口应恰为六页签，实际 %d", len(TabOrder))
	}
	for i, tab := range TabOrder {
		if tab.ID != want[i] {
			t.Errorf("页签顺序错误：位置 %d 期望 %s，实际 %s", i, want[i], tab.ID)
		}
	}
}

func TestBadgeClass(t *testing.T) {
	cases := map[string]string{
		"failed_retryable":   "failed",
		"failed_final":       "failed",
		"signed_confirmed":   "ok",
		"completed":          "ok",
		"signed_provisional": "pending",
	}
	for state, want := range cases {
		if got := BadgeClass(state); got != want {
			t.Errorf("状态 %s 应映射 %s，实际 %s", state, want, got)
		}
	}
}
