package tokenspeed

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"
)

func TestClaudeIngestUserAndInvalidEntries(t *testing.T) {
	service := New(Config{})
	state := &fileState{}
	service.ingestClaude("claude.jsonl", state, []byte("{"))
	service.ingestClaude("claude.jsonl", state, mustJSON(map[string]any{
		"type": "user", "timestamp": "bad",
	}))

	service.ingestClaude("claude.jsonl", state, mustJSON(map[string]any{
		"type":      "user",
		"timestamp": ts(time.Unix(100, 0)),
		"message": map[string]any{
			"content": []any{map[string]any{"type": "text", "text": "[Request interrupted by user]"}},
		},
	}))
	if state.active || state.hasPending || state.inFlightMS != 0 || state.claude.preciseStart {
		t.Fatalf("interrupted user should reset state: %+v", state)
	}

	state = &fileState{}
	service.ingestClaude("claude.jsonl", state, mustJSON(map[string]any{
		"type": "user", "timestamp": ts(time.Unix(200, 0)), "toolEndsTurn": true,
	}))
	if state.active || state.hasPending || state.inFlightMS != 0 {
		t.Fatalf("toolEndsTurn user should reset state: %+v", state)
	}

	state = &fileState{}
	service.ingestClaude("claude.jsonl", state, mustJSON(map[string]any{
		"type": "user", "timestamp": ts(time.Unix(300, 0)),
	}))
	if !state.active || !state.hasPending || state.pendingMS != 300000 || state.inFlightMS != 300000 {
		t.Fatalf("ordinary user should start pending work: %+v", state)
	}

	state.claude.preciseStart = true
	service.ingestClaude("claude.jsonl", state, mustJSON(map[string]any{
		"type": "user", "timestamp": ts(time.Unix(400, 0)),
	}))
	if state.pendingMS != 300000 || state.inFlightMS != 300000 {
		t.Fatalf("precise start should retain original pending timestamp: %+v", state)
	}
}

func TestClaudeAssistantBranches(t *testing.T) {
	service := New(Config{})
	state := &fileState{}
	for _, message := range []any{
		nil,
		map[string]any{"id": "synthetic", "model": "<synthetic>", "usage": map[string]any{"output_tokens": 1}},
		map[string]any{"id": "no-usage", "model": "claude"},
		map[string]any{"id": "", "model": "claude", "usage": map[string]any{"output_tokens": 1}},
	} {
		line := map[string]any{"type": "assistant", "timestamp": ts(time.Unix(500, 0))}
		if message != nil {
			line["message"] = message
		}
		service.ingestClaude("claude.jsonl", state, mustJSON(line))
	}

	service.ingestClaude("claude.jsonl", state, mustJSON(map[string]any{
		"type": "assistant", "timestamp": ts(time.Unix(510, 0)),
		"requestId": "request-fallback",
		"message": map[string]any{
			"id": "", "model": "claude",
			"usage": map[string]any{"input_tokens": 1, "output_tokens": 2},
		},
	}))
	service.ingestClaude("claude.jsonl", state, mustJSON(map[string]any{
		"type": "assistant", "timestamp": ts(time.Unix(520, 0)),
		"uuid": "uuid-fallback",
		"message": map[string]any{
			"id": "", "model": "claude",
			"usage": map[string]any{"input_tokens": 1, "output_tokens": 2},
		},
	}))
	if len(service.records) != 2 {
		t.Fatalf("requestId and UUID should both provide dedup keys: %+v", service.records)
	}

	service.claim("claude:other-file", "other.jsonl")
	service.ingestClaude("claude.jsonl", state, mustJSON(map[string]any{
		"type": "assistant", "timestamp": ts(time.Unix(530, 0)),
		"message": map[string]any{
			"id": "other-file", "model": "claude",
			"usage": map[string]any{"output_tokens": 1},
		},
	}))

	first := map[string]any{
		"type": "assistant", "timestamp": ts(time.Unix(600, 0)),
		"message": map[string]any{
			"id": "merge", "model": "claude-opus", "stop_reason": "end_turn",
			"usage": map[string]any{"input_tokens": 10, "output_tokens": 20, "cache_read_input_tokens": 2},
		},
	}
	second := map[string]any{
		"type": "assistant", "timestamp": ts(time.Unix(610, 0)),
		"message": map[string]any{
			"id": "merge", "model": "",
			"usage": map[string]any{"input_tokens": 5, "output_tokens": 30, "cache_read_input_tokens": 4},
		},
	}
	service.ingestClaude("claude.jsonl", state, mustJSON(first))
	service.ingestClaude("claude.jsonl", state, mustJSON(second))
	record := state.claude.responses["claude:merge"]
	if record.usage.input != 12 || record.usage.output != 30 ||
		record.usage.cached != 4 || record.usage.total != 39 {
		t.Fatalf("assistant usage should merge maxima: %+v", record.usage)
	}
	if record.model != "claude-opus" || !record.final {
		t.Fatalf("assistant metadata should merge: %+v", record)
	}

	streaming := &fileState{pendingMS: 700000, hasPending: true}
	service.ingestClaude("stream.jsonl", streaming, mustJSON(map[string]any{
		"type": "assistant", "timestamp": ts(time.Unix(700, 0)),
		"message": map[string]any{
			"id": "stream-1", "model": "claude",
			"usage": map[string]any{"output_tokens": 1},
		},
	}))
	if !streaming.active || streaming.inFlightMS != 700000 {
		t.Fatalf("streaming assistant should use pending start: %+v", streaming)
	}
	streaming.hasPending = false
	service.ingestClaude("stream.jsonl", streaming, mustJSON(map[string]any{
		"type": "assistant", "timestamp": ts(time.Unix(710, 0)),
		"message": map[string]any{
			"id": "stream-2", "model": "claude",
			"usage": map[string]any{"output_tokens": 1},
		},
	}))
	if !streaming.active || streaming.inFlightMS != 710000 {
		t.Fatalf("streaming without start should use assistant timestamp: %+v", streaming)
	}
}

