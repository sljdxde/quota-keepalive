package tokenspeed

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCodexMeasuredLiveWindow(t *testing.T) {
	start := time.Now().UTC().Add(-time.Minute)
	root := t.TempDir()
	path := filepath.Join(root, "2026", "10", "30", "session.jsonl")
	writeLines(t, path,
		map[string]any{
			"timestamp": ts(start),
			"type":      "session_meta",
			"payload":   map[string]any{"id": "thread-1"},
		},
		map[string]any{
			"timestamp": ts(start),
			"type":      "event_msg",
			"payload":   map[string]any{"type": "task_started"},
		},
		map[string]any{
			"timestamp": ts(start.Add(500 * time.Millisecond)),
			"type":      "response_item",
			"payload":   map[string]any{"type": "magpie_reasoning"},
		},
		map[string]any{
			"timestamp": ts(start.Add(time.Second)),
			"type":      "response_item",
			"payload": map[string]any{
				"type": "message",
				"role": "assistant",
			},
		},
		map[string]any{
			"timestamp": ts(start.Add(2 * time.Second)),
			"type":      "token_usage_record",
			"payload": map[string]any{
				"response_id": "resp-1",
				"usage": map[string]any{
					"input_tokens":            100,
					"output_tokens":           300,
					"reasoning_output_tokens": 120,
					"total_tokens":            400,
				},
			},
		},
	)

	now := start.Add(2500 * time.Millisecond)
	service := New(Config{
		Roots: []Root{{Source: "codex", Path: root}},
		Now:   func() time.Time { return now },
	})
	service.Poll()

	snapshot := service.Snapshot()
	assertNear(t, snapshot.OutputTokensPerSecond, 180)
	assertNear(t, snapshot.TotalTokensPerSecond, 230)
	assertNear(t, snapshot.InputTokensPerSecond, 50)
	if len(snapshot.Sources) != 1 || snapshot.Sources[0].Label != "Codex" {
		t.Fatalf("来源快照错误: %+v", snapshot.Sources)
	}
	if len(snapshot.Sessions) != 1 {
		t.Fatalf("会话快照错误: %+v", snapshot.Sessions)
	}
	if snapshot.Sessions[0].SourceLabel != "Codex" || snapshot.Sessions[0].Generating {
		t.Fatalf("会话来源或状态错误: %+v", snapshot.Sessions[0])
	}
	if snapshot.Title != ThemeTitle(TitleThemeVehicles, snapshot.OutputTokensPerSecond, true) ||
		snapshot.TitleTheme != "交通工具" {
		t.Fatalf("速度称号错误: title=%q theme=%q", snapshot.Title, snapshot.TitleTheme)
	}
	if snapshot.TodayTotalTokens != 400 || snapshot.TodayTokenDate != beijingDay(now) {
		t.Fatalf("今日累计错误: total=%d date=%q", snapshot.TodayTotalTokens, snapshot.TodayTokenDate)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), root) || strings.Contains(string(encoded), path) {
		t.Fatal("会话快照不应泄露本机日志路径")
	}
}

func TestTodayTotalCountsOldTranscriptsAndDeduplicatesUpdates(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, beijingZone)
	start := now.Add(-2 * time.Hour)
	root := t.TempDir()
	path := filepath.Join(root, "today.jsonl")
	writeLines(t, path,
		map[string]any{"timestamp": ts(start), "type": "session_meta", "payload": map[string]any{"id": "thread-1"}},
		map[string]any{
			"timestamp": ts(start),
			"type":      "token_usage_record",
			"payload": map[string]any{
				"response_id": "resp-older-than-live-window",
				"usage":       map[string]any{"input_tokens": 100, "output_tokens": 300, "total_tokens": 400},
			},
		},
	)
	if err := os.Chtimes(path, start, start); err != nil {
		t.Fatal(err)
	}

	service := New(Config{
		Roots: []Root{{Source: "codex", Path: root}},
		Now:   func() time.Time { return now },
	})
	service.Poll()
	if got := service.Snapshot().TodayTotalTokens; got != 400 {
		t.Fatalf("今日累计应包含实时窗口外的当日记录，实际 %d", got)
	}

	service.mu.Lock()
	service.upsertRecord(responseRecord{
		key:   "codex:resp-older-than-live-window",
		endMS: start.UnixMilli(),
		usage: usage{total: 650},
	})
	service.mu.Unlock()
	service.Poll()
	if got := service.Snapshot().TodayTotalTokens; got != 650 {
		t.Fatalf("相同 response 的累加更新应替换旧贡献，实际 %d", got)
	}

	service.mu.Lock()
	service.upsertRecord(responseRecord{
		key:   "codex:yesterday",
		endMS: start.Add(-24 * time.Hour).UnixMilli(),
		usage: usage{total: 900},
	})
	service.mu.Unlock()
	service.Poll()
	if got := service.Snapshot().TodayTotalTokens; got != 650 {
		t.Fatalf("昨日记录不应计入今日累计，实际 %d", got)
	}
}

