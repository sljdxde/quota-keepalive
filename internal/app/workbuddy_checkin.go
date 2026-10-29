package app

import (
	"context"
	"errors"
	"fmt"

	"qka/internal/accounts"
	"qka/internal/capabilities"
	"qka/internal/engine"
	"qka/internal/workbuddy"
)

// runWorkBuddyCheckin uses the verified managed source and the official
// status-first check-in protocol. It never falls back to the Bun adapter's
// placeholder /api/checkin path.
func (a *App) runWorkBuddyCheckin(ctx context.Context, accountID string) (any, error) {
	account, err := a.accounts.Get(ctx, "workbuddy-cn", accountID)
	if err != nil {
		return nil, err
	}
	if !account.Enabled {
		return nil, fmt.Errorf("账号已停用: %s", accountID)
	}
	preferred, err := a.accounts.PreferredSource(ctx, "workbuddy-cn", accountID)
	if err != nil {
		return nil, err
	}
	if preferred.Kind != accounts.KindManaged || preferred.ReadOnly {
		return nil, errors.New("签到只支持受管来源；导入来源保持只读")
	}

	key := engine.Key{
		Target: "workbuddy-cn", AccountID: accountID,
		Operation: engine.Checkin,
	}
	var outcome workbuddy.CheckinResult
	exec := func(ctx context.Context, _ engine.Key) (engine.ExternalResult, error) {
		session, err := a.newWorkBuddyManagedSession(ctx, accountID, preferred.ID)
		if err != nil {
			return engine.ExternalResult{}, err
		}
		result, err := workbuddy.NewCheckinService(workbuddy.CheckinConfig{
			Session: session,
			BaseURL: workBuddyLoginBaseURL(a.cfg.BaseURLs),
		}).Checkin(ctx)
		if err != nil {
			return engine.ExternalResult{Kind: engine.RRetryable, Message: err.Error()}, nil
		}
		outcome = result
		switch result.Outcome {
		case workbuddy.CheckinDone:
			return engine.ExternalResult{Kind: engine.RAlready, Message: result.Message}, nil
		case workbuddy.CheckinIneligible:
			return engine.ExternalResult{Kind: engine.RIneligible, Message: result.Message}, nil
		case workbuddy.CheckinInactive:
			return engine.ExternalResult{Kind: engine.RInactive, Message: result.Message}, nil
		case workbuddy.CheckinClaimed:
			return engine.ExternalResult{Kind: engine.RSuccess, Message: result.Message}, nil
		default:
			return engine.ExternalResult{Kind: engine.RRetryable, Message: result.Message}, nil
		}
	}

	var obs engine.Observation
	defer func() {
		if obs.Executed {
			a.writeWorkBuddyCheckinLog(ctx, accountID, obs)
		}
	}()
	obs, err = a.engine.Run(ctx, key, exec)
	if err != nil {
		return obs, err
	}
	if !obs.Executed || outcome.Outcome != workbuddy.CheckinClaimed {
		return obs, nil
	}

	// A claim is only confirmed with an explicit receipt. A bare success is
	// recorded as "claimed, awaiting confirmation", never "credited".
	if !outcome.HasReceipt() {
		if err := a.engine.ReconcileState(ctx, key, engine.SignedProvisional,
			"已领取，待确认"); err != nil {
			return obs, err
		}
		obs.State = engine.SignedProvisional
		obs.Message = "已领取，待确认"
		return obs, nil
	}
	if obs.State == engine.SignedConfirmed {
		if err := a.engine.ReconcileState(ctx, key, engine.SignedConfirmed, "已到账"); err != nil {
			return obs, err
		}
		obs.Message = "已到账"
	}
	return obs, nil
}

// writeWorkBuddyCheckinLog records the final engine state, including a
// provisional downgrade after a claim without receipt. Logging is best-effort:
// a log write failure must not turn a successful upstream claim into a retry.
func (a *App) writeWorkBuddyCheckinLog(
	ctx context.Context,
	accountID string,
	obs engine.Observation,
) {
	if a.store == nil {
		return
	}
	level := "info"
	switch obs.State {
	case engine.FailedRetryable, engine.FailedFinal:
		level = "error"
	}
	svc := capabilities.NewLogService(
		a.store.DB(),
		capabilities.Grant{"log.write": true},
		a.now,
	)
	_ = svc.WriteWithState(ctx, capabilities.Namespace{
		Target: "workbuddy-cn", AccountID: accountID, Operation: "checkin",
	}, string(obs.State), level, obs.Message)
}