func TestClaudeHelpers(t *testing.T) {
	previous := usage{input: 1, cached: 2, cacheNew: 3, output: 4, reasoning: 5, total: 9}
	next := usage{input: 6, cached: 1, cacheNew: 4, output: 3, reasoning: 7, total: 8}
	merged := mergeUsage(previous, next)
	if merged != (usage{input: 6, cached: 2, cacheNew: 4, output: 4, reasoning: 7, total: 9}) {
		t.Fatalf("mergeUsage = %+v", merged)
	}
	if claudeInterrupted(claudeEnvelope{}) {
		t.Fatal("nil message should not be interrupted")
	}
	if claudeInterrupted(claudeEnvelope{Message: &claudeMessage{}}) {
		t.Fatal("empty message should not be interrupted")
	}
	if !claudeInterrupted(claudeEnvelope{Message: &claudeMessage{
		Content: []claudeContent{{Text: "[Request interrupted by user for tool use]"}},
	}}) {
		t.Fatal("interruption marker should be recognized")
	}
	if claudeHasToolUse(nil) || claudeHasToolUse(&claudeMessage{}) {
		t.Fatal("nil or empty message should not report tool use")
	}
	if !claudeHasToolUse(&claudeMessage{Content: []claudeContent{{Type: "tool_use"}}}) {
		t.Fatal("tool_use content should be recognized")
	}
	if got := firstNonEmpty("first", "second"); got != "first" {
		t.Fatalf("firstNonEmpty first = %q", got)
	}
	if got := firstNonEmpty("", "second"); got != "second" {
		t.Fatalf("firstNonEmpty fallback = %q", got)
	}
}

