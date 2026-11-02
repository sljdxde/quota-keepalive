package tokenspeed

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestDSHDiscoveryEdgeCases 覆盖 dsh 根缺失/非目录、后缀与符号链接过滤、
// mtime 过滤、文件数上限与消失文件清理等发现分支。
func TestDSHDiscoveryEdgeCases(t *testing.T) {
	t.Run("根目录缺失或不是目录", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "missing")
		service := New(Config{Roots: []Root{{Source: "dsh", Path: root}},
			Now: func() time.Time { return dshFixedNow() }})
		service.Poll()
		if len(service.dsh) != 0 {
			t.Fatalf("缺失根目录不应产生文件状态: %+v", service.dsh)
		}
		fileRoot := filepath.Join(t.TempDir(), "as-file")
		if err := os.WriteFile(fileRoot, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		service = New(Config{Roots: []Root{{Source: "dsh", Path: fileRoot}},
			Now: func() time.Time { return dshFixedNow() }})
		service.Poll()
		if service.availableSources["dsh"] {
			t.Fatal("文件路径不应被当作 dsh 目录")
		}
	})

	t.Run("后缀与符号链接过滤", func(t *testing.T) {
		root := t.TempDir()
		now := dshFixedNow()
		realFile := dshSessionPath(root, "real")
		dshWriteFile(t, realFile, dshFrame(t, dshAssistantEvent("m1", now, 1, 2, 3, 0)))
		if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(realFile, filepath.Join(root, "link.jsonl.zstd")); err != nil {
			t.Fatal(err)
		}
		service := dshNewService(root, now)
		service.Poll()
		if _, ok := service.dsh[realFile]; !ok {
			t.Fatal("真实 dsh 文件应被读取")
		}
		if len(service.dsh) != 1 {
			t.Fatalf("非 dsh 后缀/符号链接不应进入状态: %+v", service.dsh)
		}
	})

	t.Run("mtime 早于今日", func(t *testing.T) {
		root := t.TempDir()
		now := dshFixedNow()
		path := dshSessionPath(root, "old")
		dshWriteFile(t, path, dshFrame(t, dshAssistantEvent("old1", now, 10, 20, 30, 0)))
		yesterday := now.AddDate(0, 0, -1)
		if err := os.Chtimes(path, yesterday, yesterday); err != nil {
			t.Fatal(err)
		}
		service := dshNewService(root, now)
		service.Poll()
		if len(service.dsh) != 0 {
			t.Fatalf("历史文件不应被读取: %+v", service.dsh)
		}
		if got := service.Snapshot().TodayTotalTokens; got != 0 {
			t.Fatalf("历史文件不应计入今日: %d", got)
		}
	})

	t.Run("walk 错误被忽略", func(t *testing.T) {
		root := t.TempDir()
		oldWalk := walkDir
		t.Cleanup(func() { walkDir = oldWalk })
		walkDir = func(string, fs.WalkDirFunc) error {
			return nil
		}
		service := dshNewService(root, dshFixedNow())
		service.Poll() // 不应 panic；availableSources 已标记但没读到文件
		if len(service.dsh) != 0 {
			t.Fatalf("walk 未访问文件: %+v", service.dsh)
		}
	})

	t.Run("walkErr 回调被忽略", func(t *testing.T) {
		root := t.TempDir()
		oldWalk := walkDir
		t.Cleanup(func() { walkDir = oldWalk })
		walkDir = func(root string, fn fs.WalkDirFunc) error {
			return fn(root, nil, errors.New("权限不足"))
		}
		service := dshNewService(root, dshFixedNow())
		service.Poll()
		if len(service.dsh) != 0 {
			t.Fatalf("walkErr 不应产生状态: %+v", service.dsh)
		}
	})

	t.Run("文件数上限", func(t *testing.T) {
		root := t.TempDir()
		now := dshFixedNow()
		dshWriteFile(t, dshSessionPath(root, "a"), dshFrame(t, dshAssistantEvent("a1", now, 1, 1, 2, 0)))
		dshWriteFile(t, dshSessionPath(root, "b"), dshFrame(t, dshAssistantEvent("b1", now, 1, 1, 2, 0)))
		oldMax := maxFilesPerRoot
		maxFilesPerRoot = 1
		t.Cleanup(func() { maxFilesPerRoot = oldMax })
		service := dshNewService(root, now)
		service.Poll()
		if len(service.dsh) != 1 {
			t.Fatalf("达到文件上限后应停止遍历: %+v", service.dsh)
		}
	})

	t.Run("消失文件清理游标", func(t *testing.T) {
		root := t.TempDir()
		now := dshFixedNow()
		path := dshSessionPath(root, "gone")
		dshWriteFile(t, path, dshFrame(t, dshAssistantEvent("g1", now, 1, 1, 2, 0)))
		service := dshNewService(root, now)
		service.Poll()
		if _, ok := service.dsh[path]; !ok {
			t.Fatal("首次轮询应建立游标")
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		service.Poll()
		if _, ok := service.dsh[path]; ok {
			t.Fatal("文件消失后应清理游标")
		}
	})
}

