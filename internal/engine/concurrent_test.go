package engine

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestConcurrentRunOnlyOneExternalRequest(t *testing.T) {
	e := newTestEngine(t, fixedClock(afternoon()))
	ex := &countingExecutor{result: ExternalResult{Kind: RSuccess}}

	// 让持有租约的执行持续一小段时间，把其余调用挡在 running 上。
	exec := Executor(func(ctx context.Context, key Key) (ExternalResult, error) {
		time.Sleep(20 * time.Millisecond)
		return ex.exec()(ctx, key)
	})

	const n = 10
	var wg sync.WaitGroup
	type res struct {
		obs Observation
		err error
	}
	resCh := make(chan res, n)
	start := make(chan struct{})

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			obs, err := e.Run(context.Background(), checkinKey, exec)
			resCh <- res{obs, err}
		}()
	}

	close(start)
	wg.Wait()
	close(resCh)

	if calls := ex.callCount(); calls != 1 {
		t.Fatalf("并发下目标侧请求应只发生一次，实际 %d", calls)
	}

	executedSuccess := 0
	for r := range resCh {
		if r.err != nil {
			t.Fatalf("并发调用报错: %v", r.err)
		}
		// 后到的调用读到最终 confirmed 是连贯状态报告；真正执行的成功只能有一个。
		if r.obs.Executed && r.obs.State == SignedConfirmed {
			executedSuccess++
		}
	}
	if executedSuccess != 1 {
		t.Errorf("真正执行的同日成功应只有一个，实际 %d", executedSuccess)
	}
}