func TestCodexIngestIgnoredAndStateBranches(t *testing.T) {
	service := New(Config{})
	state := &fileState{}
	service.ingestCodex("codex.jsonl", state, []byte("{"))
	service.ingestCodex("codex.jsonl", state, mustJSON(map[string]any{
		"type": "turn_context", "timestamp": "bad",
	}))

	service.ingestCodex("codex.jsonl", state, mustJSON(map[string]any{
		"timestamp": ts(time.Unix(100, 0)),
		"type":      "session_meta",
		"payload": map[string]any{
			"model":                          "gpt",
			"subagent_history_start_ordinal": 5,
		},
	}))
	if state.subagentOrd == nil || *state.subagentOrd != 5 || state.model != "gpt" {
		t.Fatalf("session meta = %+v", state)
	}
	service.ingestCodex("codex.jsonl", state, mustJSON(map[string]any{
		"timestamp": ts(time.Unix(101, 0)),
		"type":      "session_meta",
		"payload":   map[string]any{"model": "ignored"},
	}))
	if state.model != "gpt" {
		t.Fatalf("duplicate session meta changed model: %+v", state)
	}

	service.ingestCodex("codex.jsonl", state, mustJSON(map[string]any{
		"timestamp": ts(time.Unix(1, 0)),
		"type":      "turn_context",
		"payload":   map[string]any{"model": "too-old"},
	}))
	service.ingestCodex("codex.jsonl", state, mustJSON(map[string]any{
		"timestamp": ts(time.Unix(200, 0)),
		"ordinal":   1,
		"type":      "turn_context",
		"payload":   map[string]any{"model": "before-subagent"},
	}))
	if state.model != "gpt" {
		t.Fatalf("pre-subagent line changed model: %+v", state)
	}

	service.ingestCodex("codex.jsonl", state, mustJSON(map[string]any{
		"timestamp": ts(time.Unix(300, 0)),
		"ordinal":   6,
		"type":      "turn_context",
		"payload":   map[string]any{"model": "gpt-new"},
	}))
	if state.model != "gpt-new" {
		t.Fatalf("turn_context model = %q", state.model)
	}

	service.ingestCodex("codex.jsonl", state, mustJSON(map[string]any{
		"timestamp": ts(time.Unix(400, 0)),
		"type":      "token_usage_record",
		"payload": map[string]any{
			"thread_id": "thread-key",
			"usage":     map[string]any{"output_tokens": 1},
		},
	}))
	service.ingestCodex("codex.jsonl", state, mustJSON(map[string]any{
		"timestamp": ts(time.Unix(410, 0)),
		"type":      "token_usage_record",
		"payload": map[string]any{
			"usage": map[string]any{"output_tokens": 2},
		},
	}))
	if len(service.records) != 2 {
		t.Fatalf("thread and timestamp fallback keys = %+v", service.records)
	}

	service.ingestCodex("codex.jsonl", state, mustJSON(map[string]any{
		"timestamp": ts(time.Unix(420, 0)),
		"type":      "event_msg",
		"payload":   map[string]any{"type": "task_complete"},
	}))
	if state.active || state.streaming || state.hasPending || state.inFlightMS != 0 {
		t.Fatalf("task_complete should reset state: %+v", state)
	}
	legacyState := &fileState{}
	service.ingestCodex("codex.jsonl", legacyState, mustJSON(map[string]any{
		"timestamp": ts(time.Unix(430, 0)),
		"type":      "event_msg",
		"payload": map[string]any{
			"type": "token_count",
			"info": map[string]any{
				"total_token_usage": map[string]any{"input_tokens": 1, "output_tokens": 1},
			},
		},
	}))
	if len(service.records) != 3 {
		t.Fatalf("token_count event should ingest legacy usage: %+v", service.records)
	}
}

func TestCodexResponseItemAndLegacyUsage(t *testing.T) {
	service := New(Config{})
	state := &fileState{}
	lines := []map[string]any{
		{"timestamp": ts(time.Unix(100, 0)), "type": "response_item", "payload": map[string]any{"type": "function_output"}},
		{"timestamp": ts(time.Unix(110, 0)), "type": "response_item", "payload": map[string]any{"type": "message", "role": "user"}},
		{"timestamp": ts(time.Unix(120, 0)), "type": "response_item", "payload": map[string]any{"type": "message", "role": "assistant"}},
		{"timestamp": ts(time.Unix(130, 0)), "type": "response_item", "payload": map[string]any{"type": "reasoning"}},
		{"timestamp": ts(time.Unix(140, 0)), "type": "response_item", "payload": map[string]any{"type": "function_call"}},
	}
	for _, line := range lines {
		service.ingestCodex("codex.jsonl", state, mustJSON(line))
	}
	if !state.streaming || !state.active {
		t.Fatalf("response items should mark output streaming: %+v", state)
	}

	streaming := &fileState{streaming: true}
	codexInput(streaming, 1)
	if streaming.active {
		t.Fatal("codexInput should not restart while streaming")
	}
	output := &fileState{}
	codexOutput(output, 25, false, false)
	if !output.hasPending || output.pendingMS != 25 || output.inFlightMS != 25 {
		t.Fatalf("codexOutput initialization = %+v", output)
	}
}

