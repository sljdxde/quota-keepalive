package registry

import (
	"context"
	"database/sql"
	"time"

	"qka/internal/adapterhost"
)

// Clock 返回当前时间，便于注入。
type Clock func() time.Time

// LoadedAdapter 是一个已加载适配器及其加载/验签状态。
type LoadedAdapter struct {
	Manifest            adapterhost.Manifest
	PlatformUnsupported bool
	SignatureInvalid    bool
	Disabled            bool
}

// Registry 把已加载适配器与本体状态拼装为自描述视图。
type Registry struct {
	db       *sql.DB
	now      Clock
	adapters []LoadedAdapter
}

// New 创建注册表。
func New(db *sql.DB, now Clock) *Registry {
	if now == nil {
		now = time.Now
	}
	return &Registry{db: db, now: now}
}

// SetAdapters 设置当前已加载适配器；热更新后重新调用即可刷新视图。
func (r *Registry) SetAdapters(adapters []LoadedAdapter) {
	r.adapters = adapters
}

// List 产出所有目标应用的自描述对象。
func (r *Registry) List(ctx context.Context) ([]AgentDescriptor, error) {
	var out []AgentDescriptor
	for _, la := range r.adapters {
		accounts, err := r.loadAccounts(ctx, la.Manifest.ID)
		if err != nil {
			return nil, err
		}
		lastInv, err := r.lastInvocation(ctx, la.Manifest.ID)
		if err != nil {
			return nil, err
		}

		d := AgentDescriptor{
			ID:               la.Manifest.ID,
			Name:             la.Manifest.Name,
			Version:          la.Manifest.Version,
			Capabilities:     la.Manifest.Capabilities,
			Needs:            la.Manifest.Needs,
			Enabled:          !la.Disabled,
			ScheduleOwner:    "host",
			Accounts:         accounts,
			LastInvocationAt: lastInv,
		}
		for _, a := range la.Manifest.Actions {
			d.Actions = append(d.Actions, ActionView{Op: a.Op, Effects: a.Effects})
		}

		d.Available, d.UnavailableReason = resolveAvailability(la, accounts)
		out = append(out, d)
	}
	return out, nil
}

func resolveAvailability(la LoadedAdapter, accounts []AccountView) (bool, UnavailableReason) {
	if la.SignatureInvalid {
		return false, ReasonSignatureInvalid
	}
	if la.PlatformUnsupported {
		return false, ReasonPlatformUnsupported
	}
	if la.Disabled {
		return false, ReasonAdapterDisabled
	}
	if len(accounts) == 0 {
		return false, ReasonNotSignedIn
	}

	hasUsable := false
	for _, ac := range accounts {
		if !ac.Enabled {
			continue
		}
		for _, s := range ac.Sources {
			if s.Health == "reauth_required" {
				return false, ReasonReauthRequired
			}
			if s.Health == "healthy" {
				hasUsable = true
			}
		}
	}
	if !hasUsable {
		return false, ReasonNotSignedIn
	}
	return true, ""
}

func (r *Registry) loadAccounts(ctx context.Context, target string) ([]AccountView, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT id, display_name, masked_identity, enabled FROM accounts WHERE target = ?`, target)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AccountView
	for rows.Next() {
		var ac AccountView
		var en int
		if err := rows.Scan(&ac.ID, &ac.DisplayName, &ac.MaskedIdentity, &en); err != nil {
			return nil, err
		}
		ac.Enabled = en == 1
		ac.Sources, err = r.loadSources(ctx, target, ac.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, ac)
	}
	return out, rows.Err()
}

func (r *Registry) loadSources(ctx context.Context, target, account string) ([]SourceView, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT id, kind, read_only, health, pinned FROM sources WHERE target = ? AND account_id = ?`,
		target, account)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SourceView
	for rows.Next() {
		var s SourceView
		var ro, pin int
		if err := rows.Scan(&s.ID, &s.Kind, &ro, &s.Health, &pin); err != nil {
			return nil, err
		}
		s.ReadOnly = ro == 1
		s.Pinned = pin == 1
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *Registry) lastInvocation(ctx context.Context, target string) (string, error) {
	var ts string
	err := r.db.QueryRowContext(ctx,
		`SELECT ts FROM logs WHERE target = ? ORDER BY ts DESC LIMIT 1`, target).Scan(&ts)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return ts, err
}
