package workbuddy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// CheckinOutcome is a stable, credential-free result for one WorkBuddy CN
// check-in. Only claimed and done mean today's benefit is accounted for.
type CheckinOutcome string

const (
	CheckinClaimed        CheckinOutcome = "claimed"
	CheckinDone           CheckinOutcome = "done"
	CheckinIneligible     CheckinOutcome = "ineligible"
	CheckinInactive       CheckinOutcome = "inactive"
	CheckinManualRequired CheckinOutcome = "manual_required"
	CheckinFailed         CheckinOutcome = "failed"
)

// CheckinResult is the user-visible result. Credit is the upstream receipt
// for this claim, never a hard-coded campaign value.
type CheckinResult struct {
	Outcome       CheckinOutcome `json:"outcome"`
	Credit        float64        `json:"credit,omitempty"`
	CreditKnown   bool           `json:"credit_known,omitempty"`
	Balance       float64        `json:"balance,omitempty"`
	BalanceKnown  bool           `json:"balance_known,omitempty"`
	StreakDays    int            `json:"streak_days,omitempty"`
	Message       string         `json:"message,omitempty"`
	CreditDelayed bool           `json:"credit_delayed,omitempty"`
}

// HasReceipt reports whether the claim itself carries a non-zero credit or
// an explicit upstream message. A delayed credit is not proof of arrival,
// even when the upstream also includes a message.
func (r CheckinResult) HasReceipt() bool {
	return !r.CreditDelayed && (r.Credit > 0 || strings.TrimSpace(r.Message) != "")
}

// CheckinReview is a read-only observation of today's check-in campaign.
// It never claims and therefore cannot prove a new credit by itself.
type CheckinReview struct {
	Active       bool
	CheckedIn    bool
	Credit       float64
	CreditKnown  bool
	Balance      float64
	BalanceKnown bool
	StatusCode   int
	Message      string
}

// CheckinConfig injects the refresh-capable managed request session.
type CheckinConfig struct {
	Session *ManagedSession
	BaseURL string
}

// CheckinService queries the campaign and claims at most once.
type CheckinService struct {
	session *ManagedSession
	baseURL string
}

// NewCheckinService constructs the WorkBuddy CN check-in service.
func NewCheckinService(cfg CheckinConfig) *CheckinService {
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &CheckinService{session: cfg.Session, baseURL: baseURL}
}

type checkinActivity struct {
	Active         bool           `json:"active"`
	TodayCheckedIn bool           `json:"today_checked_in"`
	StreakDays     float64        `json:"streak_days"`
	DailyCredit    float64        `json:"daily_credit"`
	TodayCredit    optionalNumber `json:"today_credit"`
}

type checkinClaim struct {
	Credit        optionalNumber `json:"credit"`
	StreakDays    float64        `json:"streak_days"`
	IsStreakDay   bool           `json:"is_streak_day"`
	CreditDelayed bool           `json:"credit_delayed"`
	Message       string         `json:"message"`
}

type optionalNumber struct {
	Value float64
	Set   bool
}

func (n *optionalNumber) UnmarshalJSON(raw []byte) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err == nil {
		n.Value, n.Set = value, true
		return nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return fmt.Errorf("额度字段格式无效")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return fmt.Errorf("额度字段格式无效")
	}
	n.Value, n.Set = value, true
	return nil
}

// BalanceSummary is the whitelisted quota projection of WorkBuddy's resource
// summary. It never retains package identifiers or raw response fields.
type BalanceSummary struct {
	Remaining float64
	Known     bool
}

type resourcePackage struct {
	CycleTotalCapacity  optionalNumber `json:"CycleTotalCapacity"`
	CycleUsedCapacity   optionalNumber `json:"CycleUsedCapacity"`
	CycleRemainCapacity optionalNumber `json:"CycleRemainCapacity"`
}

func summarizeBalance(packages []resourcePackage) BalanceSummary {
	var total, used, walletRemain float64
	known := false
	for _, pkg := range packages {
		if pkg.CycleTotalCapacity.Set {
			known = true
			total += pkg.CycleTotalCapacity.Value
			if pkg.CycleUsedCapacity.Set {
				used += pkg.CycleUsedCapacity.Value
			}
			continue
		}
		if pkg.CycleRemainCapacity.Set {
			known = true
			walletRemain += pkg.CycleRemainCapacity.Value
		}
	}
	remaining := total - used + walletRemain
	if remaining < 0 {
		remaining = 0
	}
	return BalanceSummary{Remaining: remaining, Known: known}
}