func TestCodexActiveEstimateUsesRecentReference(t *testing.T) {
	start := time.Now().UTC().Add(-time.Minute)
	root := t.TempDir()
	path := filepath.Join(root, "session.jsonl")
	writeLines(t, path,
		map[string]any{"timestamp": ts(start), "type": "session_meta", "payload": map[string]any{"id": "thread-1"}},
		map[string]any{"timestamp": ts(start), "type": "event_msg", "payload": map[string]any{"type": "task_started"}},
		map[string]any{
			"timestamp": ts(start.Add(time.Second)),
			"type":      "response_item",
			"payload": map[string]any{
				"type": "message",
				"role": "assistant",
			},
		},
		map[string]any{
			"timestamp": ts(start.Add(3 * time.Second)),
			"type":      "token_usage_record",
			"payload": map[string]any{
				"response_id": "resp-1",
				"usage":       map[string]any{"input_tokens": 0, "output_tokens": 100, "total_tokens": 100},
			},
		},
	)

	now := start.Add(3500 * time.Millisecond)
	service := New(Config{Roots: []Root{{Source: "codex", Path: root}}, Now: func() time.Time { return now }})
	service.Poll()

	appendLines(t, path,
		map[string]any{
			"timestamp": ts(start.Add(5 * time.Second)),
			"type":      "event_msg",
			"payload":   map[string]any{"type": "task_started"},
		},
		map[string]any{
			"timestamp": ts(start.Add(6 * time.Second)),
			"type":      "response_item",
			"payload":   map[string]any{"type": "reasoning"},
		},
	)
	now = start.Add(7500 * time.Millisecond)
	service.Poll()

	snapshot := service.Snapshot()
	assertNear(t, snapshot.OutputTokensPerSecond, 50)
	if snapshot.GeneratingAgents != 1 || !snapshot.Active {
		t.Fatalf("应识别到一个正在生成的 Agent: %+v", snapshot)
	}
}

func TestCodexIdleReturnsZeroAndDuplicateUsageIsIgnored(t *testing.T) {
	start := time.Now().UTC().Add(-time.Minute)
	root := t.TempDir()
	path := filepath.Join(root, "session.jsonl")
	usage := map[string]any{
		"timestamp": ts(start.Add(2 * time.Second)),
		"type":      "token_usage_record",
		"payload": map[string]any{
			"response_id": "resp-1",
			"usage":       map[string]any{"output_tokens": 100, "total_tokens": 100},
		},
	}
	writeLines(t, path,
		map[string]any{"timestamp": ts(start), "type": "session_meta", "payload": map[string]any{"id": "thread-1"}},
		map[string]any{"timestamp": ts(start), "type": "event_msg", "payload": map[string]any{"type": "task_started"}},
		usage,
		usage,
	)

	now := start.Add(2 * time.Second)
	service := New(Config{Roots: []Root{{Source: "codex", Path: root}}, Now: func() time.Time { return now }})
	service.Poll()
	if len(service.records) != 1 {
		t.Fatalf("重复 response_id 应只保留一条记录，实际 %d", len(service.records))
	}

	now = start.Add(10 * time.Second)
	service.Poll()
	if got := service.Snapshot().OutputTokensPerSecond; got != 0 {
		t.Fatalf("空闲窗口应为 0，实际 %.3f", got)
	}
}

func TestClaudeMeasuredSpeed(t *testing.T) {
	start := time.Now().UTC().Add(-time.Minute)
	root := t.TempDir()
	path := filepath.Join(root, "project", "session.jsonl")
	writeLines(t, path,
		map[string]any{
			"timestamp": ts(start),
			"type":      "api-request",
			"requestId": "req-1",
		},
		map[string]any{
			"timestamp": ts(start.Add(2 * time.Second)),
			"type":      "assistant",
			"requestId": "req-1",
			"message": map[string]any{
				"id":          "msg-1",
				"model":       "claude-opus-5-5",
				"stop_reason": "end_turn",
				"content":     []any{map[string]any{"type": "text"}},
				"usage": map[string]any{
					"input_tokens":                10,
					"cache_creation_input_tokens": 5,
					"cache_read_input_tokens":     20,
					"output_tokens":               1200,
				},
			},
		},
	)
	now := start.Add(2500 * time.Millisecond)
	service := New(Config{Roots: []Root{{Source: "claude", Path: root}}, Now: func() time.Time { return now }})
	service.Poll()

	snapshot := service.Snapshot()
	assertNear(t, snapshot.OutputTokensPerSecond, 600)
	if len(snapshot.Sources) != 1 || snapshot.Sources[0].Label != "Claude Code" {
		t.Fatalf("Claude 来源快照错误: %+v", snapshot.Sources)
	}
}

