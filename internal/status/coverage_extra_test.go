package status

import (
	"context"
	"testing"

	"qka/internal/accounts"
)

// TestCostHintMapping 覆盖操作成本提示的三种取值。
func TestCostHintMapping(t *testing.T) {
	if got := costHint("keepalive"); got != 0.02 {
		t.Fatalf("keepalive cost = %v", got)
	}
	for _, op := range []string{"checkin", "refresh", ""} {
		if got := costHint(op); got != 0 {
			t.Fatalf("%q cost = %v, want 0", op, got)
		}
	}
}

// TestPlanValidateSkipsDisabledTaskPreference 覆盖账号级任务开关关闭后
// 预演直接跳过该操作的分支。
func TestPlanValidateSkipsDisabledTaskPreference(t *testing.T) {
	st, svc := setup(t)
	acc := accounts.NewService(st.DB())
	if err := acc.SetTaskEnabled(context.Background(), "qoder-cn", "q_1", "checkin", false); err != nil {
		t.Fatal(err)
	}

	plan, err := svc.PlanValidate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, item := range plan.Items {
		found[item.AccountID+"/"+item.Operation] = true
	}
	if found["q_1/checkin"] {
		t.Fatalf("关闭 checkin 自动任务的账号不应出现在预演里: %+v", plan.Items)
	}
	if !found["q_2/checkin"] {
		t.Fatalf("其他启用账号的 checkin 应保留: %+v", plan.Items)
	}
}
