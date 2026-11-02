package adapterwiring

import (
	"context"
	"encoding/json"
	"time"

	"qka/internal/engine"
	"qka/internal/evidence"
)

// HostLike 是可派发动作的适配器宿主（*adapterhost.Host 满足）。
type HostLike interface {
	CallWith(ctx context.Context, op string, args map[string]any) (json.RawMessage, error)
}

type checkinEngine interface {
	Run(context.Context, engine.Key, engine.Executor) (engine.Observation, error)
	ReconcileState(context.Context, engine.Key, engine.State, string) error
}

// CheckinRunner 编排一次签到：引擎租约 + 适配器领取 + 证据门确认。
type CheckinRunner struct {
	eng      checkinEngine
	host     HostLike
	now      func() time.Time
	endpoint string
}

// NewCheckinRunner 组装签到编排。
func NewCheckinRunner(eng *engine.Engine, host HostLike, now func() time.Time, endpoint string) *CheckinRunner {
	if now == nil {
		now = time.Now
	}
	return &CheckinRunner{eng: eng, host: host, now: now, endpoint: endpoint}
}

// Run 执行签到并按证据决定 provisional/confirmed。
func (r *CheckinRunner) Run(ctx context.Context, key engine.Key) (engine.Observation, error) {
	var raw json.RawMessage
	exec := func(ctx context.Context, key engine.Key) (engine.ExternalResult, error) {
		result, err := r.host.CallWith(ctx, "checkin.run",
			map[string]any{"accountId": key.AccountID})
		if err != nil {
			return engine.ExternalResult{Kind: engine.RRetryable, Message: err.Error()}, nil
		}
		raw = result
		return engine.ExternalResult{Kind: engine.RSuccess, Message: string(result)}, nil
	}

	obs, err := r.eng.Run(ctx, key, exec)
	if err != nil {
		return obs, err
	}

	// 证据门：本次执行成功后，用证据复核（午后证据充分才 confirmed，否则停 provisional）。
	if obs.Executed && raw != nil {
		chain := evidence.Sanitize(EvidenceFromCheckin(raw, r.endpoint))
		decision := evidence.Decide(r.now(), chain)
		desired := engine.State(decision.State)
		// 引擎基线 message 是原始结果；状态或文案需修正时统一写回。
		if obs.State != desired || obs.Message != decision.Message {
			if err := r.eng.ReconcileState(ctx, key, desired, decision.Message); err != nil {
				return obs, err
			}
			obs.State = desired
			obs.Message = decision.Message
		}
	}
	return obs, nil
}
