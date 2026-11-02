package tokenspeed

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

// dshFrame 用真实 compress/zstd 编码一个会话 frame（一个或多个 JSONL 行），
// 测试不做任何解压库 mock。
func dshFrame(t *testing.T, lines ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer, err := zstd.NewWriter(&buf, zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatalf("创建 zstd writer 失败: %v", err)
	}
	for _, line := range lines {
		if _, err := writer.Write([]byte(line + "\n")); err != nil {
			t.Fatalf("写入 zstd 失败: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("关闭 zstd writer 失败: %v", err)
	}
	return buf.Bytes()
}

func dshWriteFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("创建目录失败: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("写会话文件失败: %v", err)
	}
}

func dshAppendFile(t *testing.T, path string, data []byte) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("打开会话文件失败: %v", err)
	}
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		t.Fatalf("追加会话文件失败: %v", err)
	}
}

// dshAssistantEvent 构造真实的 DSH assistant/message 事件（usage 在 data 内）。
func dshAssistantEvent(id string, at time.Time, input, output, total, cacheRead int64) string {
	return fmt.Sprintf(
		`{"type":"assistant/message","seq":%d,"time":%d,"data":{"turn":1,"step":1,`+
			`"message":{"role":"assistant","content":[{"type":"reasoning","text":"..."}]},`+
			`"source":{"kind":"model","provider":"deepseek-official","model":"deepseek-v4-flash"},`+
			`"id":%q,"usage":{"inputTokens":%d,"outputTokens":%d,"totalTokens":%d,`+
			`"cacheReadTokens":%d,"cacheWriteTokens":0}}}`,
		at.UnixMilli()%10000, at.UnixMilli(), id, input, output, total, cacheRead)
}

func dshSessionPath(root, session string) string {
	workspace := "--Users-test-workspace--"
	return filepath.Join(root, workspace, "session-"+session, "session.v4.jsonl.zstd")
}

func dshFixedNow() time.Time {
	return time.Date(2026, 11, 2, 10, 0, 0, 0, beijingZone)
}

func dshNewService(root string, now time.Time) *Service {
	return New(Config{
		Roots: []Root{{Source: "dsh", Path: root}},
		Now:   func() time.Time { return now },
	})
}

// TestDSHParsesTodayUsageFromZstdSession 验证真实 zstd 会话文件的今日总量、
// input/output/cacheRead 字段、来源与会话展示名，以及跨天事件不计入今日。
func TestDSHParsesTodayUsageFromZstdSession(t *testing.T) {
	root := t.TempDir()
	now := dshFixedNow()
	path := dshSessionPath(root, "abc12345-0000-4000-8000-000000000001")
	frame := dshFrame(t,
		`{"type":"session","version":4,"id":"abc12345","createdAt":1792486955000}`,
		dshAssistantEvent("msg-today", now, 3695, 654, 17661, 13312),
		`{"type":"step/start","seq":20,"time":1792486956000}`,
		dshAssistantEvent("msg-yesterday", now.AddDate(0, 0, -1), 100, 50, 150, 0),
		`{broken json`,
	)
	dshWriteFile(t, path, frame)

	service := dshNewService(root, now)
	service.Poll()
	snapshot := service.Snapshot()

	if snapshot.TodayTotalTokens != 17661 {
		t.Fatalf("今日总量 = %d, want 17661", snapshot.TodayTotalTokens)
	}
	if snapshot.TodayTokenDate != beijingDay(now) {
		t.Fatalf("统计日期 = %q, want %q", snapshot.TodayTokenDate, beijingDay(now))
	}
	if len(snapshot.Sources) != 1 {
		t.Fatalf("来源数 = %d, want 1: %+v", len(snapshot.Sources), snapshot.Sources)
	}
	source := snapshot.Sources[0]
	if source.Source != "dsh" || source.Label != "DeepSeek Harness" {
		t.Fatalf("来源标识/展示名错误: %+v", source)
	}
	if source.TodayInputTokens != 3695 || source.TodayOutputTokens != 654 ||
		source.TodayTotalTokens != 17661 {
		t.Fatalf("来源今日字段错误: %+v", source)
	}
	// DSH 没有首 token 时间戳：不得出现实测速率。
	if snapshot.OutputTokensPerSecond != 0 || source.OutputTokensPerSecond != 0 {
		t.Fatalf("DSH 不应输出实测速率: %+v", snapshot)
	}
	if len(snapshot.Sessions) != 1 {
		t.Fatalf("会话数 = %d, want 1: %+v", len(snapshot.Sessions), snapshot.Sessions)
	}
	session := snapshot.Sessions[0]
	if session.Source != "dsh" || session.SourceLabel != "DeepSeek Harness" ||
		session.Model != "deepseek-v4-flash" {
		t.Fatalf("会话投影错误: %+v", session)
	}

	contributions := service.todayBySource["dsh"]
	if contributions.input != 3695 || contributions.output != 654 ||
		contributions.total != 17661 {
		t.Fatalf("今日来源聚合错误: %+v", contributions)
	}
	record, ok := service.records["dsh:abc12345-0000-4000-8000-000000000001:msg-today"]
	if !ok || !record.noMeasuredRate || record.usage.total != 17661 ||
		record.usage.cached != 13312 {
		t.Fatalf("事件记录错误: %+v ok=%v", record, ok)
	}
}

