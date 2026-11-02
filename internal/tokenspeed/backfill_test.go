package tokenspeed

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// withBackfillLimits 用可控的小阈值替换包级阈值，便于构造超大会话文件。
func withBackfillLimits(t *testing.T, tail, chunk, budget int64) {
	t.Helper()
	oldTail, oldChunk, oldBudget := maxInitialTail, backfillChunk, backfillBudget
	maxInitialTail, backfillChunk, backfillBudget = tail, chunk, budget
	t.Cleanup(func() {
		maxInitialTail, backfillChunk, backfillBudget = oldTail, oldChunk, oldBudget
	})
}

// pollUntilBackfillDone 反复 Poll 直到该文件的历史补读结束；有上限防止死循环。
func pollUntilBackfillDone(t *testing.T, service *Service, path string) {
	t.Helper()
	for i := 0; i < 2000; i++ {
		service.Poll()
		service.mu.Lock()
		state := service.files[path]
		done := state == nil || state.backfill == nil
		service.mu.Unlock()
		if done {
			return
		}
	}
	t.Fatalf("历史补读 %d 次 Poll 后仍未完成", 2000)
}

// TestBackfillRestoresLargeTranscriptTodayTotal 覆盖：超大会话文件首次只读
// 尾部会导致当日 token 总量偏低，历史补读完成后应等于全量。
func TestBackfillRestoresLargeTranscriptTodayTotal(t *testing.T) {
	withBackfillLimits(t, 2048, 512, 4096)

	now := time.Now().UTC()
	start := now.Add(-2 * time.Minute)
	root := t.TempDir()
	path := filepath.Join(root, "2026", "11", "02", "rollout-big.jsonl")

	const records = 120
	const perRecord = 100
	lines := make([]map[string]any, 0, records+1)
	lines = append(lines, map[string]any{
		"timestamp": ts(start),
		"type":      "session_meta",
		"payload":   map[string]any{"id": "thread-big"},
	})
	for i := 0; i < records; i++ {
		lines = append(lines, map[string]any{
			"timestamp": ts(start.Add(time.Duration(i) * time.Millisecond)),
			"type":      "token_usage_record",
			"payload": map[string]any{
				"response_id": fmt.Sprintf("resp-%03d", i),
				"usage": map[string]any{
					"input_tokens":  perRecord / 2,
					"output_tokens": perRecord / 2,
					"total_tokens":  perRecord,
				},
			},
		})
	}
	writeLines(t, path, lines...)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() <= maxInitialTail {
		t.Fatalf("测试文件 %d 字节，未超过尾部阈值 %d", info.Size(), maxInitialTail)
	}

	service := New(Config{
		Roots: []Root{{Source: "codex", Path: root}},
		Now:   func() time.Time { return now },
	})
	service.Poll()
	// 首次只读尾部时当日总量必然偏低，这正是被修复的缺陷。
	if got := service.Snapshot().TodayTotalTokens; got >= records*perRecord {
		t.Fatalf("首次 Poll 不应已读到全部历史，实际 %d", got)
	}

	pollUntilBackfillDone(t, service, path)
	want := int64(records * perRecord)
	if got := service.Snapshot().TodayTotalTokens; got != want {
		t.Fatalf("补读后当日总量 = %d，期望 %d", got, want)
	}
	// 补读完成后文件读取游标应停在文件末尾，后续轮询不会重复计入。
	service.mu.Lock()
	offset := service.files[path].offset
	backfill := service.files[path].backfill
	service.mu.Unlock()
	if backfill != nil {
		t.Fatal("补读完成后仍保留回填游标")
	}
	if offset != info.Size() {
		t.Fatalf("实时游标 = %d，期望文件大小 %d", offset, info.Size())
	}
}

