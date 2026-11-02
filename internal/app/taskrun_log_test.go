package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"qka/internal/store"
)

func TestRecordTaskRunWritesRedactedScheduleLog(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	now := time.Date(2026, 10, 30, 9, 10, 0, 0, time.FixedZone("CST", 8*3600))
	a := &App{store: st, now: func() time.Time { return now }}
	if err := a.RecordTaskRun(context.Background(), RunSummary{
		Attempted: 3, Succeeded: 2, NeedsAttention: 1, Skipped: 0,
	}); err != nil {
		t.Fatal(err)
	}

	var operation, state, message string
	if err := st.DB().QueryRow(`
SELECT operation, state, message FROM logs
WHERE operation = 'schedule'`).Scan(&operation, &state, &message); err != nil {
		t.Fatal(err)
	}
	if operation != "schedule" || state != "failed_retryable" {
		t.Fatalf("调度日志状态错误: operation=%s state=%s", operation, state)
	}
	if !strings.Contains(message, "尝试 3") || !strings.Contains(message, "需处理 1") {
		t.Fatalf("调度日志摘要错误: %s", message)
	}
	for _, forbidden := range []string{"token", "uid", "Authorization"} {
		if strings.Contains(message, forbidden) {
			t.Fatalf("调度日志不得包含敏感字段 %q: %s", forbidden, message)
		}
	}
}
