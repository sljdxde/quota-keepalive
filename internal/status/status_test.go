package status

import (
	"context"
	"testing"
	"time"

	"qka/internal/accounts"
	"qka/internal/engine"
	"qka/internal/store"
)

func fixedNow() time.Time {
	return time.Date(2026, 10, 29, 14, 0, 0, 0, time.UTC)
}

func setup(t *testing.T) (*store.Store, *Service) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	acc := accounts.NewService(st.DB())
	err = acc.Discover(context.Background(), "qoder-cn", []accounts.DiscoveredAccount{
		{ID: "q_1", DisplayName: "Qoder One", MaskedIdentity: "****4321",
			SourceID: "qs_1", SourceKind: accounts.KindManaged},
		{ID: "q_2", DisplayName: "Qoder Two", MaskedIdentity: "****8765",
			SourceID: "qs_2", SourceKind: accounts.KindManaged},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"q_1", "q_2"} {
		if err := acc.SetEnabled(context.Background(), "qoder-cn", id, true); err != nil {
			t.Fatal(err)
		}
	}

	svc := NewService(st.DB(), func() time.Time { return fixedNow() },
		func(string) []string { return []string{"checkin", "keepalive"} })
	return st, svc
}

func seedOp(t *testing.T, st *store.Store, account, op, state string) {
	t.Helper()
	day := engine.BeijingDay(fixedNow())
	_, err := st.DB().ExecContext(context.Background(),
		`INSERT INTO daily_ops (target, account_id, operation, day_key, state, updated_at)
VALUES ('qoder-cn', ?, ?, ?, ?, ?)`,
		account, op, day, state, "2026-10-29T14:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
}

func TestStatusReportsToday(t *testing.T) {
	st, svc := setup(t)
	// q_1 今日已确认；q_2 今日待复核。
	seedOp(t, st, "q_1", "checkin", "signed_confirmed")
	seedOp(t, st, "q_2", "checkin", "signed_provisional")

	view, err := svc.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !view.Enabled {
		t.Error("存在启用账号应标记 enabled")
	}
	if len(view.Accounts) != 2 {
		t.Fatalf("应含 2 个账号，实际 %d", len(view.Accounts))
	}

	states := map[string]string{}
	for _, a := range view.Accounts {
		if len(a.Operations) != 1 {
			t.Fatalf("每账号应含 1 个操作，实际 %d", len(a.Operations))
		}
		states[a.AccountID] = a.Operations[0].State
	}
	if states["q_1"] != "signed_confirmed" || states["q_2"] != "signed_provisional" {
		t.Errorf("今日状态错误: %v", states)
	}
}

func TestStatusPendingReasons(t *testing.T) {
	st, svc := setup(t)
	seedOp(t, st, "q_2", "checkin", "signed_provisional")

	view, _ := svc.Status(context.Background())
	for _, a := range view.Accounts {
		if a.AccountID == "q_2" && len(a.PendingReasons) == 0 {
			t.Error("provisional 应给出待处理原因")
		}
	}
}

func TestPlanValidateReportsConflictsAndCooldown(t *testing.T) {
	st, svc := setup(t)
	// q_1 已终态（同日已终态冲突）；q_2 保活处于冷却。
	seedOp(t, st, "q_1", "checkin", "signed_confirmed")
	day := engine.BeijingDay(fixedNow())
	_, _ = st.DB().ExecContext(context.Background(),
		`INSERT INTO daily_ops (target, account_id, operation, day_key, state, cooldown_until, updated_at)
VALUES ('qoder-cn', 'q_2', 'keepalive', ?, 'failed_retryable', '2026-10-29T23:00:00Z', 'ts')`,
		day)

	plan, err := svc.PlanValidate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Items) == 0 {
		t.Fatal("应预演出计划项")
	}

	itemByKey := map[string]PlanItem{}
	for _, it := range plan.Items {
		itemByKey[it.AccountID+"/"+it.Operation] = it
	}

	// q_1 签到已终态 → 冲突。
	q1 := itemByKey["q_1/checkin"]
	if len(q1.Conflicts) == 0 {
		t.Error("已终态应报告同日已终态冲突")
	}

	// q_2 保活冷却中。
	q2Keep := itemByKey["q_2/keepalive"]
	if !q2Keep.Cooldown {
		t.Error("冷却中应标记 cooldown")
	}
}

func TestPlanValidateZeroWrites(t *testing.T) {
	st, svc := setup(t)

	var before int
	_ = st.DB().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM daily_ops`).Scan(&before)

	if _, err := svc.PlanValidate(context.Background()); err != nil {
		t.Fatal(err)
	}

	var after int
	_ = st.DB().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM daily_ops`).Scan(&after)
	if after != before {
		t.Errorf("plan_validate 不得写入，执行前 %d 行后 %d 行", before, after)
	}
}

func TestPlanValidateOmitsDisabledAccounts(t *testing.T) {
	st, svc := setup(t)
	acc := accounts.NewService(st.DB())
	if err := acc.SetEnabled(context.Background(), "qoder-cn", "q_1", false); err != nil {
		t.Fatal(err)
	}

	plan, err := svc.PlanValidate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Items) == 0 {
		t.Fatal("启用的 q_2 仍应进入计划")
	}
	for _, item := range plan.Items {
		if item.AccountID == "q_1" {
			t.Fatalf("停用账号不得进入执行计划: %+v", item)
		}
	}
}

func TestPlanValidateTreatsCheckinRefusalsAsTerminal(t *testing.T) {
	for _, state := range []string{"already", "ineligible", "inactive"} {
		t.Run(state, func(t *testing.T) {
			st, svc := setup(t)
			seedOp(t, st, "q_1", "checkin", state)
			plan, err := svc.PlanValidate(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range plan.Items {
				if item.AccountID == "q_1" && item.Operation == "checkin" {
					if item.Transition != "no_change" {
						t.Errorf("签到结果 %s 当日应抑制重试，实际 %+v", state, item)
					}
				}
			}
		})
	}
}

func TestStatusHasNoScheduleFields(t *testing.T) {
	_, svc := setup(t)
	view, _ := svc.Status(context.Background())
	out := mustJSON(view)
	if containsAny(out, "nextRun", "schedule", "cron", "fallback") {
		t.Errorf("status 不得含计划字段，实际 %s", out)
	}
}
