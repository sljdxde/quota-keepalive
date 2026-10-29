package store

import (
	"context"
	"database/sql"
	"time"
)

// latestVersion 是当前代码内置的最新迁移版本号。
const latestVersion = 1

type migration struct {
	version int
	stmt    string
}

// migrations 按版本顺序排列；每个迁移在一个事务内应用并登记版本。
var migrations = []migration{
	{version: 1, stmt: schemaV1},
}

func ensureMigrationsTable(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version    INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL
)`)
	return err
}

func currentVersion(ctx context.Context, db *sql.DB) (int, error) {
	var version int
	err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version)
	return version, err
}

func (s *Store) migrate(ctx context.Context) error {
	if err := ensureMigrationsTable(ctx, s.db); err != nil {
		return err
	}

	for _, m := range migrations {
		var applied int
		if err := s.db.QueryRowContext(ctx,
			`SELECT COUNT(1) FROM schema_migrations WHERE version = ?`, m.version).Scan(&applied); err != nil {
			return err
		}
		if applied > 0 {
			continue
		}

		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, m.stmt); err != nil {
			_ = tx.Rollback()
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations(version, applied_at) VALUES(?, ?)`,
			m.version, time.Now().UTC().Format(time.RFC3339)); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// SchemaVersion 返回数据库已应用的迁移版本。
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	return currentVersion(ctx, s.db)
}
