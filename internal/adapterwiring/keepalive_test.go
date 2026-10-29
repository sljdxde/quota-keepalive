package adapterwiring

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"qka/internal/engine"
)

func keepaliveKey() engine.Key {
	return engine.Key{Target: "workbuddy-cn", AccountID: "u_1", Operation: engine.Keepalive}
}

func TestKeepaliveSuccessWithRealReply(t *testing.T) {
	target := newFakeTarget(t, scenario{
		accounts:  healthyAccounts(),
		chatReply: "OK",
		chatCost:  0.03, // 非硬编码 0.02
	})
	defer target.Close()
	env := setupWiredAt(t, target.URL, func() time.Time { return beijingAt(14) })

	obs, err := env.eng.Run(context.Background(), keepaliveKey(),
		KeepaliveExecutor(env.host))
	if err != nil {
		t.Fatal(err)
	}
	if obs.State != engine.Completed {
		t.Fatalf("真实回复应 completed，实际 %s", obs.State)
	}

	var result struct {
		Cost  float64 `json:"cost"`
		Reply string  `json:"reply"`
	}
	if err := json.Unmarshal([]byte(obs.Message), &result); err != nil {
		t.Fatal(err)
	}
	if result.Cost != 0.03 {
		t.Errorf("成本应取自当前回执 0.03，实际 %v", result.Cost)
	}
	if result.Reply != "OK" {
		t.Errorf("应返回真实模型回复，实际 %q", result.Reply)
	}
}

func TestKeepaliveFailsWithoutRealReply(t *testing.T) {
	target := newFakeTarget(t, scenario{
		accounts:  healthyAccounts(),
		chatReply: "", // 空回复（伪造心跳/仅建连）
	})
	defer target.Close()
	env := setupWiredAt(t, target.URL, func() time.Time { return beijingAt(14) })

	obs, err := env.eng.Run(context.Background(), keepaliveKey(),
		KeepaliveExecutor(env.host))
	if err != nil {
		t.Fatal(err)
	}
	if obs.State != engine.FailedRetryable {
		t.Errorf("无真实回复应 failed_retryable，实际 %s", obs.State)
	}
}

func TestKeepaliveOncePerDay(t *testing.T) {
	target := newFakeTarget(t, scenario{
		accounts:  healthyAccounts(),
		chatReply: "OK",
		chatCost:  0.03,
	})
	defer target.Close()
	env := setupWiredAt(t, target.URL, func() time.Time { return beijingAt(14) })

	if _, err := env.eng.Run(context.Background(), keepaliveKey(),
		KeepaliveExecutor(env.host)); err != nil {
		t.Fatal(err)
	}

	// 再次调用：单日已完成，不执行、不重复消耗。
	obs2, err := env.eng.Run(context.Background(), keepaliveKey(),
		KeepaliveExecutor(env.host))
	if err != nil {
		t.Fatal(err)
	}
	if obs2.Executed {
		t.Error("单账号单北京日最多成功一次，不应再次执行")
	}
}

func TestCheckinAndKeepaliveIndependent(t *testing.T) {
	target := newFakeTarget(t, scenario{
		accounts: healthyAccounts(),
		// 签到有证据，保活有真实回复。
		checkinEvidence: true,
		chatReply:       "OK",
		chatCost:        0.03,
	})
	defer target.Close()
	env := setupWiredAt(t, target.URL, func() time.Time { return beijingAt(14) })

	// 同轮执行签到与保活。
	checkinObs, err := env.eng.Run(context.Background(), checkinKey(),
		CheckinExecutor(env.host))
	if err != nil {
		t.Fatal(err)
	}
	keepaliveObs, err := env.eng.Run(context.Background(), keepaliveKey(),
		KeepaliveExecutor(env.host))
	if err != nil {
		t.Fatal(err)
	}

	// 各自落库，互不掩盖。
	if checkinObs.State != engine.SignedConfirmed {
		t.Errorf("签到应 confirmed，实际 %s", checkinObs.State)
	}
	if keepaliveObs.State != engine.Completed {
		t.Errorf("保活应 completed，实际 %s", keepaliveObs.State)
	}

	var count int
	rows, _ := env.st.DB().QueryContext(context.Background(),
		`SELECT operation, state FROM daily_ops WHERE target = 'workbuddy-cn'`)
	defer rows.Close()
	for rows.Next() {
		count++
	}
	if count != 2 {
		t.Errorf("签到与保活应各落一行，实际 %d 行", count)
	}
}
