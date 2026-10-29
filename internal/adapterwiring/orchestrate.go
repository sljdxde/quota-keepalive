package adapterwiring

import (
	"context"
	"encoding/json"

	"qka/internal/adapterhost"
	"qka/internal/engine"
)

// CheckinExecutor 把适配器 checkin.run 适配为引擎 Executor。
func CheckinExecutor(host *adapterhost.Host) engine.Executor {
	return func(ctx context.Context, key engine.Key) (engine.ExternalResult, error) {
		result, err := host.CallWith(ctx, "checkin.run",
			map[string]any{"accountId": key.AccountID})
		if err != nil {
			// 适配器调用失败：可重试，交给引擎状态机。
			return engine.ExternalResult{Kind: engine.RRetryable, Message: err.Error()}, nil
		}

		var data struct {
			Status string `json:"status"`
		}
		_ = json.Unmarshal(result, &data)

		kind := engine.RSuccess
		switch data.Status {
		case "already":
			kind = engine.RAlready
		case "none":
			kind = engine.RNone
		}
		return engine.ExternalResult{Kind: kind, Message: string(result)}, nil
	}
}

// KeepaliveExecutor 把适配器 keepalive.run 适配为引擎保活 Executor。
func KeepaliveExecutor(host HostLike) engine.Executor {
	return func(ctx context.Context, key engine.Key) (engine.ExternalResult, error) {
		result, err := host.CallWith(ctx, "keepalive.run",
			map[string]any{"accountId": key.AccountID})
		if err != nil {
			// 失败（含未收到真实回复）→ 可重试，进入冷却。
			return engine.ExternalResult{Kind: engine.RRetryable, Message: err.Error()}, nil
		}
		return engine.ExternalResult{Kind: engine.RSuccess, Message: string(result)}, nil
	}
}
