package engine

import (
	"testing"
	"time"
)

func morning() time.Time {
	return time.Date(2026, 10, 29, 10, 0, 0, 0, Beijing)
}

func afternoon() time.Time {
	return time.Date(2026, 10, 29, 14, 0, 0, 0, Beijing)
}

func TestCheckinTransitions(t *testing.T) {
	cases := []struct {
		name string
		now  time.Time
		kind ResultKind
		want State
	}{
		{"午前成功", morning(), RSuccess, SignedProvisional},
		{"午后成功", afternoon(), RSuccess, SignedConfirmed},
		{"已领取", morning(), RAlready, Already},
		{"活动未放量", morning(), RNone, None},
		{"无资格", morning(), RIneligible, Ineligible},
		{"活动结束", morning(), RInactive, Inactive},
		{"可重试失败", morning(), RRetryable, FailedRetryable},
		{"最终失败", morning(), RFinal, FailedFinal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Transition(Checkin, tc.kind, tc.now); got != tc.want {
				t.Errorf("期望 %s，实际 %s", tc.want, got)
			}
		})
	}
}

func TestKeepaliveTransitions(t *testing.T) {
	cases := []struct {
		name string
		kind ResultKind
		want State
	}{
		{"真实回复成功", RSuccess, Completed},
		{"可重试失败", RRetryable, FailedRetryable},
		{"最终失败", RFinal, FailedFinal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Transition(Keepalive, tc.kind, morning()); got != tc.want {
				t.Errorf("期望 %s，实际 %s", tc.want, got)
			}
		})
	}
}

func TestTerminalStatesSuppressReexecution(t *testing.T) {
	terminal := []State{SignedProvisional, SignedConfirmed, Already, Ineligible, Inactive, Completed}
	for _, s := range terminal {
		if !s.IsTerminal() {
			t.Errorf("%s 应抑制同日再次执行", s)
		}
	}
	nonTerminal := []State{Pending, Running, None, FailedRetryable, FailedFinal}
	for _, s := range nonTerminal {
		if s.IsTerminal() {
			t.Errorf("%s 不应是终态", s)
		}
	}
}
