package engine

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const (
	// DefaultLeaseTTL 是执行租约的有效期。
	DefaultLeaseTTL = 30 * time.Second
	// KeepaliveCooldown 是保活可重试失败后的冷却时长。
	KeepaliveCooldown = 10 * time.Minute
)

// Key 是每日操作的身份部分（日期由调用时刻按北京日界决定）。
type Key struct {
	Target    string
	AccountID string
	Operation Operation
}

// Observation 是一次 Run 的用户可观察结果。
type Observation struct {
	Target    string    `json:"target"`
	AccountID string    `json:"account_id"`
	Operation Operation `json:"operation"`
	Day       string    `json:"day"`
	State     State     `json:"state"`
	Executed  bool      `json:"executed"` // 本次是否真的发起了目标侧请求
	Message   string    `json:"message,omitempty"`
}

// Executor 执行一次目标侧动作（上层注入适配器调用；测试用假件计数）。
type Executor func(ctx context.Context, key Key) (ExternalResult, error)

// Clock 返回当前时间，便于注入。
type Clock func() time.Time

// Engine 协调每日操作状态机、执行租约与恢复。
type Engine struct {
	db       *sql.DB
	clock    Clock
	holderID string
	leaseTTL time.Duration
}

// New 创建引擎；clock 为 nil 时用真实时钟。
func New(db *sql.DB, clock Clock, holderID string) *Engine {
	if clock == nil {
		clock = time.Now
	}
	return &Engine{db: db, clock: clock, holderID: holderID, leaseTTL: DefaultLeaseTTL}
}

// WithLeaseTTL 覆盖租约有效期（主要用于测试）。
func (e *Engine) WithLeaseTTL(d time.Duration) *Engine {
	e.leaseTTL = d
	return e
}

func formatTS(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// ensureRow 确保当日操作行存在（唯一键冲突时不改动）。
func (e *Engine) ensureRow(ctx context.Context, key Key, day, nowTS string) error {
	_, err := e.db.ExecContext(ctx, `
INSERT INTO daily_ops (target, account_id, operation, day_key, state, updated_at)
VALUES (?, ?, ?, ?, 'pending', ?)
ON CONFLICT (target, account_id, operation, day_key) DO NOTHING`,
		key.Target, key.AccountID, string(key.Operation), day, nowTS)
	return err
}

// acquire 用条件更新原子取得租约；返回是否拿到。
// 允许进入：pending/none；failed_retryable 且冷却已过；running 且租约已过期（接管恢复）。
func (e *Engine) acquire(ctx context.Context, key Key, day string, now time.Time) (bool, error) {
	nowTS := formatTS(now)
	expires := formatTS(now.Add(e.leaseTTL))

	res, err := e.db.ExecContext(ctx, `
UPDATE daily_ops
SET state = 'running',
    lease_holder = ?,
    lease_expires_at = ?,
    attempts = attempts + 1,
    updated_at = ?
WHERE target = ? AND account_id = ? AND operation = ? AND day_key = ?
  AND (
        state IN ('pending', 'none')
     OR (state = 'failed_retryable' AND (cooldown_until = '' OR cooldown_until <= ?))
     OR (state = 'running' AND lease_expires_at < ?)
  )`,
		e.holderID, expires, nowTS,
		key.Target, key.AccountID, string(key.Operation), day,
		nowTS, nowTS)
	if err != nil {
		return false, err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows == 1, nil
}

func (e *Engine) readState(ctx context.Context, key Key, day string) (State, error) {
	var s string
	err := e.db.QueryRowContext(ctx, `
SELECT state FROM daily_ops
WHERE target = ? AND account_id = ? AND operation = ? AND day_key = ?`,
		key.Target, key.AccountID, string(key.Operation), day).Scan(&s)
	return State(s), err
}

// CurrentState returns today's state for one operation. A missing row is
// reported as pending; it is not created by this read.
func (e *Engine) CurrentState(ctx context.Context, key Key) (State, error) {
	state, err := e.readState(ctx, key, BeijingDay(e.clock()))
	if errors.Is(err, sql.ErrNoRows) {
		return Pending, nil
	}
	return state, err
}

// ReconcileState 在只读复核后显式修正当日状态（如证据不足时 confirmed→provisional）。
func (e *Engine) ReconcileState(ctx context.Context, key Key, state State, message string) error {
	day := BeijingDay(e.clock())
	_, err := e.db.ExecContext(ctx, `
UPDATE daily_ops SET state = ?, message = ?, updated_at = ?
WHERE target = ? AND account_id = ? AND operation = ? AND day_key = ?`,
		string(state), message, formatTS(e.clock()),
		key.Target, key.AccountID, string(key.Operation), day)
	return err
}

// ReconcileStateIfCurrent applies a read-only review result only when the
// expected state is still current. This prevents a concurrent review from
// overwriting a state that changed after the observation began.
func (e *Engine) ReconcileStateIfCurrent(
	ctx context.Context,
	key Key,
	from, to State,
	message string,
) (bool, error) {
	day := BeijingDay(e.clock())
	res, err := e.db.ExecContext(ctx, `
UPDATE daily_ops SET state = ?, message = ?, updated_at = ?
WHERE target = ? AND account_id = ? AND operation = ? AND day_key = ?
  AND state = ?`,
		string(to), message, formatTS(e.clock()),
		key.Target, key.AccountID, string(key.Operation), day, string(from))
	if err != nil {
		return false, err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows == 1, nil
}

// complete 写回新状态并释放租约；保活可重试失败时设置冷却。
func (e *Engine) complete(ctx context.Context, key Key, day string, newState State, message string, now time.Time) error {
	cooldown := ""
	if key.Operation == Keepalive && newState == FailedRetryable {
		cooldown = formatTS(now.Add(KeepaliveCooldown))
	}

	_, err := e.db.ExecContext(ctx, `
UPDATE daily_ops
SET state = ?,
    lease_holder = '',
    lease_expires_at = '',
    cooldown_until = ?,
    message = ?,
    updated_at = ?
WHERE target = ? AND account_id = ? AND operation = ? AND day_key = ?`,
		string(newState), cooldown, message, formatTS(now),
		key.Target, key.AccountID, string(key.Operation), day)
	return err
}

// Run 执行一次单次调用：取得租约、执行外部动作、推进状态机。
// 未拿到租约（终态/他人有效租约/冷却中）时只返回当前状态，不发起外部请求。
func (e *Engine) Run(ctx context.Context, key Key, exec Executor) (Observation, error) {
	now := e.clock()
	day := BeijingDay(now)
	nowTS := formatTS(now)

	obs := Observation{Target: key.Target, AccountID: key.AccountID, Operation: key.Operation, Day: day}

	if err := e.ensureRow(ctx, key, day, nowTS); err != nil {
		return obs, err
	}

	gotLease, err := e.acquire(ctx, key, day, now)
	if err != nil {
		return obs, err
	}

	if !gotLease {
		s, err := e.readState(ctx, key, day)
		if err != nil {
			return obs, err
		}
		obs.State = s
		obs.Executed = false
		return obs, nil
	}

	result, execErr := exec(ctx, key)
	if execErr != nil {
		// 调用失败：按可重试失败处理，释放租约，下次调用再试。
		result = ExternalResult{Kind: RRetryable, Message: execErr.Error()}
	}

	newState := Transition(key.Operation, result.Kind, now)
	if err := e.complete(ctx, key, day, newState, result.Message, now); err != nil {
		return obs, err
	}

	obs.State = newState
	obs.Executed = true
	obs.Message = result.Message
	return obs, nil
}
