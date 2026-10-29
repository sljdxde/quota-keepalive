package engine

import (
	"context"
	"testing"
	"time"
)

func keepaliveKey() Key {
	return Key{Target: "workbuddy-cn", AccountID: "u_1", Operation: Keepalive}
}

func TestKeepaliveSuccessCompletesOnce(t *testing.T) {
	now := afternoon()
	e := newTestEngine(t, fixedClock(now))
	ex := &countingExecutor{result: ExternalResult{Kind: RSuccess, Message: "real reply"}}

	obs, err := e.Run(context.Background(), keepaliveKey(), ex.exec())
	if err != nil {
		t.Fatal(err)
	}
	if obs.State != Completed {
		t.Errorf("真实回复后期望 completed，实际 %s", obs.State)
	}

	// 同日再次调用：终态抑制，不重复执行。
	obs, _ = e.Run(context.Background(), keepaliveKey(), ex.exec())
	if obs.Executed || obs.State != Completed {
		t.Errorf("completed 后不应再执行，executed=%v state=%s", obs.Executed, obs.State)
	}
	if calls := ex.callCount(); calls != 1 {
		t.Errorf("保活单日应只执行一次，实际 %d", calls)
	}
}

func TestKeepaliveRetryableEntersCooldown(t *testing.T) {
	now := afternoon()
	mutable := now
	e := newTestEngine(t, func() time.Time { return mutable })

	ex := &countingExecutor{result: ExternalResult{Kind: RRetryable, Message: "transient"}}

	// 第一次：可重试失败。
	obs, err := e.Run(context.Background(), keepaliveKey(), ex.exec())
	if err != nil {
		t.Fatal(err)
	}
	if obs.State != FailedRetryable {
		t.Fatalf("期望 failed_retryable，实际 %s", obs.State)
	}

	// 立即重试：冷却中，不执行。
	obs, _ = e.Run(context.Background(), keepaliveKey(), ex.exec())
	if obs.Executed {
		t.Fatal("10 分钟冷却内不应执行外部请求")
	}
	if calls := ex.callCount(); calls != 1 {
		t.Fatalf("冷却内不应再调用，实际次数 %d", calls)
	}

	// 10 分钟后：冷却已过，可执行成功。
	mutable = mutable.Add(11 * time.Minute)
	ex.result = ExternalResult{Kind: RSuccess, Message: "real reply"}
	obs, err = e.Run(context.Background(), keepaliveKey(), ex.exec())
	if err != nil {
		t.Fatal(err)
	}
	if obs.State != Completed {
		t.Errorf("冷却后应成功 completed，实际 %s", obs.State)
	}
	if calls := ex.callCount(); calls != 2 {
		t.Errorf("冷却后应再执行一次，实际总次数 %d", calls)
	}
}

func TestKeepaliveIndependentFromCheckin(t *testing.T) {
	now := afternoon()
	e := newTestEngine(t, fixedClock(now))

	// 签到成功不应抑制保活。
	checkinEx := &countingExecutor{result: ExternalResult{Kind: RSuccess}}
	if _, err := e.Run(context.Background(), checkinKey, checkinEx.exec()); err != nil {
		t.Fatal(err)
	}

	keepaliveEx := &countingExecutor{result: ExternalResult{Kind: RSuccess}}
	obs, err := e.Run(context.Background(), keepaliveKey(), keepaliveEx.exec())
	if err != nil {
		t.Fatal(err)
	}
	if obs.State != Completed || !obs.Executed {
		t.Errorf("签到成功后保活仍应独立执行，state=%s executed=%v", obs.State, obs.Executed)
	}
}
