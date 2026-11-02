package capabilities

import (
	"context"
	"database/sql"
	"time"

	"qka/internal/privacy"
)

// maxLogMessageRunes 是写入 logs 表的单条消息上限。脱敏在写入口统一完成，
// 上游插件与供应商文案不能把明文凭证、uid、手机号或邮箱带进长期日志。
const maxLogMessageRunes = 4000

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
	return l.WriteWithState(ctx, ns, "", level, message)
}

// WriteWithState 写入一条带每日操作状态的日志；状态仅由调用方提供，
// 命名空间仍由服务端强制；message 在写入口按 privacy 统一规则脱敏。
func (l *LogService) WriteWithState(
	ctx context.Context,
	ns Namespace,
	state, level, message string,
) error {
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
INSERT INTO logs (ts, target, account_id, operation, state, level, message)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		now.UTC().Format(time.RFC3339),
		ns.Target, ns.AccountID, ns.Operation, state, level,
		privacy.SanitizeText(message, maxLogMessageRunes))
	return err
}
