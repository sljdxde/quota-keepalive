package workbuddy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
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
	Active     bool
	CheckedIn  bool
	StatusCode int
	Message    string
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
	Active         bool    `json:"active"`
	TodayCheckedIn bool    `json:"today_checked_in"`
	StreakDays     float64 `json:"streak_days"`
	DailyCredit    float64 `json:"daily_credit"`
	TodayCredit    float64 `json:"today_credit"`
}

type checkinClaim struct {
	Credit        float64 `json:"credit"`
	StreakDays    float64 `json:"streak_days"`
	IsStreakDay   bool    `json:"is_streak_day"`
	CreditDelayed bool    `json:"credit_delayed"`
	Message       string  `json:"message"`
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
		return CheckinResult{
			Outcome: CheckinDone, Credit: activity.TodayCredit,
			StreakDays: int(activity.StreakDays), Message: "今日已领取",
		}, nil
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
				return CheckinResult{Outcome: CheckinDone, Message: business.Message}, nil
			case 1002:
				return CheckinResult{Outcome: CheckinIneligible, Message: business.Message}, nil
			case 1003:
				return CheckinResult{Outcome: CheckinInactive, Message: business.Message}, nil
			}
		}
		return CheckinResult{}, err
	}
	return CheckinResult{
		Outcome: CheckinClaimed, Credit: claim.Credit,
		StreakDays: int(claim.StreakDays), Message: claim.Message,
		CreditDelayed: claim.CreditDelayed,
	}, nil
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
			if balanceErr := s.readBalance(ctx); balanceErr != nil {
				return CheckinReview{}, balanceErr
			}
			return CheckinReview{
				StatusCode: status, Message: "签到状态查询被拒绝",
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
	}
	return review, nil
}

func (s *CheckinService) classifyUnauthorized(ctx context.Context) (CheckinResult, error) {
	if err := s.readBalance(ctx); err != nil {
		return CheckinResult{}, s.classifyBalanceFailure(err)
	}
	return CheckinResult{
		Outcome: CheckinManualRequired,
		Message: "自动签到被 WorkBuddy 拒绝；请回 WorkBuddy App 签到，完成后再次执行 checkin_run 复查",
	}, nil
}

func (s *CheckinService) classifyBalanceFailure(err error) error {
	if errors.Is(err, ErrManagedReauthRequired) ||
		errors.Is(err, ErrManagedRetryable) {
		return err
	}
	return fmt.Errorf("%w: %v", ErrManagedRetryable, err)
}

func (s *CheckinService) readBalance(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.baseURL+"/billing/meter/get-user-resource-summary", bytes.NewReader([]byte("{}")))
	if err != nil {
		return err
	}
	setBaseHeaders(req)
	resp, err := s.session.Do(ctx, req)
	if err != nil {
		if errors.Is(err, ErrManagedReauthRequired) {
			return err
		}
		return s.classifyBalanceFailure(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		if err := s.session.MarkReauthRequired(ctx); err != nil {
			return fmt.Errorf("%w: 余额查询返回 HTTP %d；来源状态更新失败: %v",
				ErrManagedReauthRequired, resp.StatusCode, err)
		}
		return fmt.Errorf("%w: 余额查询返回 HTTP %d",
			ErrManagedReauthRequired, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: 余额查询返回 HTTP %d",
			ErrManagedRetryable, resp.StatusCode)
	}
	_, err = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil {
		return fmt.Errorf("%w: 余额响应无法读取", ErrManagedRetryable)
	}
	return nil
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