func TestCodexLegacyUsageBranches(t *testing.T) {
	service := New(Config{})
	info := &codexTokenInfo{
		TotalTokenUsage: codexUsagePtr(10, 20),
		LastTokenUsage:  codexUsagePtr(1, 2),
	}
	state := &fileState{modernUsage: true, streaming: true, active: true}
	service.ingestCodexLegacy("legacy.jsonl", state, 100, info)
	if state.active || state.streaming || !state.hasPending || state.pendingMS != 100 {
		t.Fatalf("modern streaming state = %+v", state)
	}

	modern := &fileState{modernUsage: true}
	service.ingestCodexLegacy("legacy.jsonl", modern, 100, info)
	if modern.hasPending {
		t.Fatalf("modern non-streaming usage should be ignored: %+v", modern)
	}
	service.ingestCodexLegacy("legacy.jsonl", modern, 100, nil)

	state = &fileState{model: "gpt", pendingMS: 50, hasPending: true}
	service.ingestCodexLegacy("legacy.jsonl", state, 100, info)
	record := service.records["legacy:legacy.jsonl:20"]
	if record.usage.total != 2 {
		t.Fatalf("first legacy sample should use last usage: %+v", record)
	}

	next := &codexTokenInfo{
		TotalTokenUsage: codexUsagePtr(15, 30),
	}
	service.ingestCodexLegacy("legacy.jsonl", state, 110, next)
	if got := service.records["legacy:legacy.jsonl:30"]; got.usage.total != 10 {
		t.Fatalf("legacy delta should use total increase: %+v", got)
	}

	negative := &codexTokenInfo{
		TotalTokenUsage: codexUsagePtr(3, 6),
		LastTokenUsage:  codexUsagePtr(1, 3),
	}
	service.ingestCodexLegacy("legacy.jsonl", state, 120, negative)
	if got := service.records["legacy:legacy.jsonl:6"]; got.usage.total != 3 {
		t.Fatalf("negative delta should use last usage: %+v", got)
	}

	service.ingestCodexLegacy("legacy.jsonl", state, 130, &codexTokenInfo{
		TotalTokenUsage: codexUsagePtr(3, 6),
	})
	otherState := &fileState{}
	service.ingestCodexLegacy(otherFileID, otherState, 140, &codexTokenInfo{
		TotalTokenUsage: codexUsagePtr(3, 6),
	})
	service.ingestCodexLegacy(otherFileID, &fileState{}, 150, &codexTokenInfo{
		TotalTokenUsage: codexUsagePtr(3, 6),
	})
	if got := service.claim("legacy:"+otherFileID+":6", "another"); got != dedupOtherFile {
		t.Fatalf("legacy dedup claim = %v", got)
	}

	service.ingestCodexLegacy("nil.jsonl", &fileState{}, 160, nil)
}

const otherFileID = "other.jsonl"

func TestCodexNormalizationAndTimestampHelpers(t *testing.T) {
	if got := codexUsagePtr(10, 20).normalized(); got.total != 20 || got.input != 10 || got.output != 10 {
		t.Fatalf("normalized usage = %+v", got)
	}
	if got := (&codexUsage{InputTokens: json.Number("5"), OutputTokens: json.Number("6")}).normalized(); got.total != 11 {
		t.Fatalf("zero total should be derived: %+v", got)
	}
	smallTotal := codexUsage{
		InputTokens:  json.Number("5"),
		OutputTokens: json.Number("6"),
		TotalTokens:  json.Number("1"),
	}
	if got := smallTotal.normalized(); got.total != 11 {
		t.Fatalf("small total should be raised: %+v", got)
	}
	current := usage{input: 10, cached: 10, cacheNew: 10, output: 10, reasoning: 10, total: 10}
	previous := usage{input: 5, cached: 6, cacheNew: 7, output: 8, reasoning: 9, total: 10}
	delta, negative := subtractUsage(current, previous)
	if delta.input != 5 || delta.cached != 4 || delta.cacheNew != 3 ||
		delta.output != 2 || delta.reasoning != 1 || negative {
		t.Fatalf("usage delta = %+v negative=%v", delta, negative)
	}
	if got := numberTokens(json.Number("bad")); got != 0 {
		t.Fatalf("invalid token number = %d", got)
	}
	if _, ok := parseTimestamp("bad"); ok {
		t.Fatal("invalid timestamp should be rejected")
	}
}

func codexUsagePtr(input, total int) *codexUsage {
	return &codexUsage{
		InputTokens:  json.Number(strconv.Itoa(input)),
		OutputTokens: json.Number(strconv.Itoa(total - input)),
		TotalTokens:  json.Number(strconv.Itoa(total)),
	}
}
