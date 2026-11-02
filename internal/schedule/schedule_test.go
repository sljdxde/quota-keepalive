package schedule

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMissingFileReturnsDisabledDefault(t *testing.T) {
	cfg, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Enabled || cfg.IntervalMinutes != DefaultIntervalMinutes ||
		cfg.Action != ActionCheckin || cfg.Timezone != TimezoneLocal {
		t.Fatalf("默认调度应停用并采用 30 分钟/签到，实际 %+v", cfg)
	}
}

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := Default()
	cfg.Enabled = true
	cfg.LastRun = &LastRun{
		At: "2026-10-30T09:10:00+08:00", Result: "success",
		Attempted: 2, Succeeded: 2,
	}
	if err := Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Enabled || got.IntervalMinutes != 30 || got.Action != ActionCheckin || got.LastRun == nil ||
		got.LastRun.Succeeded != 2 {
		t.Fatalf("往返结果不符: %+v", got)
	}
	info, err := os.Stat(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("schedule.json 应为 0600，实际 %#o", info.Mode().Perm())
	}
}

func TestInvalidIntervalActionAndTimezoneRejected(t *testing.T) {
	cfg := Default()
	cfg.IntervalMinutes = 4
	if err := Save(t.TempDir(), cfg); !errors.Is(err, ErrInvalidInterval) {
		t.Fatalf("非法间隔应失败，实际 %v", err)
	}
	cfg = Default()
	cfg.Action = "unknown"
	if err := Save(t.TempDir(), cfg); !errors.Is(err, ErrInvalidAction) {
		t.Fatalf("非法执行动作应失败，实际 %v", err)
	}
	cfg = Default()
	cfg.Timezone = "UTC"
	if err := Save(t.TempDir(), cfg); !errors.Is(err, ErrInvalidTimezone) {
		t.Fatalf("第一版只支持本机时区，实际 %v", err)
	}
}

func TestNormalizeAndValidateLastRun(t *testing.T) {
	cfg := Config{}
	cfg.normalize()
	if cfg.IntervalMinutes != DefaultIntervalMinutes || cfg.Action != ActionCheckin || cfg.Timezone != TimezoneLocal {
		t.Fatalf("缺省字段未规范化: %+v", cfg)
	}

	cases := []struct {
		name string
		last *LastRun
	}{
		{name: "missing timestamp", last: &LastRun{Result: "success"}},
		{name: "invalid timestamp", last: &LastRun{At: "not-a-time", Result: "success"}},
		{name: "invalid result", last: &LastRun{At: "2026-10-30T00:00:00Z", Result: "unknown"}},
		{name: "negative attempted", last: &LastRun{At: "2026-10-30T00:00:00Z", Result: "success", Attempted: -1}},
		{name: "negative succeeded", last: &LastRun{At: "2026-10-30T00:00:00Z", Result: "success", Succeeded: -1}},
		{name: "negative attention", last: &LastRun{At: "2026-10-30T00:00:00Z", Result: "success", NeedsAttention: -1}},
		{name: "negative skipped", last: &LastRun{At: "2026-10-30T00:00:00Z", Result: "success", Skipped: -1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Default()
			cfg.LastRun = tc.last
			if err := cfg.validate(); err == nil {
				t.Fatal("无效 LastRun 必须拒绝")
			}
		})
	}
	for _, result := range []string{"success", "needs_attention", "failed", "disabled"} {
		t.Run("valid "+result, func(t *testing.T) {
			cfg := Default()
			cfg.LastRun = &LastRun{At: "2026-10-30T00:00:00Z", Result: result}
			if err := cfg.validate(); err != nil {
				t.Fatalf("合法结果 %q 被拒绝: %v", result, err)
			}
		})
	}
}

func TestLoadNormalizesLegacyKeepaliveActions(t *testing.T) {
	for _, action := range []string{"all", "keepalive"} {
		t.Run(action, func(t *testing.T) {
			dir := t.TempDir()
			data := []byte(`{"enabled":true,"interval_minutes":30,"action":"` + action + `","timezone":"local"}`)
			if err := os.WriteFile(filepath.Join(dir, FileName), data, 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Action != ActionCheckin {
				t.Fatalf("legacy action %q must normalize to checkin, got %q", action, cfg.Action)
			}
		})
	}
}

func TestLoadRejectsUnreadableAndMalformedFiles(t *testing.T) {
	t.Run("directory at file path", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, FileName), 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(dir); err == nil {
			t.Fatal("目录不能作为 schedule.json 读取")
		}
	})
	t.Run("malformed JSON", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, FileName), []byte("{"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(dir); err == nil {
			t.Fatal("格式错误的 JSON 必须拒绝")
		}
	})
	t.Run("invalid persisted config", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, FileName), []byte(`{"interval_minutes":1}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(dir); !errors.Is(err, ErrInvalidInterval) {
			t.Fatalf("应拒绝非法持久化配置，实际 %v", err)
		}
	})
}

func TestSaveAndRecordLastRunErrors(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Save(filepath.Join(file, "child"), Default()); err == nil {
		t.Fatal("配置目录的父路径是文件时 Save 必须失败")
	}
	t.Run("rename over directory fails", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, FileName)
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "keep"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := Save(dir, Default()); err == nil {
			t.Fatal("原子替换非空目录必须失败")
		}
	})

	if err := RecordLastRun(t.TempDir(), LastRun{At: "2026-10-30T00:00:00Z", Result: "success", Attempted: 2, Succeeded: 2}); err != nil {
		t.Fatal(err)
	}
	invalidDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(invalidDir, FileName), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RecordLastRun(invalidDir, LastRun{}); err == nil {
		t.Fatal("读取无效配置失败时 RecordLastRun 必须返回错误")
	}
	if err := RecordLastRun(t.TempDir(), LastRun{At: "invalid", Result: "success"}); err == nil {
		t.Fatal("持久化无效 LastRun 必须返回错误")
	}
	rootFile := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(rootFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RecordLastRun(filepath.Join(rootFile, "child"), LastRun{}); err == nil {
		t.Fatal("lock acquisition failure must surface")
	}
}

func TestNextRunAtUsesInterval(t *testing.T) {
	now := time.Date(2026, 10, 30, 10, 0, 0, 0, time.Local)
	next, err := NextRunAt(now, 45)
	if err != nil {
		t.Fatal(err)
	}
	if got := next.Format("2006-01-02 15:04"); got != "2026-10-30 10:45" {
		t.Fatalf("应返回间隔后的时间，实际 %s", got)
	}
	if _, err := NextRunAt(now, 1); !errors.Is(err, ErrInvalidInterval) {
		t.Fatalf("过短间隔应拒绝，实际 %v", err)
	}
}

func TestSavedStateDoesNotContainCredentials(t *testing.T) {
	dir := t.TempDir()
	cfg := Default()
	cfg.Enabled = true
	if err := Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ToLower(string(data))
	for _, forbidden := range []string{"token", "uid", "authorization", "cookie", "phone"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("schedule.json 不应包含敏感字段 %q: %s", forbidden, data)
		}
	}
}
