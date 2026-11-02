// Package requestguard serializes provider account operations and applies
// conservative cooldowns after authentication, rate-limit, challenge, and
// upstream failures. It never retries automatically.
package requestguard

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Reason identifies the upstream response class that caused a cooldown.
type Reason string

const (
	ReasonAuth      Reason = "auth"
	ReasonRateLimit Reason = "rate_limit"
	ReasonChallenge Reason = "challenge"
	ReasonUpstream  Reason = "upstream"
)

// BlockedError reports an account-scoped cooling period.
type BlockedError struct {
	Provider string
	Account  string
	Reason   Reason
	Until    time.Time
}

func (e *BlockedError) Error() string {
	label := map[Reason]string{
		ReasonAuth:      "登录态或权限被拒绝",
		ReasonRateLimit: "远端限流",
		ReasonChallenge: "触发验证或风控",
		ReasonUpstream:  "远端暂时异常",
	}[e.Reason]
	if label == "" {
		label = "请求冷却中"
	}
	if e.Until.IsZero() {
		return "操作已暂停：" + label
	}
	return fmt.Sprintf("操作已暂停：%s，冷却至 %s",
		label, e.Until.Local().Format("15:04"))
}

// Guard is safe for concurrent use.
type Guard struct {
	mu     sync.Mutex
	now    func() time.Time
	scopes map[string]*scopeState
}

type scopeState struct {
	gate       chan struct{}
	cooldown   time.Time
	reason     Reason
	failures   int
	operations map[string]*operationState
}

type operationState struct {
	lastStarted time.Time
	lastResult  any
	hasResult   bool
}

// New creates a guard. A nil clock uses time.Now.
func New(now func() time.Time) *Guard {
	if now == nil {
		now = time.Now
	}
	return &Guard{now: now, scopes: map[string]*scopeState{}}
}

// Do executes fn at most once for one provider/account/operation inside the
// minimum interval. Concurrent calls for the same account are serialized and
// identical calls share the most recent successful result. No automatic retry
// is performed.
func Do[T any](
	ctx context.Context,
	guard *Guard,
	provider, account, operation string,
	minInterval time.Duration,
	fn func() (T, error),
) (T, error) {
	var zero T
	if guard == nil {
		return fn()
	}
	state := guard.scope(provider, account)
	select {
	case state.gate <- struct{}{}:
		defer func() { <-state.gate }()
	case <-ctx.Done():
		return zero, ctx.Err()
	}

	now := guard.now()
	guard.mu.Lock()
	if state.cooldown.After(now) {
		blocked := &BlockedError{
			Provider: provider, Account: account,
			Reason: state.reason, Until: state.cooldown,
		}
		guard.mu.Unlock()
		return zero, blocked
	}
	op := state.operations[operation]
	if op == nil {
		op = &operationState{}
		state.operations[operation] = op
	}
	if op.hasResult && minInterval > 0 && now.Sub(op.lastStarted) < minInterval {
		result, _ := op.lastResult.(T)
		guard.mu.Unlock()
		return result, nil
	}
	op.lastStarted = now
	guard.mu.Unlock()

	result, err := fn()
	if err != nil {
		guard.ObserveError(provider, account, err)
		return result, err
	}

	guard.mu.Lock()
	op.lastStarted = guard.now()
	op.lastResult = result
	op.hasResult = true
	state.failures = 0
	state.cooldown = time.Time{}
	state.reason = ""
	guard.mu.Unlock()
	return result, nil
}

// ObserveError records a cooldown when the error proves an upstream safety
// condition. Generic application errors deliberately do not cool down.
func (g *Guard) ObserveError(provider, account string, err error) {
	if g == nil || err == nil {
		return
	}
	reason := Classify(err.Error())
	if reason != "" {
		g.Record(provider, account, reason)
	}
}

// ObserveText applies the same classifier to outcome text returned without an
// error, such as "captcha required" or "HTTP 429".
func (g *Guard) ObserveText(provider, account, text string) {
	if g == nil {
		return
	}
	if reason := Classify(text); reason != "" {
		g.Record(provider, account, reason)
	}
}

