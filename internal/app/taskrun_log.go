package app

import (
	"context"
	"fmt"
	"time"
)

// RecordTaskRun writes one redacted log row for the built-in scheduler. It
// deliberately stores no account IDs, upstream messages or credentials.
func (a *App) RecordTaskRun(ctx context.Context, summary RunSummary) error {
	if a.store == nil {
		return nil
	}
	state := "completed"
	if summary.NeedsAttention > 0 {
		state = "failed_retryable"
	}
	message := fmt.Sprintf(
		"自动执行：尝试 %d，成功 %d，需处理 %d，跳过 %d",
		summary.Attempted, summary.Succeeded, summary.NeedsAttention, summary.Skipped,
	)
	ts := a.now().UTC().Format(time.RFC3339)
	_, err := a.store.DB().ExecContext(ctx, `
INSERT INTO logs (ts, target, account_id, operation, state, level, message)
VALUES (?, '', '', 'schedule', ?, 'info', ?)`,
		ts, state, message)
	return err
}
