package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"qka/internal/accounts"
	"qka/internal/capabilities"
	"qka/internal/engine"
	"qka/internal/providerruntime"
	"qka/internal/requestguard"
	"qka/internal/workbuddy"
)

var (
	getWorkBuddyAccount      = (*accounts.Service).Get
	preferredWorkBuddySource = (*accounts.Service).PreferredSource
	workBuddyCurrentState    = (*engine.Engine).CurrentState
)

// runWorkBuddyCheckin uses the verified managed source and the official
// status-first check-in protocol. It never falls back to the Bun adapter's
// placeholder /api/checkin path.
func (a *App) runWorkBuddyCheckin(ctx context.Context, accountID string) (any, error) {
	account, err := getWorkBuddyAccount(a.accounts, ctx, "workbuddy-cn", accountID)
	if err != nil {
		return nil, err
	}
	if !account.Enabled {
		return nil, fmt.Errorf("账号已停用: %s", accountID)
	}
	preferred, err := preferredWorkBuddySource(a.accounts, ctx, "workbuddy-cn", accountID)
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
	state, err := workBuddyCurrentState(a.engine, ctx, key)
	if err != nil {
		return nil, err
	}
	if state == engine.ManualRequired {
		return a.reviewWorkBuddyFallback(ctx, accountID, preferred, key)
	}
	var outcome workbuddy.CheckinResult
	exec := func(ctx context.Context, _ engine.Key) (engine.ExternalResult, error) {
		result, guardErr := requestguard.Do(ctx, a.riskGuard,
			"workbuddy-cn", accountID, "checkin", time.Minute,
			func() (workbuddy.CheckinResult, error) {
				session, err := a.newWorkBuddyManagedSession(ctx, accountID, preferred.ID)
				if err != nil {
					return workbuddy.CheckinResult{}, err
				}
				return workbuddy.NewCheckinService(workbuddy.CheckinConfig{
					Session: session,
					BaseURL: workBuddyLoginBaseURL(a.cfg.BaseURLs),
				}).Checkin(ctx)
			})
		if guardErr != nil {
			return engine.ExternalResult{Kind: engine.RRetryable, Message: guardErr.Error()}, nil
		}
		if a.riskGuard != nil {
			a.riskGuard.ObserveText("workbuddy-cn", accountID, result.Message)
			if result.Outcome == workbuddy.CheckinManualRequired {
				a.riskGuard.Record("workbuddy-cn", accountID, requestguard.ReasonChallenge)
			}
		}
		outcome = result
		return workBuddyCheckinExternalResult(result), nil
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
	if !obs.Executed {
		return obs, nil
	}

	// A claim is only confirmed with an explicit receipt. A bare success is
	// recorded as "claimed, awaiting confirmation", never "credited".
	if outcome.Outcome == workbuddy.CheckinClaimed && !outcome.HasReceipt() {
		if err := a.engine.ReconcileState(ctx, key, engine.SignedProvisional,
			"已领取，待确认"); err != nil {
			return obs, err
		}
		obs.State = engine.SignedProvisional
		obs.Message = "已领取，待确认"
	} else if outcome.Outcome == workbuddy.CheckinClaimed &&
		obs.State == engine.SignedConfirmed {
		if err := a.engine.ReconcileState(ctx, key, engine.SignedConfirmed, "已到账"); err != nil {
			return obs, err
		}
		obs.Message = "已到账"
	}
	if err := a.recordWorkBuddyUsage(ctx, key, &obs,
		optionalUsageValue(outcome.Credit, outcome.CreditKnown),
		optionalUsageValue(outcome.Balance, outcome.BalanceKnown)); err != nil {
		return obs, err
	}
	// Some successful check-in payloads only return a credit delta. Refresh the
	// read-only quota view immediately so the account card is not left blank.
	// A quota refresh failure never changes the confirmed check-in result.
	if !outcome.BalanceKnown &&
		(obs.State == engine.SignedConfirmed ||
			obs.State == engine.SignedProvisional ||
			obs.State == engine.Already) {
		if err := a.refreshWorkBuddyUsage(ctx, accountID); err != nil {
			message := obs.Message + "；当前额度暂不可读，可点击「刷新额度」重试"
			_ = a.engine.ReconcileState(ctx, key, obs.State, message)
		}
	}
	return obs, nil
}

func workBuddyCheckinExternalResult(result workbuddy.CheckinResult) engine.ExternalResult {
	switch result.Outcome {
	case workbuddy.CheckinDone:
		return engine.ExternalResult{Kind: engine.RAlready, Message: result.Message}
	case workbuddy.CheckinManualRequired:
		return engine.ExternalResult{Kind: engine.RManualRequired, Message: result.Message}
	case workbuddy.CheckinIneligible:
		return engine.ExternalResult{Kind: engine.RIneligible, Message: result.Message}
	case workbuddy.CheckinInactive:
		return engine.ExternalResult{Kind: engine.RInactive, Message: result.Message}
	case workbuddy.CheckinClaimed:
		return engine.ExternalResult{Kind: engine.RSuccess, Message: result.Message}
	default:
		return engine.ExternalResult{Kind: engine.RRetryable, Message: result.Message}
	}
}

// reviewWorkBuddyFallback is the same-day read-only path after the user has
// been sent to the official WorkBuddy app. It never calls daily-checkin.
func (a *App) reviewWorkBuddyFallback(
	ctx context.Context,
	accountID string,
	preferred accounts.Source,
	key engine.Key,
) (any, error) {
	session, err := a.newWorkBuddyManagedSession(ctx, accountID, preferred.ID)
	if err != nil {
		return nil, err
	}
	review, err := requestguard.Do(ctx, a.riskGuard,
		"workbuddy-cn", accountID, "checkin_review", 30*time.Second,
		func() (workbuddy.CheckinReview, error) {
			return workbuddy.NewCheckinService(workbuddy.CheckinConfig{
				Session: session,
				BaseURL: workBuddyLoginBaseURL(a.cfg.BaseURLs),
			}).Review(ctx)
		})
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if errors.Is(err, workbuddy.ErrManagedReauthRequired) {
			obs := engine.Observation{
				Target: key.Target, AccountID: accountID, Operation: key.Operation,
				Day: engine.BeijingDay(a.now()), State: engine.FailedRetryable,
				Executed: true, Message: "登录态已失效，请重新登录后再复查",
			}
			a.writeWorkBuddyCheckinLog(ctx, accountID, obs)
			return obs, nil
		}
		// A failed review cannot prove the user's result either way. Keep the
		// fallback state and make the retry visible without another claim.
		obs := engine.Observation{
			Target: key.Target, AccountID: accountID, Operation: key.Operation,
			Day: engine.BeijingDay(a.now()), State: engine.ManualRequired,
			Executed: true, Message: "复查暂不可用；请回 WorkBuddy App 签到后重试",
		}
		a.writeWorkBuddyCheckinLog(ctx, accountID, obs)
		return obs, nil
	}
	if review.CheckedIn {
		message := fmt.Sprintf(
			"只读复查观察到今日已领取（HTTP %d）", review.StatusCode)
		updated, err := a.engine.ReconcileStateIfCurrent(
			ctx, key, engine.ManualRequired, engine.Already, message)
		if err != nil {
			return nil, err
		}
		if !updated {
			state, err := workBuddyCurrentState(a.engine, ctx, key)
			if err != nil {
				return nil, err
			}
			if state != engine.Already {
				return engine.Observation{
					Target: key.Target, AccountID: accountID, Operation: key.Operation,
					Day: engine.BeijingDay(a.now()), State: state, Executed: true,
					Message: "复查期间状态已变化；未更新结果",
				}, nil
			}
		}
		obs := engine.Observation{
			Target: key.Target, AccountID: accountID, Operation: key.Operation,
			Day: engine.BeijingDay(a.now()), State: engine.Already,
			Executed: true, Message: message,
		}
		if err := a.recordReviewUsage(ctx, key, &obs, review); err != nil {
			return nil, err
		}
		a.writeWorkBuddyCheckinLog(ctx, accountID, obs)
		return obs, nil
	}
	if review.StatusCode == http.StatusUnauthorized {
		obs := engine.Observation{
			Target: key.Target, AccountID: accountID, Operation: key.Operation,
			Day: engine.BeijingDay(a.now()), State: engine.ManualRequired,
			Executed: true,
			Message: fmt.Sprintf(
				"复查请求被拒绝（HTTP %d），但余额查询仍可用；请回 WorkBuddy App 签到，完成后再次执行 checkin_run 复查",
				review.StatusCode),
		}
		if err := a.recordReviewUsage(ctx, key, &obs, review); err != nil {
			return nil, err
		}
		a.writeWorkBuddyCheckinLog(ctx, accountID, obs)
		return obs, nil
	}
	if !review.Active {
		obs := engine.Observation{
			Target: key.Target, AccountID: accountID, Operation: key.Operation,
			Day: engine.BeijingDay(a.now()), State: engine.ManualRequired,
			Executed: true,
			Message: fmt.Sprintf(
				"复查显示活动未开始或已结束（HTTP %d）；今日无需再次领取",
				review.StatusCode),
		}
		if err := a.recordReviewUsage(ctx, key, &obs, review); err != nil {
			return nil, err
		}
		a.writeWorkBuddyCheckinLog(ctx, accountID, obs)
		return obs, nil
	}
	obs := engine.Observation{
		Target: key.Target, AccountID: accountID, Operation: key.Operation,
		Day: engine.BeijingDay(a.now()), State: engine.ManualRequired,
		Executed: true,
		Message: fmt.Sprintf(
			"尚未观察到今日签到（HTTP %d）；请回 WorkBuddy App 签到，完成后再次执行 checkin_run 复查",
			review.StatusCode),
	}
	if err := a.recordReviewUsage(ctx, key, &obs, review); err != nil {
		return nil, err
	}
	a.writeWorkBuddyCheckinLog(ctx, accountID, obs)
	return obs, nil
}

