package engine

import (
	"context"
	"sync"
	"testing"
	"time"

	"qka/internal/store"
)

func newTestEngine(t *testing.T, clock Clock) *Engine {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("打开 store 失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return New(st.DB(), clock, "test-holder")
}

func fixedClock(t time.Time) Clock {
	return func() time.Time { return t }
}

type countingExecutor struct {
	mu     sync.Mutex
	calls  int
	result ExternalResult
	err    error
}

func (c *countingExecutor) exec() Executor {
	return func(ctx context.Context, key Key) (ExternalResult, error) {
		c.mu.Lock()
		c.calls++
		c.mu.Unlock()
		return c.result, c.err
	}
}

func (c *countingExecutor) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

var checkinKey = Key{Target: "workbuddy-cn", AccountID: "u_1", Operation: Checkin}

func TestRunCheckinAfternoonConfirms(t *testing.T) {
	e := newTestEngine(t, fixedClock(afternoon()))
	ex := &countingExecutor{result: ExternalResult{Kind: RSuccess, Message: "ok"}}

	obs, err := e.Run(context.Background(), checkinKey, ex.exec())
	if err != nil {
		t.Fatalf("Run 失败: %v", err)
	}
	if obs.State != SignedConfirmed {
		t.Errorf("午后成功期望 signed_confirmed，实际 %s", obs.State)
	}
	if !obs.Executed || ex.callCount() != 1 {
		t.Errorf("应恰好执行一次外部请求，executed=%v count=%d", obs.Executed, ex.callCount())
	}
}

func TestRunCheckinMorningProvisional(t *testing.T) {
	e := newTestEngine(t, fixedClock(morning()))
	ex := &countingExecutor{result: ExternalResult{Kind: RSuccess}}

	obs, err := e.Run(context.Background(), checkinKey, ex.exec())
	if err != nil {
		t.Fatalf("Run 失败: %v", err)
	}
	if obs.State != SignedProvisional {
		t.Errorf("午前成功期望 signed_provisional，实际 %s", obs.State)
	}
}

func TestRunSkipsTerminalSameDay(t *testing.T) {
	e := newTestEngine(t, fixedClock(afternoon()))
	ex := &countingExecutor{result: ExternalResult{Kind: RSuccess}}

	if _, err := e.Run(context.Background(), checkinKey, ex.exec()); err != nil {
		t.Fatal(err)
	}
	// 当日再次调用：同一日期（固定时钟），终态抑制，不再发请求。
	obs, err := e.Run(context.Background(), checkinKey, ex.exec())
	if err != nil {
		t.Fatal(err)
	}
	if obs.Executed || obs.State != SignedConfirmed {
		t.Errorf("终态后应只读返回、不执行，executed=%v state=%s", obs.Executed, obs.State)
	}
	if ex.callCount() != 1 {
		t.Errorf("目标侧请求应只发生一次，实际 %d", ex.callCount())
	}
}

func TestRunCheckinResultKinds(t *testing.T) {
	cases := []struct {
		name string
		kind ResultKind
		want State
	}{
		{"already", RAlready, Already},
		{"none", RNone, None},
		{"retryable", RRetryable, FailedRetryable},
		{"final", RFinal, FailedFinal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEngine(t, fixedClock(morning()))
			ex := &countingExecutor{result: ExternalResult{Kind: tc.kind}}
			obs, err := e.Run(context.Background(), checkinKey, ex.exec())
			if err != nil {
				t.Fatal(err)
			}
			if obs.State != tc.want {
				t.Errorf("期望 %s，实际 %s", tc.want, obs.State)
			}
		})
	}
}

func TestRunNoneAndRetryableRemainEligibleNextDay(t *testing.T) {
	// 第 1 天 none，第 2 天（北京日期变化）可重新执行。
	day1 := time.Date(2026, 10, 28, 14, 0, 0, 0, Beijing)
	now := day1
	clock := func() time.Time { return now }
	e := newTestEngine(t, clock)

	ex := &countingExecutor{result: ExternalResult{Kind: RNone}}
	obs, _ := e.Run(context.Background(), checkinKey, ex.exec())
	if obs.State != None {
		t.Fatalf("期望 none，实际 %s", obs.State)
	}

	// 次日：活动放量，成功。
	now = day1.AddDate(0, 0, 1)
	ex.result = ExternalResult{Kind: RSuccess}
	obs, err := e.Run(context.Background(), checkinKey, ex.exec())
	if err != nil {
		t.Fatal(err)
	}
	if obs.State != SignedConfirmed {
		t.Errorf("none 次日应可执行成功，实际 state=%s executed=%v", obs.State, obs.Executed)
	}
}

func TestCurrentStateMissingRowIsPending(t *testing.T) {
	e := newTestEngine(t, fixedClock(afternoon()))
	state, err := e.CurrentState(context.Background(), checkinKey)
	if err != nil {
		t.Fatalf("缺失当日行不应报错: %v", err)
	}
	if state != Pending {
		t.Errorf("缺失当日行应为 pending，实际 %s", state)
	}
}

func TestCurrentStateReturnsDatabaseError(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := New(st.DB(), fixedClock(afternoon()), "test-holder")
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := e.CurrentState(context.Background(), checkinKey); err == nil {
		t.Fatal("数据库读取失败必须返回错误，不能伪装成 pending")
	}
}

func TestReconcileStateIfCurrentOnlyAppliesExpectedState(t *testing.T) {
	e := newTestEngine(t, fixedClock(afternoon()))
	ex := &countingExecutor{result: ExternalResult{Kind: RManualRequired}}
	if _, err := e.Run(context.Background(), checkinKey, ex.exec()); err != nil {
		t.Fatal(err)
	}
	updated, err := e.ReconcileStateIfCurrent(
		context.Background(), checkinKey, ManualRequired, Already, "已领取")
	if err != nil || !updated {
		t.Fatalf("当前状态匹配时应更新，updated=%v err=%v", updated, err)
	}
	updated, err = e.ReconcileStateIfCurrent(
		context.Background(), checkinKey, ManualRequired, Already, "再次更新")
	if err != nil || updated {
		t.Fatalf("当前状态已变化时不得覆盖，updated=%v err=%v", updated, err)
	}
}
