package status

import (
	"context"
	"database/sql"
	"time"

	"qka/internal/engine"
	"qka/internal/features"
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
	target         string
	id             string
	displayName    string
	maskedIdentity string
	enabled        int
}

func (s *Service) accounts(ctx context.Context) ([]accountRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT target, id, display_name, masked_identity, enabled
		 FROM accounts ORDER BY target, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []accountRow
	for rows.Next() {
		var a accountRow
		if err := rows.Scan(&a.target, &a.id, &a.displayName,
			&a.maskedIdentity, &a.enabled); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

type sourceAvailability struct {
	health     string
	reviewable bool
}

// preferredSource mirrors accounts.PreferredSource: healthy pinned source,
// then healthy managed, then healthy imported. reviewable is true only when
// that selected source is managed and not read-only.
func (s *Service) preferredSource(
	ctx context.Context,
	target, account string,
) (sourceAvailability, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT health, pinned, kind, read_only
		 FROM sources WHERE target = ? AND account_id = ? ORDER BY id`,
		target, account)
	if err != nil {
		return sourceAvailability{}, err
	}
	defer rows.Close()

	type sourceRow struct {
		health   string
		pinned   bool
		managed  bool
		readOnly bool
	}
	var sources []sourceRow
	hasReauth := false
	for rows.Next() {
		var health string
		var pinned int
		var kind string
		var readOnly int
		if err := rows.Scan(&health, &pinned, &kind, &readOnly); err != nil {
			return sourceAvailability{}, err
		}
		if health == "reauth_required" {
			hasReauth = true
		}
		sources = append(sources, sourceRow{
			health: health, pinned: pinned == 1, managed: kind == "managed",
			readOnly: readOnly == 1,
		})
	}
	if err := rows.Err(); err != nil {
		return sourceAvailability{}, err
	}

	selected := -1
	for i, source := range sources {
		if source.pinned && source.health == "healthy" {
			selected = i
			break
		}
	}
	if selected < 0 {
		for i, source := range sources {
			if !source.pinned && source.health == "healthy" && source.managed {
				selected = i
				break
			}
		}
	}
	if selected < 0 {
		for i, source := range sources {
			if !source.pinned && source.health == "healthy" && !source.managed {
				selected = i
				break
			}
		}
	}
	if selected >= 0 {
		source := sources[selected]
		return sourceAvailability{
			health: "healthy", reviewable: source.managed && !source.readOnly,
		}, nil
	}
	if hasReauth {
		return sourceAvailability{health: "reauth_required"}, nil
	}
	return sourceAvailability{health: "disabled"}, nil
}

func (s *Service) taskPreferences(
	ctx context.Context,
	target, account string,
) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT operation, enabled FROM account_task_preferences
WHERE target = ? AND account_id = ?`, target, account)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]bool{}
	for rows.Next() {
		var operation string
		var enabled int
		if err := rows.Scan(&operation, &enabled); err != nil {
			return nil, err
		}
		out[operation] = enabled == 1
	}
	return out, rows.Err()
}