func optionalUsageValue(value float64, known bool) *float64 {
	if !known {
		return nil
	}
	v := value
	return &v
}

func (a *App) recordWorkBuddyUsage(
	ctx context.Context,
	key engine.Key,
	obs *engine.Observation,
	credits, balance *float64,
) error {
	if credits == nil && balance == nil {
		return nil
	}
	if err := a.engine.RecordUsage(ctx, key, credits, balance); err != nil {
		return err
	}
	if err := a.recordWorkBuddyQuotaReading(ctx, key, balance); err != nil {
		return err
	}
	obs.Credits = credits
	obs.Balance = balance
	return nil
}

func (a *App) recordWorkBuddyQuotaReading(
	ctx context.Context,
	key engine.Key,
	balance *float64,
) error {
	if balance == nil || a.store == nil {
		return nil
	}
	value := *balance
	windows, _ := json.Marshal([]providerruntime.UsageWindow{{
		Name: "当前额度", Balance: &value,
		Display: strconv.FormatFloat(value, 'f', -1, 64),
	}})
	_, err := a.store.DB().ExecContext(ctx, `
INSERT INTO quota_readings
  (provider, account_id, observed_at, balance, windows_json)
VALUES (?, ?, ?, ?, ?)`,
		key.Target, key.AccountID, a.now().UTC().Format(time.RFC3339),
		strconv.FormatFloat(value, 'f', -1, 64), string(windows))
	return err
}