// TestDSHReadFileErrorPaths 覆盖打开失败、非普通文件、尾部 frame 失效回退等分支。
func TestDSHReadFileErrorPaths(t *testing.T) {
	root := t.TempDir()
	now := dshFixedNow()
	path := dshSessionPath(root, "errs")
	dshWriteFile(t, path, dshFrame(t, dshAssistantEvent("e1", now, 1, 1, 2, 0)))

	t.Run("打开失败", func(t *testing.T) {
		oldOpen := openTranscript
		t.Cleanup(func() { openTranscript = oldOpen })
		openTranscript = func(string) (transcriptFile, error) {
			return nil, errors.New("open denied")
		}
		service := dshNewService(root, now)
		service.Poll()
		if len(service.dsh) != 0 {
			t.Fatalf("打开失败不应建立状态: %+v", service.dsh)
		}
	})

	t.Run("非普通文件", func(t *testing.T) {
		oldOpen := openTranscript
		t.Cleanup(func() { openTranscript = oldOpen })
		openTranscript = func(string) (transcriptFile, error) {
			return &stubTranscriptFile{data: []byte("x")}, nil
		}
		service := dshNewService(root, now)
		service.Poll()
		if len(service.dsh) != 0 {
			t.Fatalf("非普通文件（Stat 失败）不应建立状态: %+v", service.dsh)
		}
	})

	t.Run("尾部不是独立 frame 时回退整文件", func(t *testing.T) {
		service := dshNewService(root, now)
		service.Poll()
		if got := service.Snapshot().TodayTotalTokens; got != 2 {
			t.Fatalf("首次 today = %d, want 2", got)
		}
		// 整体重写为更大的单 frame（旧游标落在新 frame 中间，尾部解压失败）。
		larger := dshFrame(t,
			dshAssistantEvent("e1", now, 1, 1, 2, 0),
			dshAssistantEvent("e2", now, 2, 2, 4, 0),
			dshAssistantEvent("e3", now, 3, 3, 6, 0),
		)
		if err := os.WriteFile(path, larger, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, now.Add(time.Minute), now.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		service.Poll()
		if got := service.Snapshot().TodayTotalTokens; got != 12 {
			t.Fatalf("回退整文件解压后 today = %d, want 12", got)
		}
	})
}

// TestDSHDecodeRangeAndSessionID 覆盖 decodeDSHRange 的边界/错误与 session 标识提取。
func TestDSHDecodeRangeAndSessionID(t *testing.T) {
	service := newCoverageService(dshFixedNow())

	t.Run("空区间", func(t *testing.T) {
		if err := service.decodeDSHRange(&stubTranscriptFile{}, "x", 10, 10); err != nil {
			t.Fatalf("空区间应直接返回: %v", err)
		}
	})
	t.Run("Seek 失败", func(t *testing.T) {
		if err := service.decodeDSHRange(&stubTranscriptFile{seekErr: errors.New("seek")}, "x", 0, 10); err == nil {
			t.Fatal("Seek 失败应回传错误")
		}
	})
	t.Run("非 zstd 数据 Reset 失败", func(t *testing.T) {
		if err := service.decodeDSHRange(&stubTranscriptFile{data: []byte("plain text")}, "x", 0, 10); err == nil {
			t.Fatal("非 zstd 数据应回传错误")
		}
	})
	t.Run("解码器复用", func(t *testing.T) {
		first := service.dshDecoder()
		if second := service.dshDecoder(); first != second {
			t.Fatal("解码器应复用同一实例")
		}
	})
	t.Run("会话标识", func(t *testing.T) {
		cases := map[string]string{
			dshSessionPath(t.TempDir(), "uuid-1"): "uuid-1",
			"session.v4.jsonl.zstd":               "unknown",
		}
		for path, want := range cases {
			if got := dshSessionID(path); got != want {
				t.Errorf("dshSessionID(%q) = %q, want %q", path, got, want)
			}
		}
	})
}

// TestDSHIngestRejectionsAndFallbacks 覆盖 ingestDSH 的各类拒绝/兜底与归一化。
func TestDSHIngestRejectionsAndFallbacks(t *testing.T) {
	now := dshFixedNow()
	service := newCoverageService(now)
	path := dshSessionPath(t.TempDir(), "ingest")

	lines := []string{
		`not json`,
		`{"type":"step/start","time":1792486956000}`,
		`{"type":"assistant/message","time":1792486956000,"data":null}`,
		`{"type":"assistant/message","time":1792486956000,"data":{"message":{"role":"user"}}}`,
		`{"type":"assistant/message","time":1792486956000,"data":{"message":{"role":"assistant"}}}`,
		`{"type":"assistant/message","time":0,"data":{"message":{"role":"assistant"},"usage":{"inputTokens":1,"outputTokens":1,"totalTokens":2}}}`,
		`{"type":"assistant/message","time":1792486956000,"data":{"message":{"role":"assistant"},"usage":{"inputTokens":0,"outputTokens":0,"totalTokens":0}}}`,
	}
	for _, line := range lines {
		service.ingestDSH(path, []byte(line))
	}
	if len(service.records) != 0 {
		t.Fatalf("拒绝样例不应产生记录: %+v", service.records)
	}

	// 顶层 usage 兜底 + 空 id 用 seq 兜底。
	service.ingestDSH(path, []byte(`{"type":"assistant/message","seq":7,"time":1792486956000,`+
		`"data":{"message":{"role":"assistant"}},"usage":{"inputTokens":1,"outputTokens":2,"totalTokens":3}}`))
	if _, ok := service.records["dsh:ingest:7"]; !ok {
		t.Fatalf("顶层 usage/seq 兜底失败: %+v", service.records)
	}
	// 空 id 且 seq 为 0：用时间戳兜底；同时覆盖缺 total 的求和兜底。
	service.ingestDSH(path, []byte(`{"type":"assistant/message","time":1792486957000,`+
		`"data":{"message":{"role":"assistant"},"source":{"model":"m"},"usage":{"inputTokens":1,"outputTokens":2,"cacheReadTokens":3,"cacheWriteTokens":4}}}`))
	record, ok := service.records["dsh:ingest:1792486957000"]
	if !ok || record.usage.total != 10 || record.usage.cached != 3 || record.usage.cacheNew != 4 {
		t.Fatalf("时间戳兜底/求和归一化错误: %+v ok=%v", record, ok)
	}

	// dshUsage.normalized 的兜底分支与负数夹紧。
	normalized := dshUsage{InputTokens: -1, OutputTokens: 2}.normalized()
	if normalized.input != 0 || normalized.total != 2 {
		t.Fatalf("normalized 负数/缺失 total 处理错误: %+v", normalized)
	}
	if got := (dshUsage{InputTokens: -5, CacheReadTokens: -5, TotalTokens: -1}).normalized(); got.total != 0 {
		t.Fatalf("全负 usage total 应为 0: %+v", got)
	}
}

// TestDSHWhitespaceLinesSkipped 覆盖解压流中的空行跳过分支。
func TestDSHWhitespaceLinesSkipped(t *testing.T) {
	root := t.TempDir()
	now := dshFixedNow()
	path := dshSessionPath(root, "blank")
	frame := dshFrame(t, "", "   ", dshAssistantEvent("b1", now, 1, 1, 2, 0))
	dshWriteFile(t, path, frame)
	service := dshNewService(root, now)
	service.Poll()
	if got := service.Snapshot().TodayTotalTokens; got != 2 {
		t.Fatalf("空行应跳过且正常事件计入: %d", got)
	}
}

// TestDefaultConfigDeduplicatesSharedRoots 覆盖两个来源指向同一目录时的去重分支。
func TestDefaultConfigDeduplicatesSharedRoots(t *testing.T) {
	root := t.TempDir()
	t.Setenv("QODER_CONFIG_DIR", root)
	t.Setenv("QODERCN_CONFIG_DIR", root)
	config := DefaultConfig()
	sources := make(map[string]string, len(config.Roots))
	paths := make(map[string]string, len(config.Roots))
	for _, item := range config.Roots {
		sources[item.Source] = item.Path
		paths[item.Path] = item.Source
	}
	if sources["qoder"] != filepath.Join(root, "projects") {
		t.Fatalf("qoder 根路径错误: %+v", sources)
	}
	if _, ok := sources["qoder-cn"]; ok {
		t.Fatalf("同一路径的第二个来源应被去重: %+v", config.Roots)
	}
	if owner := paths[filepath.Join(root, "projects")]; owner != "qoder" {
		t.Fatalf("重复路径应保留首次注册来源，实际 %q", owner)
	}
}
