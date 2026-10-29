package status

import (
	"context"
	"database/sql"
	"time"

	"qka/internal/engine"
)

// CapabilitiesFunc 返回某目标支持的操作（从适配器能力推导，不硬编码产品）。
type CapabilitiesFunc func(target string) []string

// Service 提供只读状态查询与计划预演。
type Service struct {
	db   *sql.DB
	now  func() time.Time
	caps CapabilitiesFunc
}

// NewService 构造状态服务。
func NewService(db *sql.DB, now func() time.Time, caps CapabilitiesFunc) *Service {
	if caps == nil {
		caps = func(string) []string { return []string{"checkin"} }
	}
	return &Service{db: db, now: now, caps: caps}
}

type accountRow struct {
	target      string
	id          string
	displayName string
	enabled     int
}

func (s *Service) accounts(ctx context.Context) ([]accountRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT target, id, display_name, enabled FROM accounts ORDER BY target, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []accountRow
	for rows.Next() {
		var a accountRow
		if err := rows.Scan(&a.target, &a.id, &a.displayName, &a.enabled); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// healthRank 给健康状态排序（数字越小越好）。
func healthRank(h string) int {
	switch h {
	case "healthy":
		return 0
	case "reauth_required":
		return 1
	default:
		return 2
	}
}

// bestHealth 返回账号最优先来源的健康状态（pinned 优先，其次健康度）。
func (s *Service) bestHealth(ctx context.Context, target, account string) (string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT health, pinned, kind FROM sources WHERE target = ? AND account_id = ?`,
		target, account)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	best := "disabled"
	bestRank := 3
	var bestPinned int
	for rows.Next() {
		var health string
		var pinned int
		var kind string
		if err := rows.Scan(&health, &pinned, &kind); err != nil {
			return "", err
		}
		rank := healthRank(health)
		// pinned 来源在同等健康度下优先。
		if pinned > bestPinned || (pinned == bestPinned && rank < bestRank) {
			best, bestRank, bestPinned = health, rank, pinned
		}
	}
	return best, rows.Err()
}

// Status 汇总当前状态（只读，不含计划字段）。
func (s *Service) Status(ctx context.Context) (View, error) {
	accts, err := s.accounts(ctx)
	if err != nil {
		return View{}, err
	}
	day := engine.BeijingDay(s.now())

	view := View{}
	for _, a := range accts {
		if a.enabled != 1 {
			continue
		}
		as := AccountStatus{
			Target: a.target, AccountID: a.id, DisplayName: a.displayName, Enabled: true,
		}

		rows, err := s.db.QueryContext(ctx,
			`SELECT operation, state FROM daily_ops WHERE target = ? AND account_id = ? AND day_key = ?`,
			a.target, a.id, day)
		if err != nil {
			return View{}, err
		}
		for rows.Next() {
			var op, state string
			if err := rows.Scan(&op, &state); err != nil {
				rows.Close()
				return View{}, err
			}
			as.Operations = append(as.Operations, OpStatus{
				Target: a.target, AccountID: a.id, Operation: op, State: state,
			})
			if state == "signed_provisional" {
				as.PendingReasons = append(as.PendingReasons, "已领取，待复核")
			}
		}
		rows.Close()

		health, err := s.bestHealth(ctx, a.target, a.id)
		if err != nil {
			return View{}, err
		}
		as.SourceHealth = health

		view.Enabled = true
		view.Accounts = append(view.Accounts, as)
	}

	view.LastInvocation = s.lastInvocation(ctx)
	return view, nil
}

func (s *Service) lastInvocation(ctx context.Context) string {
	var ts sql.NullString
	_ = s.db.QueryRowContext(ctx, `SELECT MAX(ts) FROM logs`).Scan(&ts)
	return ts.String
}

// PlanValidate 只读预演：报告账号×操作、预计成本、冲突、冷却与状态迁移，零网络、零写入。
func (s *Service) PlanValidate(ctx context.Context) (Plan, error) {
	accts, err := s.accounts(ctx)
	if err != nil {
		return Plan{}, err
	}
	day := engine.BeijingDay(s.now())
	now := s.now()

	plan := Plan{}
	for _, a := range accts {
		if a.enabled != 1 {
			continue
		}
		health, _ := s.bestHealth(ctx, a.target, a.id)

		for _, op := range s.caps(a.target) {
			item := PlanItem{Target: a.target, AccountID: a.id, Operation: op, CostHint: costHint(op)}

			var state, cooldown string
			err := s.db.QueryRowContext(ctx,
				`SELECT state, COALESCE(cooldown_until, '') FROM daily_ops
				 WHERE target = ? AND account_id = ? AND operation = ? AND day_key = ?`,
				a.target, a.id, op, day).Scan(&state, &cooldown)
			if err != nil && err != sql.ErrNoRows {
				return Plan{}, err
			}

			switch state {
			case "signed_confirmed", "signed_provisional", "completed",
				"already", "ineligible", "inactive":
				item.Conflicts = append(item.Conflicts, "同日已终态")
			case "failed_retryable":
				if cd, perr := time.Parse(time.RFC3339, cooldown); perr == nil && cd.After(now) {
					item.Cooldown = true
				}
			}

			switch health {
			case "reauth_required":
				item.Conflicts = append(item.Conflicts, "来源失效，需重登")
			case "disabled":
				item.Conflicts = append(item.Conflicts, "无可用来源")
			}

			canRun := len(item.Conflicts) == 0 && !item.Cooldown
			item.Transition = previewTransition(op, now, canRun)

			plan.Items = append(plan.Items, item)
		}
	}
	return plan, nil
}

func previewTransition(op string, now time.Time, canRun bool) string {
	if !canRun {
		return "no_change"
	}
	if op == "keepalive" {
		return "completed"
	}
	if engine.IsBeforeBeijingNoon(now) {
		return "signed_provisional"
	}
	return "signed_confirmed"
}

func costHint(op string) float64 {
	if op == "keepalive" {
		return 0.02 // 仅为提示，实际成本以当次回执为准
	}
	return 0
}