func (a *App) recordReviewUsage(
	ctx context.Context,
	key engine.Key,
	obs *engine.Observation,
	review workbuddy.CheckinReview,
) error {
	return a.recordWorkBuddyUsage(
		ctx, key, obs,
		optionalUsageValue(review.Credit, review.CreditKnown),
		optionalUsageValue(review.Balance, review.BalanceKnown),
	)
}

// refreshWorkBuddyUsage reads today's check-in status and quota without
// claiming. It is used by the account page's refresh action.
func (a *App) refreshWorkBuddyUsage(ctx context.Context, accountID string) error {
	account, err := getWorkBuddyAccount(a.accounts, ctx, "workbuddy-cn", accountID)
	if err != nil {
		return err
	}
	preferred, err := preferredWorkBuddySource(a.accounts, ctx, "workbuddy-cn", account.ID)
	if err != nil {
		return err
	}
	if preferred.Kind != accounts.KindManaged || preferred.ReadOnly {
		return errors.New("额度刷新只支持受管来源")
	}
	session, err := a.newWorkBuddyManagedSession(ctx, accountID, preferred.ID)
	if err != nil {
		return err
	}
	review, err := requestguard.Do(ctx, a.riskGuard,
		"workbuddy-cn", accountID, "refresh", 15*time.Second,
		func() (workbuddy.CheckinReview, error) {
			return workbuddy.NewCheckinService(workbuddy.CheckinConfig{
				Session: session,
				BaseURL: workBuddyLoginBaseURL(a.cfg.BaseURLs),
			}).Review(ctx)
		})
	if err != nil {
		return err
	}
	key := engine.Key{
		Target: "workbuddy-cn", AccountID: accountID,
		Operation: engine.Checkin,
	}
	obs := engine.Observation{}
	if err := a.recordReviewUsage(ctx, key, &obs, review); err != nil {
		return err
	}
	if !review.BalanceKnown {
		return errors.New("未读取到当前额度，请稍后重试")
	}
	return nil
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