// TestBackfillDoesNotChangeLiveRate 覆盖：历史补读进行中与完成后，实时速率
// 都应来自尾部真实响应，不被补读顺序干扰。
func TestBackfillDoesNotChangeLiveRate(t *testing.T) {
	withBackfillLimits(t, 4096, 512, 4096)

	now := time.Now().UTC()
	start := now.Add(-time.Minute)
	root := t.TempDir()
	path := filepath.Join(root, "2026", "11", "02", "rollout-live.jsonl")

	lines := make([]map[string]any, 0, 200)
	// 填充历史：早期 user 消息，只为把实时尾部推远，不产生 token 记录。
	for i := 0; i < 80; i++ {
		lines = append(lines, map[string]any{
			"timestamp": ts(start.Add(-time.Hour + time.Duration(i)*time.Second)),
			"type":      "response_item",
			"payload":   map[string]any{"type": "message", "role": "user"},
		})
	}
	// 实时尾部：与既有 TestCodexMeasuredLiveWindow 相同的一次可测量响应。
	lines = append(lines,
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
			"payload":   map[string]any{"type": "message", "role": "assistant"},
		},
		map[string]any{
			"timestamp": ts(start.Add(2 * time.Second)),
			"type":      "token_usage_record",
			"payload": map[string]any{
				"response_id": "resp-live",
				"usage": map[string]any{
					"input_tokens":            100,
					"output_tokens":           300,
					"reasoning_output_tokens": 120,
					"total_tokens":            400,
				},
			},
		},
	)
	writeLines(t, path, lines...)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() <= maxInitialTail {
		t.Fatalf("测试文件 %d 字节，未超过尾部阈值 %d", info.Size(), maxInitialTail)
	}

	service := New(Config{
		Roots: []Root{{Source: "codex", Path: root}},
		Now:   func() time.Time { return start.Add(2500 * time.Millisecond) },
	})
	service.Poll()
	before := service.Snapshot().OutputTokensPerSecond
	assertNear(t, before, 180)
	service.mu.Lock()
	pending := service.files[path].backfill != nil
	service.mu.Unlock()
	if !pending {
		t.Fatal("首次 Poll 后应仍有待补读历史，测试未覆盖补读进行中的场景")
	}

	pollUntilBackfillDone(t, service, path)
	after := service.Snapshot().OutputTokensPerSecond
	assertNear(t, after, 180)
	if after != before {
		t.Fatalf("历史补读改变了实时速率：before=%.3f after=%.3f", before, after)
	}
	if got := service.Snapshot().TodayTotalTokens; got != 400 {
		t.Fatalf("实时响应的当日总量 = %d，期望 400", got)
	}
}

// TestBackfillResetsSafelyOnTranscriptShrink 覆盖 Poll 边界：会话文件被截断
// 或轮转时补读游标必须安全重置，不 panic、不残留旧游标。
func TestBackfillResetsSafelyOnTranscriptShrink(t *testing.T) {
	withBackfillLimits(t, 1024, 256, 1024)

	now := time.Now().UTC()
	start := now.Add(-time.Minute)
	root := t.TempDir()
	path := filepath.Join(root, "rollout-shrink.jsonl")

	lines := make([]map[string]any, 0, 80)
	for i := 0; i < 60; i++ {
		lines = append(lines, map[string]any{
			"timestamp": ts(start.Add(time.Duration(i) * time.Millisecond)),
			"type":      "token_usage_record",
			"payload": map[string]any{
				"response_id": fmt.Sprintf("resp-shrink-%02d", i),
				"usage":       map[string]any{"input_tokens": 10, "output_tokens": 10, "total_tokens": 20},
			},
		})
	}
	writeLines(t, path, lines...)
	service := New(Config{
		Roots: []Root{{Source: "codex", Path: root}},
		Now:   func() time.Time { return now },
	})
	service.Poll()

	// 截断并写入一份全新的短文件：等价于编辑器重写/日志轮转。
	writeLines(t, path, map[string]any{
		"timestamp": ts(start.Add(2 * time.Minute)),
		"type":      "token_usage_record",
		"payload": map[string]any{
			"response_id": "resp-rotated",
			"usage":       map[string]any{"input_tokens": 5, "output_tokens": 5, "total_tokens": 10},
		},
	})
	service.Poll()

	snapshot := service.Snapshot()
	if snapshot.UpdatedAt == "" {
		t.Fatal("截断后快照未更新")
	}
	service.mu.Lock()
	state := service.files[path]
	service.mu.Unlock()
	if state == nil {
		t.Fatal("截断后文件状态不应消失")
	}
	if state.backfill != nil {
		t.Fatal("截断后不应继续补读旧文件区间")
	}
	if state.offset > 1<<20 {
		t.Fatalf("截断后实时游标残留旧偏移：%d", state.offset)
	}
	if got := snapshot.TodayTotalTokens; got < 10 {
		t.Fatalf("轮转后的新记录未计入当日总量：%d", got)
	}
}