func taskEnabled(preferences map[string]bool, operation string) bool {
	enabled, ok := preferences[operation]
	return !ok || enabled
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
			Target: a.target, AccountID: a.id, DisplayName: a.displayName,
			MaskedIdentity: a.maskedIdentity, Enabled: true,
		}

		source, err := s.preferredSource(ctx, a.target, a.id)
		if err != nil {
			return View{}, err
		}
		as.SourceHealth = source.health
		prefs, err := s.taskPreferences(ctx, a.target, a.id)
		if err != nil {
			return View{}, err
		}

		rows, err := s.db.QueryContext(ctx,
			`SELECT operation, state, credits, balance, credits_known, balance_known
			 FROM daily_ops
			 WHERE target = ? AND account_id = ? AND day_key = ?`,
			a.target, a.id, day)
		if err != nil {
			return View{}, err
		}
		for rows.Next() {
			var op, state string
			var credits, balance float64
			var creditsKnown, balanceKnown int
			if err := rows.Scan(&op, &state, &credits, &balance,
				&creditsKnown, &balanceKnown); err != nil {
				rows.Close()
				return View{}, err
			}
			if !features.OperationAvailable(op) {
				continue
			}
			opStatus := OpStatus{
				Target: a.target, AccountID: a.id, Operation: op, State: state,
				TaskEnabled: taskEnabled(prefs, op),
			}
			if creditsKnown == 1 {
				v := credits
				opStatus.Credits = &v
			}
			if balanceKnown == 1 {
				v := balance
				opStatus.Balance = &v
			}
			if state == "signed_provisional" {
				as.PendingReasons = append(as.PendingReasons, "已领取，待复核")
			}
			if state == "manual_required" {
				opStatus.Message = "自动签到被 WorkBuddy 拒绝"
				switch source.health {
				case "reauth_required":
					opStatus.Message += "；来源登录态已失效"
					opStatus.NextAction = "请重新登录 WorkBuddy CN 后再执行 checkin_run 复查"
					as.PendingReasons = append(as.PendingReasons, "来源需重新登录")
				case "disabled":
					opStatus.Message += "；没有可用来源"
					opStatus.NextAction = "请检查账号来源或重新登录 WorkBuddy CN"
					as.PendingReasons = append(as.PendingReasons, "没有可用来源")
				default:
					opStatus.NextAction = "请回 WorkBuddy App 签到，完成后再次执行 checkin_run 复查"
					as.PendingReasons = append(as.PendingReasons, "需回 WorkBuddy App 签到")
				}
			}
			as.Operations = append(as.Operations, opStatus)
		}
		rows.Close()

		view.Enabled = true
		view.Accounts = append(view.Accounts, as)
	}

	view.LastInvocation = s.lastInvocation(ctx)
	return view, nil
}

func (s *Service) lastInvocation(ctx context.Context) string {
	var ts sql.NullString
	_ = s.db.QueryRowContext(ctx,
		`SELECT MAX(ts) FROM logs WHERE operation <> ?`, "keepalive").Scan(&ts)
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
		source, err := s.preferredSource(ctx, a.target, a.id)
		if err != nil {
			return Plan{}, err
		}
		prefs, err := s.taskPreferences(ctx, a.target, a.id)
		if err != nil {
			return Plan{}, err
		}

		for _, op := range s.caps(a.target) {
			if !features.OperationAvailable(op) {
				continue
			}
			if !taskEnabled(prefs, op) {
				continue
			}
			item := PlanItem{Target: a.target, AccountID: a.id, Operation: op, CostHint: costHint(op)}

			var state, cooldown, leaseExpires string
			err := s.db.QueryRowContext(ctx,
				`SELECT state, COALESCE(cooldown_until, ''),
				        COALESCE(lease_expires_at, '') FROM daily_ops
				 WHERE target = ? AND account_id = ? AND operation = ? AND day_key = ?`,
				a.target, a.id, op, day).Scan(&state, &cooldown, &leaseExpires)
			if err != nil && err != sql.ErrNoRows {
				return Plan{}, err
			}

			manualReview := false
			switch state {
			case "manual_required":
				if a.target == "workbuddy-cn" && op == "checkin" {
					manualReview = true
				} else {
					item.Conflicts = append(item.Conflicts, "同日已终态")
				}
			case "signed_confirmed", "signed_provisional", "completed",
				"already", "ineligible", "inactive", "failed_final":
				item.Conflicts = append(item.Conflicts, "同日已终态")
			case "running":
				if expires, perr := time.Parse(time.RFC3339, leaseExpires); perr == nil && expires.After(now) {
					item.Conflicts = append(item.Conflicts, "执行中，租约仍有效")
				}
			case "failed_retryable":
				if cd, perr := time.Parse(time.RFC3339, cooldown); perr == nil && cd.After(now) {
					item.Cooldown = true
				}
			}

			switch source.health {
			case "reauth_required":
				item.Conflicts = append(item.Conflicts, "来源失效，需重登")
			case "disabled":
				item.Conflicts = append(item.Conflicts, "无可用来源")
			}

			if manualReview {
				if !source.reviewable && len(item.Conflicts) == 0 {
					item.Conflicts = append(item.Conflicts, "复查需要健康受管来源")
				}
				if len(item.Conflicts) == 0 {
					item.Transition = "review"
					plan.Items = append(plan.Items, item)
					continue
				}
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
