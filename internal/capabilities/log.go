package capabilities

import (
	"context"
	"database/sql"
	"time"
)

// LogService 提供按命名空间强制写入的日志能力。
type LogService struct {
	db    *sql.DB
	grant Grant
	now   Clock
}

// NewLogService 组装日志服务。
func NewLogService(db *sql.DB, grant Grant, now Clock) *LogService {
	if now == nil {
		now = time.Now
	}
	return &LogService{db: db, grant: grant, now: now}
}

// Write 写入一条日志；target/account/operation 由命名空间强制，适配器无法指定其他作用域。
func (l *LogService) Write(ctx context.Context, ns Namespace, level, message string) error {
	if err := l.grant.Authorize("log.write"); err != nil {
		return err
	}
	for _, c := range []string{ns.Target, ns.AccountID, ns.Operation} {
		if !safeComponent(c) {
			return ErrUnsafeNamespace
		}
	}

	now := l.now()
	_, err := l.db.ExecContext(ctx, `
INSERT INTO logs (ts, target, account_id, operation, level, message)
VALUES (?, ?, ?, ?, ?, ?)`,
		now.UTC().Format(time.RFC3339),
		ns.Target, ns.AccountID, ns.Operation, level, message)
	return err
}
