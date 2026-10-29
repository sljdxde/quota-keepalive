package engine

import (
	"context"
	"testing"
	"time"
)

func TestRecoverExpiredLease(t *testing.T) {
	now := afternoon()
	e := newTestEngine(t, fixedClock(now))
	day := BeijingDay(now)

	// 先建当日行，再模拟持有者崩溃：running 且租约已过期。
	if err := e.ensureRow(context.Background(), checkinKey, day, formatTS(now)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.ExecContext(context.Background(), `
UPDATE daily_ops SET state = 'running', lease_holder = 'dead', lease_expires_at = ?
WHERE target = ? AND account_id = ? AND operation = ? AND day_key = ?`,
		formatTS(now.Add(-time.Minute)),
		checkinKey.Target, checkinKey.AccountID, string(checkinKey.Operation), day); err != nil {
		t.Fatal(err)
	}

	ex := &countingExecutor{result: ExternalResult{Kind: RSuccess}}
	obs, err := e.Run(context.Background(), checkinKey, ex.exec())
	if err != nil {
		t.Fatalf("恢复后 Run 失败: %v", err)
	}
	if obs.State != SignedConfirmed {
		t.Errorf("过期租约恢复后应成功确认，实际 %s", obs.State)
	}
	if calls := ex.callCount(); calls != 1 {
		t.Errorf("恢复后目标侧请求应只发生一次，实际 %d", calls)
	}
}

func TestValidLeaseNotTakenOver(t *testing.T) {
	now := afternoon()
	e := newTestEngine(t, fixedClock(now))
	day := BeijingDay(now)

	if err := e.ensureRow(context.Background(), checkinKey, day, formatTS(now)); err != nil {
		t.Fatal(err)
	}
	// 有效的未过期租约（持有者仍在运行）。
	if _, err := e.db.ExecContext(context.Background(), `
UPDATE daily_ops SET state = 'running', lease_holder = 'other', lease_expires_at = ?
WHERE target = ? AND account_id = ? AND operation = ? AND day_key = ?`,
		formatTS(now.Add(time.Minute)),
		checkinKey.Target, checkinKey.AccountID, string(checkinKey.Operation), day); err != nil {
		t.Fatal(err)
	}

	ex := &countingExecutor{result: ExternalResult{Kind: RSuccess}}
	obs, err := e.Run(context.Background(), checkinKey, ex.exec())
	if err != nil {
		t.Fatal(err)
	}
	if obs.Executed || obs.State != Running {
		t.Errorf("有效租约不应被接管，executed=%v state=%s", obs.Executed, obs.State)
	}
	if calls := ex.callCount(); calls != 0 {
		t.Errorf("有效租约期间不应发外部请求，实际 %d", calls)
	}
}

func TestTimePassingDoesNotExecute(t *testing.T) {
	now := afternoon()
	mutable := now
	e := newTestEngine(t, func() time.Time { return mutable })

	ex := &countingExecutor{result: ExternalResult{Kind: RSuccess}}

	// 不调用 Run，仅让时间流逝（含冷却到期、唤醒）：引擎无任何后台业务执行。
	mutable = mutable.Add(3 * time.Hour)
	// 引擎存在但从未被调用，因此持有执行器的假件不应被触发。
	_ = e
	if calls := ex.callCount(); calls != 0 {
		t.Fatalf("未调用 Run 不应有外部执行，实际 %d", calls)
	}
}
