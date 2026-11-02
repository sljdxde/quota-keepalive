package app

import (
	"context"
	"fmt"
	"testing"
	"time"

	"qka/internal/store"
)

func TestFeedbackLogsReturnsRecentFailuresInChronologicalOrder(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	state := &guiState{app: &App{
		store: st,
		now:   func() time.Time { return now },
	}}

	insert := func(ts string, stateValue, level, message string) {
		t.Helper()
		if _, err := st.DB().Exec(`
INSERT INTO logs (ts, target, account_id, operation, state, level, message)
VALUES (?, 'fixture', 'a1', 'checkin', ?, ?, ?)`,
			ts, stateValue, level, message); err != nil {
			t.Fatal(err)
		}
	}
	insert(now.Add(-49*time.Hour).Format(time.RFC3339), "failed_final", "error", "old")
	insert(now.Add(-time.Hour).Format(time.RFC3339), "completed", "info", "success")
	for index := 0; index < 85; index++ {
		insert(now.Add(-time.Duration(85-index)*time.Second).Format(time.RFC3339),
			"failed_final", "error", fmt.Sprintf("failure-%02d", index))
	}

	entries, err := state.feedbackLogs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 80 {
		t.Fatalf("feedback log limit = %d, want 80", len(entries))
	}
	if entries[0].Message != "failure-05" || entries[len(entries)-1].Message != "failure-84" {
		t.Fatalf("feedback logs should be chronological and drop the oldest rows: %q ... %q",
			entries[0].Message, entries[len(entries)-1].Message)
	}
	for _, entry := range entries {
		if entry.Message == "old" || entry.Message == "success" {
			t.Fatalf("ineligible log leaked into feedback: %+v", entry)
		}
	}

	if entries, err := (&guiState{}).feedbackLogs(context.Background()); err != nil || entries != nil {
		t.Fatalf("nil state should return no logs: %v %v", entries, err)
	}
	if _, err := st.DB().Exec(`DROP TABLE logs`); err != nil {
		t.Fatal(err)
	}
	if _, err := state.feedbackLogs(context.Background()); err == nil {
		t.Fatal("database read failure should surface")
	}
}
