package tokenspeed

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestQoderMeasuredSpeedUsesItsOwnSource(t *testing.T) {
	start := time.Now().UTC().Add(-time.Minute)
	root := t.TempDir()
	path := filepath.Join(root, "project", "session.jsonl")
	writeLines(t, path,
		map[string]any{
			"timestamp": ts(start),
			"type":      "user",
			"message":   map[string]any{"role": "user"},
		},
		map[string]any{
			"timestamp": ts(start.Add(2 * time.Second)),
			"type":      "assistant",
			"message": map[string]any{
				"id":          "qoder-message-1",
				"model":       "qwen3-coder-plus",
				"stop_reason": "end_turn",
				"usage": map[string]any{
					"input_tokens":  100,
					"output_tokens": 3000,
				},
			},
		},
	)
	now := start.Add(2500 * time.Millisecond)
	service := New(Config{
		Roots: []Root{{Source: "qoder", Path: root}},
		Now:   func() time.Time { return now },
	})
	service.Poll()

	snapshot := service.Snapshot()
	assertNear(t, snapshot.OutputTokensPerSecond, 1500)
	if len(snapshot.Sources) != 1 || snapshot.Sources[0].Source != "qoder" ||
		snapshot.Sources[0].Label != "Qoder" {
		t.Fatalf("Qoder source snapshot = %+v", snapshot.Sources)
	}
	if snapshot.TodayTotalTokens != 3100 {
		t.Fatalf("Qoder today total = %d", snapshot.TodayTotalTokens)
	}
}

func TestWorkBuddyUsageMergesRepeatedMessage(t *testing.T) {
	start := time.Now().UTC().Add(-time.Minute)
	root := t.TempDir()
	path := filepath.Join(root, "session", "workbuddy.jsonl")
	writeLines(t, path,
		map[string]any{
			"timestamp": start.UnixMilli(),
			"type":      "message",
			"role":      "user",
		},
		map[string]any{
			"timestamp": start.Add(2 * time.Second).UnixMilli(),
			"type":      "function_call",
			"providerData": map[string]any{
				"messageId":      "reply-1",
				"model":          "hy3",
				"requestModelId": "hy3",
				"usage": map[string]any{
					"inputTokens":        100,
					"outputTokens":       3000,
					"totalTokens":        3100,
					"inputTokensDetails": []any{map[string]any{"cached_tokens": 20}},
				},
			},
		},
	)
	now := start.Add(2500 * time.Millisecond)
	service := New(Config{
		Roots: []Root{{Source: "workbuddy", Path: root}},
		Now:   func() time.Time { return now },
	})
	service.Poll()

	snapshot := service.Snapshot()
	assertNear(t, snapshot.OutputTokensPerSecond, 1500)
	if len(snapshot.Sources) != 1 || snapshot.Sources[0].Label != "WorkBuddy" {
		t.Fatalf("WorkBuddy source snapshot = %+v", snapshot.Sources)
	}
	if snapshot.TodayTotalTokens != 3100 {
		t.Fatalf("WorkBuddy today total = %d", snapshot.TodayTotalTokens)
	}

	appendLines(t, path, map[string]any{
		"timestamp": start.Add(3 * time.Second).UnixMilli(),
		"type":      "message",
		"role":      "assistant",
		"providerData": map[string]any{
			"messageId": "reply-1",
			"model":     "hy3",
			"usage": map[string]any{
				"inputTokens":  120,
				"outputTokens": 3600,
				"totalTokens":  3720,
			},
		},
	})
	now = start.Add(3500 * time.Millisecond)
	service.Poll()
	if got := service.Snapshot().TodayTotalTokens; got != 3720 {
		t.Fatalf("repeated WorkBuddy usage should replace prior contribution, got %d", got)
	}
	record := service.records["workbuddy:reply-1"]
	if record.usage.output != 3600 || record.usage.input != 120 || record.usage.total != 3720 {
		t.Fatalf("merged WorkBuddy usage = %+v", record.usage)
	}
}

func TestIdleAgentDailyTotalsStayVisiblePerSource(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, beijingZone)
	start := now.Add(-2 * time.Hour)
	codexRoot := t.TempDir()
	qoderRoot := t.TempDir()
	writeLines(t, filepath.Join(codexRoot, "session.jsonl"),
		map[string]any{
			"timestamp": ts(start),
			"type":      "token_usage_record",
			"payload": map[string]any{
				"response_id": "codex-idle",
				"usage": map[string]any{
					"input_tokens":  100,
					"output_tokens": 300,
					"total_tokens":  400,
				},
			},
		},
	)
	writeLines(t, filepath.Join(qoderRoot, "session.jsonl"),
		map[string]any{
			"timestamp": ts(start),
			"type":      "assistant",
			"message": map[string]any{
				"id":          "qoder-idle",
				"model":       "qwen3-coder-plus",
				"stop_reason": "end_turn",
				"usage": map[string]any{
					"input_tokens":  20,
					"output_tokens": 80,
				},
			},
		},
	)
	for _, path := range []string{
		filepath.Join(codexRoot, "session.jsonl"),
		filepath.Join(qoderRoot, "session.jsonl"),
	} {
		if err := os.Chtimes(path, start, start); err != nil {
			t.Fatal(err)
		}
	}

	service := New(Config{
		Roots: []Root{
			{Source: "codex", Path: codexRoot},
			{Source: "qoder", Path: qoderRoot},
		},
		Now: func() time.Time { return now },
	})
	service.Poll()

	snapshot := service.Snapshot()
	if snapshot.OutputTokensPerSecond != 0 || snapshot.GeneratingAgents != 0 {
		t.Fatalf("idle snapshot should have no live speed: %+v", snapshot)
	}
	if snapshot.TodayTotalTokens != 500 {
		t.Fatalf("today total = %d", snapshot.TodayTotalTokens)
	}
	got := make(map[string]SourceSnapshot)
	for _, source := range snapshot.Sources {
		got[source.Source] = source
	}
	if got["codex"].TodayTotalTokens != 400 || got["qoder"].TodayTotalTokens != 100 {
		t.Fatalf("daily source totals = %+v", got)
	}
}

func TestParseFlexibleTimestampAndAgentLabels(t *testing.T) {
	wantMS := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC).UnixMilli()
	for _, raw := range []json.RawMessage{
		json.RawMessage(`"2026-11-02T10:00:00Z"`),
		json.RawMessage(`1793613600000`),
		json.RawMessage(`1793613600`),
	} {
		if got, ok := parseFlexibleTimestamp(raw); !ok || got != wantMS {
			t.Fatalf("parseFlexibleTimestamp(%s) = %d, %v; want %d", raw, got, ok, wantMS)
		}
	}
	for source, label := range map[string]string{
		"qoder":     "Qoder",
		"qoder-cn":  "Qoder CN",
		"workbuddy": "WorkBuddy",
		"codebuddy": "CodeBuddy",
		"opencode":  "OpenCode",
		"zcode":     "ZCode",
	} {
		if got := sourceLabel(source); got != label {
			t.Fatalf("sourceLabel(%q) = %q, want %q", source, got, label)
		}
	}
}
