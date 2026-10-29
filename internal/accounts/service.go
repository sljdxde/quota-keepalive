package accounts

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ErrNoHealthySource 无可用健康来源。
var ErrNoHealthySource = errors.New("无可用健康来源")

// Service 管理账号与来源。
type Service struct {
	db *sql.DB
}

// NewService 构造账号服务。
func NewService(db *sql.DB) *Service {
	return &Service{db: db}
}

func nowTS() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// Discover 写入/更新账号及其发现来源（幂等 upsert）。
func (s *Service) Discover(ctx context.Context, target string, accounts []DiscoveredAccount) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, a := range accounts {
		ts := nowTS()
		_, err := tx.ExecContext(ctx, `
INSERT INTO accounts (id, target, display_name, masked_identity, enabled, created_at, updated_at)
VALUES (?, ?, ?, ?, 1, ?, ?)
ON CONFLICT(target, id) DO UPDATE SET
  display_name = excluded.display_name,
  masked_identity = excluded.masked_identity,
  updated_at = excluded.updated_at`,
			a.ID, target, a.DisplayName, a.MaskedIdentity, ts, ts)
		if err != nil {
			return err
		}

		kind := string(a.SourceKind)
		if kind == "" {
			kind = string(KindManaged)
		}
		readOnly := 0
		if kind == string(KindImported) {
			readOnly = 1
		}
		_, err = tx.ExecContext(ctx, `
INSERT INTO sources (id, target, account_id, kind, read_only, health, pinned, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, 'healthy', 0, ?, ?)
ON CONFLICT(target, account_id, id) DO UPDATE SET updated_at = excluded.updated_at`,
			a.SourceID, target, a.ID, kind, readOnly, ts, ts)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ImportSource 导入一个已存在来源，标记 imported/read_only。
func (s *Service) ImportSource(ctx context.Context, target, accountID, sourceID string) error {
	ts := nowTS()
	_, err := s.db.ExecContext(ctx, `
INSERT INTO sources (id, target, account_id, kind, read_only, health, pinned, created_at, updated_at)
VALUES (?, ?, ?, 'imported', 1, 'healthy', 0, ?, ?)
ON CONFLICT(target, account_id, id) DO UPDATE SET
  kind = 'imported', read_only = 1, updated_at = excluded.updated_at`,
		sourceID, target, accountID, ts, ts)
	return err
}

// EstablishManagedSource 经官方登录 verified 建立受管来源。
func (s *Service) EstablishManagedSource(ctx context.Context, target, accountID, sourceID string) error {
	ts := nowTS()
	_, err := s.db.ExecContext(ctx, `
INSERT INTO sources (id, target, account_id, kind, read_only, health, pinned, created_at, updated_at)
VALUES (?, ?, ?, 'managed', 0, 'healthy', 0, ?, ?)
ON CONFLICT(target, account_id, id) DO UPDATE SET
  kind = 'managed', read_only = 0, health = 'healthy', updated_at = excluded.updated_at`,
		sourceID, target, accountID, ts, ts)
	return err
}

// PinSource 固定/取消固定来源。
func (s *Service) PinSource(ctx context.Context, target, accountID, sourceID string, pinned bool) error {
	p := 0
	if pinned {
		p = 1
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE sources SET pinned = ?, updated_at = ? WHERE target = ? AND account_id = ? AND id = ?`,
		p, nowTS(), target, accountID, sourceID)
	return err
}

// SetEnabled 逐账号启停。
func (s *Service) SetEnabled(ctx context.Context, target, accountID string, enabled bool) error {
	e := 0
	if enabled {
		e = 1
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE accounts SET enabled = ?, updated_at = ? WHERE target = ? AND id = ?`,
		e, nowTS(), target, accountID)
	return err
}

// Delete 仅删除 qka 映射（账号行级联来源），目标应用数据完好。
func (s *Service) Delete(ctx context.Context, target, accountID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM accounts WHERE target = ? AND id = ?`, target, accountID)
	return err
}

// MarkSourceFailure 来源失败：导入来源停用，受管来源转需重登。
func (s *Service) MarkSourceFailure(ctx context.Context, target, accountID, sourceID string) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE sources SET
  health = CASE WHEN kind = 'imported' THEN 'disabled' ELSE 'reauth_required' END,
  updated_at = ?
WHERE target = ? AND account_id = ? AND id = ?`,
		nowTS(), target, accountID, sourceID)
	return err
}

// List 返回指定目标的全部账号（含来源）。
func (s *Service) List(ctx context.Context, target string) ([]Account, error) {
	// target 为空时列出所有目标应用的账号。
	query := `SELECT target, id, display_name, masked_identity, enabled FROM accounts`
	args := []any{}
	if target != "" {
		query += ` WHERE target = ?`
		args = append(args, target)
	}
	query += ` ORDER BY target, id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Account
	for rows.Next() {
		var a Account
		var enabled int
		if err := rows.Scan(&a.Target, &a.ID, &a.DisplayName, &a.MaskedIdentity, &enabled); err != nil {
			return nil, err
		}
		a.Enabled = enabled == 1
		srcs, err := s.loadSources(ctx, a.Target, a.ID)
		if err != nil {
			return nil, err
		}
		a.Sources = srcs
		out = append(out, a)
	}
	return out, rows.Err()
}

// Get 返回单个账号（含来源）。
func (s *Service) Get(ctx context.Context, target, accountID string) (Account, error) {
	var a Account
	var enabled int
	err := s.db.QueryRowContext(ctx,
		`SELECT id, display_name, masked_identity, enabled FROM accounts WHERE target = ? AND id = ?`,
		target, accountID).Scan(&a.ID, &a.DisplayName, &a.MaskedIdentity, &enabled)
	if err != nil {
		return Account{}, err
	}
	a.Target = target
	a.Enabled = enabled == 1
	srcs, err := s.loadSources(ctx, target, accountID)
	if err != nil {
		return Account{}, err
	}
	a.Sources = srcs
	return a, nil
}

func (s *Service) loadSources(ctx context.Context, target, accountID string) ([]Source, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, kind, read_only, health, pinned FROM sources WHERE target = ? AND account_id = ? ORDER BY id`,
		target, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Source
	for rows.Next() {
		var src Source
		var readOnly, pinned int
		src.Target = target
		src.AccountID = accountID
		if err := rows.Scan(&src.ID, &src.Kind, &readOnly, &src.Health, &pinned); err != nil {
			return nil, err
		}
		src.ReadOnly = readOnly == 1
		src.Pinned = pinned == 1
		out = append(out, src)
	}
	return out, rows.Err()
}

// PreferredSource 按优先级选择来源：固定 > 健康受管 > 健康导入。
func (s *Service) PreferredSource(ctx context.Context, target, accountID string) (Source, error) {
	srcs, err := s.loadSources(ctx, target, accountID)
	if err != nil {
		return Source{}, err
	}

	for _, src := range srcs {
		if src.Pinned && src.Health == HealthHealthy {
			return src, nil
		}
	}
	for _, src := range srcs {
		if !src.Pinned && src.Health == HealthHealthy && src.Kind == KindManaged {
			return src, nil
		}
	}
	for _, src := range srcs {
		if !src.Pinned && src.Health == HealthHealthy && src.Kind == KindImported {
			return src, nil
		}
	}
	return Source{}, ErrNoHealthySource
}