func TestPartialLineWaitsForNewline(t *testing.T) {
	start := time.Now().UTC().Add(-time.Minute)
	root := t.TempDir()
	path := filepath.Join(root, "session.jsonl")
	first := mustJSON(map[string]any{
		"timestamp": ts(start),
		"type":      "session_meta",
		"payload":   map[string]any{"id": "thread-1"},
	})
	second := mustJSON(map[string]any{
		"timestamp": ts(start.Add(time.Second)),
		"type":      "event_msg",
		"payload":   map[string]any{"type": "task_started"},
	})
	if err := os.WriteFile(path, append(append(first, '\n'), second...), 0o600); err != nil {
		t.Fatal(err)
	}
	now := start.Add(time.Second)
	service := New(Config{Roots: []Root{{Source: "codex", Path: root}}, Now: func() time.Time { return now }})
	service.Poll()
	if service.files[path] == nil || service.files[path].active {
		t.Fatal("未换行的半条 JSONL 不应被处理")
	}
	appendBytes(t, path, []byte("\n"))
	service.Poll()
	if !service.files[path].active {
		t.Fatal("补齐换行后应处理 task_started")
	}
}

func TestFormatSpeed(t *testing.T) {
	cases := []struct {
		value float64
		want  string
	}{
		{0, "0"},
		{0.04, "0.0"},
		{9.96, "10"},
		{12.4, "12"},
		{999.4, "999"},
		{999.6, "1.00k"},
		{1234, "1.23k"},
		{12345, "12.3k"},
	}
	for _, tc := range cases {
		if got := FormatSpeed(tc.value); got != tc.want {
			t.Errorf("FormatSpeed(%v) = %q, want %q", tc.value, got, tc.want)
		}
	}
}

func TestTransportTitle(t *testing.T) {
	cases := []struct {
		value  float64
		active bool
		want   string
	}{
		{0, false, "待机"},
		{0.4, true, "待机"},
		{1, true, "步行"},
		{8, true, "自行车"},
		{12, true, "电动车"},
		{16, true, "摩托车"},
		{24, true, "汽车"},
		{32, true, "跑车"},
		{48, true, "高铁"},
		{68, true, "赛车"},
		{96, true, "磁悬浮列车"},
		{136, true, "喷气客机"},
		{200, true, "战斗机"},
		{280, true, "高超音速飞机"},
		{400, true, "火箭"},
	}
	for _, tc := range cases {
		if got := TransportTitle(tc.value, tc.active); got != tc.want {
			t.Errorf("TransportTitle(%v, %v) = %q, want %q", tc.value, tc.active, got, tc.want)
		}
	}
}

func TestThemeTitleAndLabels(t *testing.T) {
	cases := []struct {
		theme string
		value float64
		want  string
	}{
		{TitleThemeVehicles, 500, "火箭"},
		{TitleThemeAnimals, 500, "游隼"},
		{TitleThemeAthletics, 500, "人类极限"},
		{TitleThemeMusic, 500, "超频爆速"},
		{TitleThemeNetwork, 500, "数据中心互联"},
		{TitleThemeSound, 500, "再入大气层"},
		{TitleThemeStudent, 500, "诺奖级"},
		{TitleThemeCareer, 500, "行业缔造者"},
		{TitleThemeSpace, 500, "大爆炸"},
	}
	for _, tc := range cases {
		if got := ThemeTitle(tc.theme, tc.value, true); got != tc.want {
			t.Errorf("ThemeTitle(%q, %v) = %q, want %q", tc.theme, tc.value, got, tc.want)
		}
		if got := TitleThemeLabel(tc.theme); got == "" {
			t.Errorf("标题主题 %q 缺少显示名", tc.theme)
		}
	}
	if got := ThemeTitle(TitleThemeStudent, 0, false); got != "放假" {
		t.Errorf("学生生涯空闲称号应为放假，实际 %q", got)
	}
	catalog := TitleThemeCatalog()
	if len(catalog) != 9 || len(catalog[0].Thresholds) != 13 || len(catalog[0].Titles) != 13 {
		t.Fatalf("称号目录不完整: %+v", catalog)
	}
}

func TestServiceTitleThemeOverride(t *testing.T) {
	service := New(Config{Now: func() time.Time { return time.Unix(0, 0) }})
	service.SetTitleTheme(TitleThemeStudent)
	snapshot := service.Snapshot()
	if snapshot.TitleThemeID != TitleThemeStudent ||
		snapshot.TitleTheme != "学生生涯" ||
		snapshot.Title != "放假" {
		t.Fatalf("称号主题未应用: %+v", snapshot)
	}
}

func TestRetiredTitleThemeFallsBackToVehicles(t *testing.T) {
	if got := NormalizeTitleTheme("cultivation"); got != TitleThemeVehicles {
		t.Fatalf("旧称号主题应回退到 vehicles，实际 %q", got)
	}
}

func writeLines(t *testing.T, path string, lines ...map[string]any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var data []byte
	for _, line := range lines {
		data = append(data, mustJSON(line)...)
		data = append(data, '\n')
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func appendLines(t *testing.T, path string, lines ...map[string]any) {
	t.Helper()
	for _, line := range lines {
		appendBytes(t, path, append(mustJSON(line), '\n'))
	}
}

func appendBytes(t *testing.T, path string, data []byte) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func mustJSON(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}

func ts(value time.Time) string {
	return value.Format(time.RFC3339Nano)
}

func assertNear(t *testing.T, got, want float64) {
	t.Helper()
	diff := got - want
	if diff < 0 {
		diff = -diff
	}
	if diff > 0.01 {
		t.Fatalf("got %.3f, want %.3f", got, want)
	}
}