// ObserveStatus records an HTTP status observed by a caller that returns a
// structured response instead of an error.
func (g *Guard) ObserveStatus(provider, account string, status int) {
	if g == nil {
		return
	}
	switch {
	case status == 401 || status == 403:
		g.Record(provider, account, ReasonAuth)
	case status == 429:
		g.Record(provider, account, ReasonRateLimit)
	case status == 408 || status >= 500:
		g.Record(provider, account, ReasonUpstream)
	}
}

// Record starts or extends one account-scoped cooldown.
func (g *Guard) Record(provider, account string, reason Reason) {
	if g == nil || reason == "" {
		return
	}
	state := g.scope(provider, account)
	now := g.now()
	g.mu.Lock()
	defer g.mu.Unlock()
	state.failures++
	delay := cooldownFor(reason, state.failures)
	until := now.Add(delay)
	if until.After(state.cooldown) {
		state.cooldown = until
		state.reason = reason
	}
}

// Cooldown returns the current account-scoped cooldown, if any.
func (g *Guard) Cooldown(provider, account string) (time.Time, Reason, bool) {
	if g == nil {
		return time.Time{}, "", false
	}
	state := g.scope(provider, account)
	now := g.now()
	g.mu.Lock()
	defer g.mu.Unlock()
	if !state.cooldown.After(now) {
		return time.Time{}, "", false
	}
	return state.cooldown, state.reason, true
}

// Reset clears all in-memory state. It is intended for explicit acceptance
// tests and user-requested recovery, not automatic retries.
func (g *Guard) Reset() {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.scopes = map[string]*scopeState{}
}

func (g *Guard) scope(provider, account string) *scopeState {
	key := strings.TrimSpace(provider) + "\x00" + strings.TrimSpace(account)
	g.mu.Lock()
	defer g.mu.Unlock()
	state := g.scopes[key]
	if state == nil {
		state = &scopeState{
			gate:       make(chan struct{}, 1),
			operations: map[string]*operationState{},
		}
		g.scopes[key] = state
	}
	return state
}

func cooldownFor(reason Reason, failures int) time.Duration {
	if failures < 1 {
		failures = 1
	}
	exponential := func(base, maximum time.Duration) time.Duration {
		delay := base
		for i := 1; i < failures && delay < maximum; i++ {
			delay *= 2
		}
		if delay > maximum {
			return maximum
		}
		return delay
	}
	switch reason {
	case ReasonAuth:
		return exponential(30*time.Minute, 6*time.Hour)
	case ReasonRateLimit:
		return exponential(15*time.Minute, 6*time.Hour)
	case ReasonChallenge:
		return 6 * time.Hour
	case ReasonUpstream:
		return exponential(time.Minute, 6*time.Hour)
	default:
		return 0
	}
}

// Classify maps an error or outcome message to a safety reason. It is
// intentionally conservative: unknown errors do not create a cooldown.
func Classify(text string) Reason {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return ""
	}
	contains := func(values ...string) bool {
		for _, value := range values {
			if strings.Contains(lower, value) {
				return true
			}
		}
		return false
	}
	if contains("captcha", "验证码", "风控", "risk control", "challenge required", "blocked by risk") {
		return ReasonChallenge
	}
	if contains("http 429", "status 429", "too many requests", "rate limit", "ratelimited",
		"请求过于频繁", "限流") {
		return ReasonRateLimit
	}
	if contains("http 401", "http 403", "unauthorized", "forbidden",
		"invalid token", "reauth", "重新登录", "鉴权失败", "凭据被拒绝") {
		return ReasonAuth
	}
	if contains("http 408", "http 500", "http 502", "http 503", "http 504",
		"timeout", "timed out", "connection reset", "network is unreachable",
		"网络失败", "暂时不可用", "服务暂时异常") {
		return ReasonUpstream
	}
	return ""
}

// IsBlocked reports whether err is a guard cooldown.
func IsBlocked(err error) bool {
	var blocked *BlockedError
	return errors.As(err, &blocked)
}
