package app

import (
	"context"
	"testing"
	"time"

	"qka/internal/store"
)

// TestFeedbackLogsScanErrorSurfaces 覆盖反馈日志读取时的行扫描失败分支：
// logs.message 为 NULL 时不能静默吞掉数据库错误。
func TestFeedbackLogsScanErrorSurfaces(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	state := &guiState{app: &App{store: st, now: func() time.Time { return now }}}

	if _, err := st.DB().Exec(`DROP TABLE logs`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`
CREATE TABLE logs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ts TEXT NOT NULL,
  target TEXT NOT NULL,
  account_id TEXT NOT NULL,
  operation TEXT NOT NULL,
  state TEXT NOT NULL,
  level TEXT NOT NULL,
  message TEXT
)`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`
INSERT INTO logs (ts, target, account_id, operation, state, level, message)
VALUES (?, 'fixture', 'a1', 'checkin', 'failed_final', 'error', NULL)`,
		now.Add(-time.Minute).Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}

	if _, err := state.feedbackLogs(context.Background()); err == nil {
		t.Fatal("NULL message 行扫描失败应返回错误")
	}
}