// TestDSHSkipsUnchangedFile 验证文件大小与 mtime 未变化时不会重复解压：
// 把文件内容替换成同样长度的垃圾字节并恢复 mtime，总量必须保持原值。
func TestDSHSkipsUnchangedFile(t *testing.T) {
	root := t.TempDir()
	now := dshFixedNow()
	path := dshSessionPath(root, "session-cache")
	original := dshFrame(t, dshAssistantEvent("msg-1", now, 10, 20, 30, 0))
	dshWriteFile(t, path, original)

	service := dshNewService(root, now)
	service.Poll()
	if got := service.Snapshot().TodayTotalTokens; got != 30 {
		t.Fatalf("首次解析 today = %d, want 30", got)
	}
	state := service.dsh[path]
	if state == nil || state.compressedOffset != int64(len(original)) {
		t.Fatalf("解压游标错误: %+v", state)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	// 同长度替换 + 恢复 mtime：模拟「没有变化」，不应再次解压。
	garbage := bytes.Repeat([]byte{0xde, 0xad}, len(original)/2)
	if len(original)%2 != 0 {
		garbage = append(garbage, 0xbe)
	}
	if err := os.WriteFile(path, garbage, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	service.Poll()
	if got := service.Snapshot().TodayTotalTokens; got != 30 {
		t.Fatalf("文件未变化却重新解压: today = %d, want 30", got)
	}
}

// TestDSHAppendIsIncrementalAndDeduped 验证追加 frame 后只解压新增部分、
// 多 frame 连续追加可解析、文件重写回退全量解压时不会重复累计。
func TestDSHAppendIsIncrementalAndDeduped(t *testing.T) {
	root := t.TempDir()
	now := dshFixedNow()
	path := dshSessionPath(root, "session-append")
	first := dshFrame(t, dshAssistantEvent("msg-1", now, 10, 20, 30, 0))
	dshWriteFile(t, path, first)

	service := dshNewService(root, now)
	service.Poll()
	if got := service.Snapshot().TodayTotalTokens; got != 30 {
		t.Fatalf("初始 today = %d, want 30", got)
	}
	firstSize := int64(len(first))

	second := dshFrame(t, dshAssistantEvent("msg-2", now, 5, 15, 20, 0))
	dshAppendFile(t, path, second)
	service.Poll()
	if got := service.Snapshot().TodayTotalTokens; got != 50 {
		t.Fatalf("追加后 today = %d, want 50（不得重复累计 msg-1）", got)
	}
	if state := service.dsh[path]; state.compressedOffset != firstSize+int64(len(second)) {
		t.Fatalf("追加后解压游标错误: %+v", state)
	}

	// 一次追加多个连续 frame，解码器必须一次消费完整段。
	third := dshFrame(t, dshAssistantEvent("msg-3", now, 1, 9, 10, 0))
	fourth := dshFrame(t, dshAssistantEvent("msg-4", now, 1, 14, 15, 0))
	dshAppendFile(t, path, append(third, fourth...))
	service.Poll()
	if got := service.Snapshot().TodayTotalTokens; got != 75 {
		t.Fatalf("多 frame 追加后 today = %d, want 75", got)
	}

	// 文件被整体重写（非追加）：回退整文件解压，重复事件不得二次累计，新事件正常计入。
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	rewritten := dshFrame(t,
		dshAssistantEvent("msg-1", now, 10, 20, 30, 0),
		dshAssistantEvent("msg-2", now, 5, 15, 20, 0),
		dshAssistantEvent("msg-3", now, 1, 9, 10, 0),
		dshAssistantEvent("msg-4", now, 1, 14, 15, 0),
		dshAssistantEvent("msg-5", now, 2, 8, 10, 0),
	)
	dshWriteFile(t, path, rewritten)
	if err := os.Chtimes(path, info.ModTime().Add(time.Second), info.ModTime().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	service.Poll()
	if got := service.Snapshot().TodayTotalTokens; got != 85 {
		t.Fatalf("重写后 today = %d, want 85（重复事件去重 + 新事件计入）", got)
	}
	if state := service.dsh[path]; state.compressedOffset != int64(len(rewritten)) {
		t.Fatalf("重写后解压游标错误: %+v", state)
	}
}

// TestDSHMalformedAndIncompleteFiles 验证垃圾文件不会 panic，末尾 frame 不完整时
// 下一轮写入完整后可恢复。
func TestDSHMalformedAndIncompleteFiles(t *testing.T) {
	root := t.TempDir()
	now := dshFixedNow()
	garbagePath := dshSessionPath(root, "session-garbage")
	dshWriteFile(t, garbagePath, []byte("not a zstd stream at all"))

	truncatedPath := dshSessionPath(root, "session-truncated")
	frame := dshFrame(t, dshAssistantEvent("msg-1", now, 1, 2, 3, 0))
	dshWriteFile(t, truncatedPath, frame[:len(frame)-5])

	service := dshNewService(root, now)
	service.Poll()
	if got := service.Snapshot().TodayTotalTokens; got != 0 {
		t.Fatalf("损坏文件不应产生总量: %d", got)
	}
	if !service.availableSources["dsh"] {
		t.Fatal("存在 dsh 目录时来源应可用")
	}

	// 补全被截断的 frame 后应能读到事件。
	dshWriteFile(t, truncatedPath, frame)
	service.Poll()
	if got := service.Snapshot().TodayTotalTokens; got != 3 {
		t.Fatalf("补全后 today = %d, want 3", got)
	}
}

// TestDSHDefaultConfigAndLabel 验证 DSH_HOME 覆盖与展示名。
func TestDSHDefaultConfigAndLabel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DSH_HOME", home)
	config := DefaultConfig()
	found := false
	for _, root := range config.Roots {
		if root.Source == "dsh" {
			found = true
			if want := filepath.Join(home, "sessions"); root.Path != want {
				t.Fatalf("dsh 根目录 = %q, want %q", root.Path, want)
			}
		}
	}
	if !found {
		t.Fatalf("DefaultConfig 未注册 dsh 源: %+v", config.Roots)
	}
	if label := sourceLabel("dsh"); label != "DeepSeek Harness" {
		t.Fatalf("dsh 展示名 = %q", label)
	}
}
