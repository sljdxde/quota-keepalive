package tokenspeed

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTodayUsageResetsAtBeijingMidnight(t *testing.T) {
	beforeMidnight := time.Date(2026, 11, 2, 23, 59, 0, 0, beijingZone)
	now := beforeMidnight
	service := New(Config{Now: func() time.Time { return now }})
	service.upsertRecord(responseRecord{
		key:       "codex:late",
		source:    "codex",
		endMS:     beforeMidnight.UnixMilli(),
		usage:     usage{input: 100, output: 300, total: 400},
		final:     true,
		updatedAt: beforeMidnight,
	})
	if service.todayTotal != 400 {
		t.Fatalf("late-day total = %d, want 400", service.todayTotal)
	}

	now = beforeMidnight.Add(2 * time.Minute)
	service.ensureToday(now)
	if service.todayDate != beijingDay(now) || service.todayTotal != 0 ||
		len(service.todayContributions) != 0 || len(service.todayBySource) != 0 {
		t.Fatalf("midnight reset failed: date=%s total=%d contributions=%v sources=%v",
			service.todayDate, service.todayTotal,
			service.todayContributions, service.todayBySource)
	}

	service.upsertRecord(responseRecord{
		key:       "codex:new-day",
		source:    "codex",
		endMS:     now.UnixMilli(),
		usage:     usage{input: 20, output: 80, total: 100},
		final:     true,
		updatedAt: now,
	})
	if service.todayTotal != 100 || service.todayBySource["codex"].total != 100 {
		t.Fatalf("new-day usage was not counted independently: total=%d sources=%v",
			service.todayTotal, service.todayBySource)
	}
}

func TestWorkBuddyParserRejectsIncompleteEventsAndReadsLegacyUsage(t *testing.T) {
	service := New(Config{Now: time.Now})
	state := &fileState{source: "workbuddy", agentID: "legacy.jsonl"}
	for _, line := range [][]byte{
		[]byte(`{`),
		[]byte(`{"type":"message","role":"user"}`),
		[]byte(`{"type":"reasoning"}`),
	} {
		service.ingestWorkBuddy("workbuddy", state.agentID, state, line)
	}
	if len(service.records) != 0 || state.active {
		t.Fatalf("incomplete WorkBuddy events changed state: records=%v active=%v",
			service.records, state.active)
	}

	line, err := json.Marshal(map[string]any{
		"timestamp": 1793613600000,
		"type":      "message",
		"role":      "assistant",
		"providerData": map[string]any{
			"messageId": "legacy-reply",
			"usage": map[string]any{
				"input_tokens":                100,
				"output_tokens":               300,
				"cache_creation_input_tokens": 20,
				"input_tokens_details": []any{
					map[string]any{"cached_tokens": 10},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	service.ingestWorkBuddy("workbuddy", state.agentID, state, line)
	record, ok := service.records["workbuddy:legacy-reply"]
	if !ok {
		t.Fatal("legacy WorkBuddy usage was not recorded")
	}
	if record.usage.input != 100 || record.usage.output != 300 ||
		record.usage.cached != 10 || record.usage.cacheNew != 20 ||
		record.usage.total != 400 {
		t.Fatalf("legacy WorkBuddy usage = %+v", record.usage)
	}
}

func TestOpenCodeMalformedDatabaseAndRecreatedCursor(t *testing.T) {
	now := time.Now().UTC()
	path := filepath.Join(t.TempDir(), "opencode.db")
	if err := os.WriteFile(path, []byte("not sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := New(Config{
		Roots: []Root{{Source: "opencode", Path: path}},
		Now:   func() time.Time { return now },
	})
	service.Poll()
	if len(service.records) != 0 {
		t.Fatalf("malformed OpenCode database produced records: %+v", service.records)
	}

	createOpenCodeEventDB(t, path, 1, now.Add(-time.Second).UnixMilli())
	service.Poll()
	if len(service.records) != 1 {
		t.Fatalf("first valid OpenCode poll records = %d, want 1", len(service.records))
	}
	state := service.opencode[path]
	if state == nil || state.lastRowID != 1 {
		t.Fatalf("OpenCode cursor = %+v, want row 1", state)
	}

	createOpenCodeEventDB(t, path, 7, now.UnixMilli())
	service.Poll()
	state = service.opencode[path]
	if state == nil || state.lastRowID != 7 {
		t.Fatalf("recreated OpenCode database cursor = %+v, want reset and row 7", state)
	}
}

func createOpenCodeEventDB(t *testing.T, path string, rowID int64, createdMS int64) {
	t.Helper()
	_ = os.Remove(path)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE event (
	  id TEXT PRIMARY KEY,
	  aggregate_id TEXT NOT NULL,
	  seq INTEGER NOT NULL,
	  type TEXT NOT NULL,
	  data TEXT NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{
		"info": map[string]any{
			"id":        fmt.Sprintf("msg-%d", rowID),
			"sessionID": "session-opencode",
			"role":      "assistant",
			"modelID":   "model-opencode",
			"time":      map[string]any{"created": createdMS, "completed": createdMS},
			"tokens": map[string]any{
				"input": 10, "output": 20, "total": 5,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO event (rowid, id, aggregate_id, seq, type, data) VALUES (?, ?, ?, ?, ?, ?)`,
		rowID,
		fmt.Sprintf("event-%d", rowID), "aggregate", rowID,
		"message.updated.1", string(data),
	); err != nil {
		t.Fatal(err)
	}
}