// Checkin performs the status-first, claim-once WorkBuddy CN check-in.
func (s *CheckinService) Checkin(ctx context.Context) (CheckinResult, error) {
	if s.session == nil {
		return CheckinResult{}, errors.New("WorkBuddy 受管会话未接线")
	}

	var activity checkinActivity
	status := http.StatusOK
	err := s.call(ctx, "/v2/billing/meter/checkin-activity-status", &activity, &status)
	if err != nil {
		if status == http.StatusUnauthorized {
			return s.classifyUnauthorized(ctx)
		}
		return CheckinResult{}, err
	}
	switch {
	case !activity.Active:
		return CheckinResult{Outcome: CheckinInactive, Message: "活动未开始或已结束"}, nil
	case activity.TodayCheckedIn:
		return s.withBalance(ctx, CheckinResult{
			Outcome: CheckinDone, Credit: activity.TodayCredit.Value,
			CreditKnown: activity.TodayCredit.Set,
			StreakDays:  int(activity.StreakDays), Message: "今日已领取",
		}), nil
	}

	var claim checkinClaim
	err = s.call(ctx, "/v2/billing/meter/daily-checkin", &claim, &status)
	if err != nil {
		if status == http.StatusUnauthorized {
			return s.classifyUnauthorized(ctx)
		}
		var business *checkinBusinessError
		if errors.As(err, &business) {
			switch business.Code {
			case 1001:
				return s.withBalance(ctx, CheckinResult{
					Outcome: CheckinDone, Message: business.Message,
				}), nil
			case 1002:
				return CheckinResult{Outcome: CheckinIneligible, Message: business.Message}, nil
			case 1003:
				return CheckinResult{Outcome: CheckinInactive, Message: business.Message}, nil
			}
		}
		return CheckinResult{}, err
	}
	return s.withBalance(ctx, CheckinResult{
		Outcome: CheckinClaimed, Credit: claim.Credit.Value,
		CreditKnown: claim.Credit.Set,
		StreakDays:  int(claim.StreakDays), Message: claim.Message,
		CreditDelayed: claim.CreditDelayed,
	}), nil
}

// Review checks only today's activity status. It is the safe retry path after
// the user signs in through the official WorkBuddy app.
func (s *CheckinService) Review(ctx context.Context) (CheckinReview, error) {
	if s.session == nil {
		return CheckinReview{}, errors.New("WorkBuddy 受管会话未接线")
	}
	var activity checkinActivity
	status := http.StatusOK
	err := s.call(ctx, "/v2/billing/meter/checkin-activity-status", &activity, &status)
	if err != nil {
		if status == http.StatusUnauthorized {
			balance, balanceErr := s.readBalance(ctx)
			if balanceErr != nil {
				return CheckinReview{}, balanceErr
			}
			return CheckinReview{
				StatusCode: status, Message: "签到状态查询被拒绝",
				Balance: balance.Remaining, BalanceKnown: balance.Known,
			}, nil
		}
		return CheckinReview{}, err
	}
	review := CheckinReview{
		Active: activity.Active, CheckedIn: activity.TodayCheckedIn,
		StatusCode: status, Message: "今日尚未领取",
	}
	if !activity.Active {
		review.Message = "活动未开始或已结束"
	}
	if activity.TodayCheckedIn {
		review.Message = "今日已领取"
		review.Credit = activity.TodayCredit.Value
		review.CreditKnown = activity.TodayCredit.Set
	}
	if balance := s.readBalanceOptional(ctx); balance.Known {
		review.Balance = balance.Remaining
		review.BalanceKnown = balance.Known
	}
	return review, nil
}

func (s *CheckinService) classifyUnauthorized(ctx context.Context) (CheckinResult, error) {
	balance, err := s.readBalance(ctx)
	if err != nil {
		return CheckinResult{}, s.classifyBalanceFailure(err)
	}
	return CheckinResult{
		Outcome: CheckinManualRequired,
		Message: "自动签到被 WorkBuddy 拒绝；请回 WorkBuddy App 签到，完成后再次执行 checkin_run 复查",
		Balance: balance.Remaining, BalanceKnown: balance.Known,
	}, nil
}

