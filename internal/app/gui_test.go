package app

import (
	"testing"

	"qka/internal/workbuddy"
)

func TestWorkBuddyLoginDefaultsToOfficialOrigin(t *testing.T) {
	if got := workBuddyLoginBaseURL(nil); got != workbuddy.DefaultBaseURL {
		t.Fatalf("安装态应使用腾讯官方 WorkBuddy CN 入口，实际 %s", got)
	}
	if workBuddyLoginBaseURL(nil) == "https://workbuddy.example.com" {
		t.Fatal("安装态不得再使用 example.com")
	}
}

func TestWorkBuddyLoginBaseURLOverrideForControlledTarget(t *testing.T) {
	const fake = "http://127.0.0.1:12345"
	if got := workBuddyLoginBaseURL(map[string]string{"workbuddy-cn": fake}); got != fake {
		t.Fatalf("受控测试应可覆盖基址，实际 %s", got)
	}
}

func TestGUIExposesOnlyCompletedWorkBuddyTarget(t *testing.T) {
	if len(targetOrder) != 1 || targetOrder[0] != "workbuddy-cn" {
		t.Fatalf("MVP 界面只应展示已闭环的 WorkBuddy CN，实际 %v", targetOrder)
	}
}
