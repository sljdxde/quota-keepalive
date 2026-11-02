package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"qka/internal/app"
	"qka/internal/schedule"
)

type fakeTaskRunRunner struct {
	summary app.RunSummary
	err     error
	calls   int
	closed  bool
	action  string
}

func (f *fakeTaskRunRunner) RunAllActionSummary(_ context.Context, action string) (app.RunSummary, error) {
	f.calls++
	f.action = action
	return f.summary, f.err
}
func (f *fakeTaskRunRunner) Close() error { f.closed = true; return nil }

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

func TestTaskRunDisabledPerformsZeroWritesOrBuilds(t *testing.T) {
	dir := t.TempDir()
	builds, locks := 0, 0
	deps := taskRunDeps{
		LoadSchedule: schedule.Load,
		SaveLastRun:  schedule.RecordLastRun,
		AcquireLock: func(string) (io.Closer, error) {
			locks++
			return nopCloser{}, nil
		},
		Build: func(context.Context, string) (taskRunRunner, error) {
			builds++
			return &fakeTaskRunRunner{}, nil
		},
		Now: time.Now,
	}
	resp, err := runTaskRun(context.Background(), dir, deps)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != "disabled" || builds != 0 || locks != 0 {
		t.Fatalf("未启用时不得建 App 或加锁: status=%s builds=%d locks=%d",
			resp.Status, builds, locks)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("未启用时不得写文件，实际 %v", entries)
	}
}

func TestTaskRunEnabledUsesRunAllAndRecordsRedactedSummary(t *testing.T) {
	dir := t.TempDir()
	cfg := schedule.Default()
	cfg.Enabled = true
	if err := schedule.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	runner := &fakeTaskRunRunner{summary: app.RunSummary{
		Attempted: 2, Succeeded: 2,
	}}
	builds, locks := 0, 0
	var builtConfigDir string
	now := time.Date(2026, 10, 30, 9, 10, 0, 0, time.Local)
	deps := taskRunDeps{
		LoadSchedule: schedule.Load,
		SaveLastRun:  schedule.RecordLastRun,
		AcquireLock: func(string) (io.Closer, error) {
			locks++
			return nopCloser{}, nil
		},
		Build: func(_ context.Context, configDir string) (taskRunRunner, error) {
			builds++
			builtConfigDir = configDir
			return runner, nil
		},
		Now: func() time.Time { return now },
	}
	resp, err := runTaskRun(context.Background(), dir, deps)
	if err != nil {
		t.Fatal(err)
	}
	if resp.ExitCode != 0 || runner.calls != 1 || builds != 1 || locks != 1 ||
		!runner.closed {
		t.Fatalf("启用时应恰好执行一次并释放资源: resp=%+v runner=%+v builds=%d locks=%d",
			resp, runner, builds, locks)
	}
	if runner.action != schedule.ActionCheckin {
		t.Fatalf("task-run 应把动作传给执行链，实际 %q", runner.action)
	}
	if builtConfigDir != dir {
		t.Fatalf("task-run 必须把同一配置目录传给 App，实际 %q want %q",
			builtConfigDir, dir)
	}
	got, err := schedule.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastRun == nil || got.LastRun.Result != "success" ||
		got.LastRun.Attempted != 2 || got.LastRun.Succeeded != 2 {
		t.Fatalf("应写入脱敏执行摘要，实际 %+v", got.LastRun)
	}
	data, err := os.ReadFile(filepath.Join(dir, schedule.FileName))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"token", "uid", "authorization", "cookie"} {
		if containsFold(string(data), forbidden) {
			t.Errorf("调度结果不应包含敏感词 %q: %s", forbidden, data)
		}
	}
}

func TestTaskRunHandlerHonorsConfigDirFlagWithoutStartingGUI(t *testing.T) {
	dir := t.TempDir()
	var stdout strings.Builder
	env := &Env{
		Stdout: &stdout,
		Stderr: &stdout,
		Getenv: func(key string) string {
			if key == "HOME" {
				return "/Users/test"
			}
			return ""
		},
	}
	resp, err := taskRunHandler(context.Background(), env,
		[]string{"--config-dir", dir, "--scheduled"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != "disabled" {
		t.Fatalf("未启用 schedule.json 时应直接返回 disabled，实际 %+v", resp)
	}
	if !strings.Contains(stdout.String(), "自动执行未启用") {
		t.Fatalf("非 JSON 输出应给出明确结果，实际 %q", stdout.String())
	}
}

func TestTaskRunBusinessFailureReturnsNonZeroWithoutAbortingOthers(t *testing.T) {
	dir := t.TempDir()
	cfg := schedule.Default()
	cfg.Enabled = true
	if err := schedule.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	runner := &fakeTaskRunRunner{
		summary: app.RunSummary{Attempted: 2, Succeeded: 1, NeedsAttention: 1},
		err:     errors.New("one account failed"),
	}
	resp, err := runTaskRun(context.Background(), dir, taskRunDeps{
		LoadSchedule: schedule.Load,
		SaveLastRun:  schedule.RecordLastRun,
		AcquireLock:  func(string) (io.Closer, error) { return nopCloser{}, nil },
		Build: func(context.Context, string) (taskRunRunner, error) {
			return runner, nil
		},
		Now: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.ExitCode != 1 || resp.Status != "needs_attention" {
		t.Fatalf("有账号失败时应返回非 0 摘要，实际 %+v", resp)
	}
	if runner.calls != 1 {
		t.Fatalf("失败摘要仍应来自完整批次，实际调用 %d", runner.calls)
	}
}

func TestTaskRunBuildFailureRecordsRedactedFailure(t *testing.T) {
	dir := t.TempDir()
	cfg := schedule.Default()
	cfg.Enabled = true
	if err := schedule.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 30, 9, 10, 0, 0, time.Local)
	resp, err := runTaskRun(context.Background(), dir, taskRunDeps{
		LoadSchedule: schedule.Load,
		SaveLastRun:  schedule.RecordLastRun,
		AcquireLock:  func(string) (io.Closer, error) { return nopCloser{}, nil },
		Build: func(context.Context, string) (taskRunRunner, error) {
			return nil, errors.New("adapter failed with secret-token")
		},
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.ExitCode != 1 || resp.Status != "failed" {
		t.Fatalf("启动失败应返回非 0 脱敏摘要，实际 %+v", resp)
	}
	got, err := schedule.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastRun == nil || got.LastRun.Result != "failed" ||
		strings.Contains(strings.ToLower(got.LastRun.Message), "secret-token") {
		t.Fatalf("失败摘要不得回显底层错误，实际 %+v", got.LastRun)
	}
}

func containsFold(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexFold(s, sub) >= 0)
}

func indexFold(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if equalFoldASCII(s[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}

func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		ac, bc := a[i], b[i]
		if ac >= 'A' && ac <= 'Z' {
			ac += 'a' - 'A'
		}
		if bc >= 'A' && bc <= 'Z' {
			bc += 'a' - 'A'
		}
		if ac != bc {
			return false
		}
	}
	return true
}