func (s *CheckinService) classifyBalanceFailure(err error) error {
	if errors.Is(err, ErrManagedReauthRequired) ||
		errors.Is(err, ErrManagedRetryable) {
		return err
	}
	return fmt.Errorf("%w: %v", ErrManagedRetryable, err)
}

func (s *CheckinService) withBalance(
	ctx context.Context,
	result CheckinResult,
) CheckinResult {
	balance := s.readBalanceOptional(ctx)
	if balance.Known {
		result.Balance = balance.Remaining
		result.BalanceKnown = balance.Known
	}
	return result
}

func (s *CheckinService) readBalance(ctx context.Context) (BalanceSummary, error) {
	return s.readBalanceRequest(ctx, true)
}

func (s *CheckinService) readBalanceOptional(ctx context.Context) BalanceSummary {
	balance, _ := s.readBalanceRequest(ctx, false)
	return balance
}

func (s *CheckinService) readBalanceRequest(
	ctx context.Context,
	strict bool,
) (BalanceSummary, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.baseURL+"/billing/meter/get-user-resource-summary", bytes.NewReader([]byte("{}")))
	if err != nil {
		return BalanceSummary{}, err
	}
	setBaseHeaders(req)
	var resp *http.Response
	if strict {
		resp, err = s.session.Do(ctx, req)
	} else {
		resp, err = s.session.DoAllowUnauthorized(ctx, req)
	}
	if err != nil {
		if errors.Is(err, ErrManagedReauthRequired) {
			return BalanceSummary{}, err
		}
		return BalanceSummary{}, s.classifyBalanceFailure(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		if !strict {
			return BalanceSummary{}, fmt.Errorf("%w: 余额查询返回 HTTP %d",
				ErrManagedReauthRequired, resp.StatusCode)
		}
		if err := s.session.MarkReauthRequired(ctx); err != nil {
			return BalanceSummary{}, fmt.Errorf("%w: 余额查询返回 HTTP %d；来源状态更新失败: %v",
				ErrManagedReauthRequired, resp.StatusCode, err)
		}
		return BalanceSummary{}, fmt.Errorf("%w: 余额查询返回 HTTP %d",
			ErrManagedReauthRequired, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return BalanceSummary{}, fmt.Errorf("%w: 余额查询返回 HTTP %d",
			ErrManagedRetryable, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil || len(body) > maxResponseSize {
		return BalanceSummary{}, fmt.Errorf("%w: 余额响应无法读取", ErrManagedRetryable)
	}
	var env struct {
		Code json.RawMessage `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return BalanceSummary{}, fmt.Errorf("%w: 余额响应格式无效", ErrManagedRetryable)
	}
	if code, ok := refreshBusinessCode(env.Code); ok && code != 0 {
		return BalanceSummary{}, fmt.Errorf("%w: 余额查询业务码 %d",
			ErrManagedRetryable, code)
	}
	var data struct {
		Packages []resourcePackage `json:"Packages"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		return BalanceSummary{}, fmt.Errorf("%w: 余额响应格式无效", ErrManagedRetryable)
	}
	return summarizeBalance(data.Packages), nil
}

type checkinBusinessError struct {
	Code    int
	Message string
}

func (e *checkinBusinessError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return "WorkBuddy 签到业务失败"
}

func (s *CheckinService) call(ctx context.Context, path string, dst any, status *int) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+path,
		bytes.NewReader([]byte("{}")))
	if err != nil {
		return err
	}
	setBaseHeaders(req)
	resp, err := s.session.DoAllowUnauthorized(ctx, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	*status = resp.StatusCode

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil || len(body) > maxResponseSize {
		return errors.New("WorkBuddy 签到响应无法读取")
	}
	var env struct {
		Code json.RawMessage `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	_ = json.Unmarshal(body, &env)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		code, ok := refreshBusinessCode(env.Code)
		if ok && code != 0 {
			return &checkinBusinessError{Code: code, Message: env.Msg}
		}
		return errors.New("WorkBuddy 签到接口返回非 2xx")
	}
	code, hasCode := refreshBusinessCode(env.Code)
	if hasCode && code != 0 {
		return &checkinBusinessError{Code: code, Message: env.Msg}
	}
	if len(env.Data) == 0 || string(env.Data) == "null" {
		return errors.New("WorkBuddy 签到响应缺少数据")
	}
	if err := json.Unmarshal(env.Data, dst); err != nil {
		return errors.New("WorkBuddy 签到响应格式无效")
	}
	return nil
}
