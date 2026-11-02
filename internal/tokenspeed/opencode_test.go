package tokenspeed

import (
	"database/sql"
	"encoding/json"
	"math"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenCodeMessageUsageAndDailyTotals(t *testing.T) {
	start := time.Now().UTC().Add(-time.Minute)
	path := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`
CREATE TABLE event (
  id TEXT PRIMARY KEY,
  aggregate_id TEXT NOT NULL,
  seq INTEGER NOT NULL,
  type TEXT NOT NULL,
  data TEXT NOT NULL
)`); err != nil {
		t.Fatal(err)
	}

	writeEvent := func(id string, completed int64) {
		t.Helper()
		info := map[string]any{
			"id":        "msg-opencode-1",
			"sessionID": "session-opencode-1",
			"role":      "assistant",
			"modelID":   "deepseek-v4.1-flash-free",
			"time": map[string]any{
				"created": start.UnixMilli(),
			},
			"tokens": map[string]any{
				"total":     400,
				"input":     100,
				"output":    300,
				"reasoning": 20,
				"cache": map[string]any{
					"read":  0,
					"write": 0,
				},
			},
		}
		if completed > 0 {
			info["time"] = map[string]any{
				"created":   start.UnixMilli(),
				"completed": completed,
			}
		}
		data, err := json.Marshal(map[string]any{
			"sessionID": "session-opencode-1",
			"info":      info,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(
			`INSERT INTO event (id, aggregate_id, seq, type, data) VALUES (?, ?, ?, ?, ?)`,
			id, "session-opencode-1", completed, "message.updated.1", string(data),
		); err != nil {
			t.Fatal(err)
		}
	}

	writeEvent("event-start", 0)
	writeEvent("event-complete", start.Add(2*time.Second).UnixMilli())

	now := start.Add(2500 * time.Millisecond)
	service := New(Config{
		Roots: []Root{{Source: "opencode", Path: path}},
		Now:   func() time.Time { return now },
	})
	service.Poll()

	snapshot := service.Snapshot()
	if snapshot.OutputTokensPerSecond != 0 {
		t.Fatalf("OpenCode has no first-token timing, so it must not invent a decode rate: %.3f",
			snapshot.OutputTokensPerSecond)
	}
	if len(snapshot.Sources) != 1 {
		t.Fatalf("sources = %+v", snapshot.Sources)
	}
	source := snapshot.Sources[0]
	if source.Source != "opencode" || source.Label != "OpenCode" ||
		source.TodayInputTokens != 100 || source.TodayOutputTokens != 300 ||
		source.TodayTotalTokens != 400 {
		t.Fatalf("OpenCode source = %+v", source)
	}
	if snapshot.TodayTotalTokens != 400 {
		t.Fatalf("today total = %d", snapshot.TodayTotalTokens)
	}

	service.Poll()
	if got := service.Snapshot().TodayTotalTokens; got != 400 {
		t.Fatalf("repeated OpenCode poll duplicated today usage: %d", got)
	}
}

func TestZCodeMessagesUseFirstTextAndAvoidDoubleCountingCache(t *testing.T) {
	start := time.Now().UTC().Add(-time.Minute)
	path := filepath.Join(t.TempDir(), "db.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		`CREATE TABLE message (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			time_created INTEGER NOT NULL,
			time_updated INTEGER NOT NULL,
			data TEXT NOT NULL
		)`,
		`CREATE TABLE part (
			id TEXT PRIMARY KEY,
			message_id TEXT NOT NULL,
			session_id TEXT NOT NULL,
			time_created INTEGER NOT NULL,
			time_updated INTEGER NOT NULL,
			data TEXT NOT NULL
		)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}

	messageData, err := json.Marshal(map[string]any{
		"role":    "assistant",
		"modelId": "GLM-5.3-Flash",
		"time":    map[string]any{"created": start.UnixMilli(), "completed": start.Add(2 * time.Second).UnixMilli()},
		"tokens": map[string]any{
			"total":     400,
			"input":     100,
			"output":    300,
			"reasoning": 20,
			"cache":     map[string]any{"read": 80, "write": 5},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO message (id, session_id, time_created, time_updated, data)
		 VALUES (?, ?, ?, ?, ?)`,
		"msg-zcode-1", "sess-zcode-1", start.UnixMilli(),
		start.Add(2*time.Second).UnixMilli(), string(messageData),
	); err != nil {
		t.Fatal(err)
	}
	for _, part := range []struct {
		id, data string
		at       time.Time
	}{
		{"part-reason", `{"type":"reasoning"}`, start.Add(200 * time.Millisecond)},
		{"part-text", `{"type":"text"}`, start.Add(500 * time.Millisecond)},
		{"part-finish", `{"type":"step-finish"}`, start.Add(2 * time.Second)},
	} {
		if _, err := db.Exec(
			`INSERT INTO part
			   (id, message_id, session_id, time_created, time_updated, data)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			part.id, "msg-zcode-1", "sess-zcode-1",
			part.at.UnixMilli(), part.at.UnixMilli(), part.data,
		); err != nil {
			t.Fatal(err)
		}
	}

	now := start.Add(2500 * time.Millisecond)
	service := New(Config{
		Roots: []Root{{Source: "zcode", Path: path}},
		Now:   func() time.Time { return now },
	})
	service.Poll()
	snapshot := service.Snapshot()
	if math.Abs(snapshot.OutputTokensPerSecond-186.6666666667) > 0.01 {
		t.Fatalf("ZCode output speed = %.3f, want 186.67 tok/s",
			snapshot.OutputTokensPerSecond)
	}
	if snapshot.TodayTotalTokens != 400 {
		t.Fatalf("ZCode today total = %d", snapshot.TodayTotalTokens)
	}
	if len(snapshot.Sources) != 1 || snapshot.Sources[0].Source != "zcode" ||
		snapshot.Sources[0].Label != "ZCode" ||
		snapshot.Sources[0].TodayInputTokens != 100 ||
		snapshot.Sources[0].TodayOutputTokens != 300 {
		t.Fatalf("ZCode source = %+v", snapshot.Sources)
	}

	service.Poll()
	if got := service.Snapshot().TodayTotalTokens; got != 400 {
		t.Fatalf("repeated ZCode poll duplicated usage: %d", got)
	}
}
