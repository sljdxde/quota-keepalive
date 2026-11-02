package schedule

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"qka/internal/scheduler"
)

type fakeScheduler struct {
	installed    bool
	installs     int
	removes      int
	installSpecs []scheduler.Spec
	removeSpecs  []scheduler.Spec
	failInstall  bool
	failRemove   bool
	failStatus   bool
	onInstall    func()
	onRemove     func()
}

func (f *fakeScheduler) Install(_ context.Context, spec scheduler.Spec) error {
	f.installs++
	f.installSpecs = append(f.installSpecs, spec)
	if f.onInstall != nil {
		f.onInstall()
	}
	if f.failInstall {
		return errors.New("install failed")
	}
	f.installed = true
	return nil
}

func (f *fakeScheduler) Remove(_ context.Context, spec scheduler.Spec) error {
	f.removes++
	f.removeSpecs = append(f.removeSpecs, spec)
	if f.onRemove != nil {
		f.onRemove()
	}
	if f.failRemove {
		return errors.New("remove failed")
	}
	f.installed = false
	return nil
}

func (f *fakeScheduler) Status(context.Context, scheduler.Spec) (scheduler.Status, error) {
	if f.failStatus {
		return scheduler.Status{}, errors.New("status failed")
	}
	return scheduler.Status{Installed: f.installed, PowerPolicy: "test"}, nil
}

func TestManagerEnableDisableKeepsSystemAndFileInStep(t *testing.T) {
	ctx := context.Background()
	fake := &fakeScheduler{}
	now := func() time.Time {
		return time.Date(2026, 10, 30, 8, 0, 0, 0, time.Local)
	}
	m := NewManager(t.TempDir(), "/Applications/QKA.app/Contents/MacOS/qka", fake, now)

	view, err := m.SetEnabled(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if !view.Enabled || !view.Installed || fake.installs != 1 {
		t.Fatalf("启用应安装原生任务并持久化: view=%+v fake=%+v", view, fake)
	}
	cfg, err := Load(m.ConfigDir)
	if err != nil || !cfg.Enabled {
		t.Fatalf("启用后 schedule.json 应为 enabled: %+v %v", cfg, err)
	}

	view, err = m.SetEnabled(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if view.Enabled || view.Installed || fake.removes != 1 {
		t.Fatalf("停用应移除原生任务并持久化: view=%+v fake=%+v", view, fake)
	}
}

func TestManagerInstallFailureDoesNotClaimEnabled(t *testing.T) {
	fake := &fakeScheduler{failInstall: true}
	m := NewManager(t.TempDir(), "/tmp/qka", fake, time.Now)
	if _, err := m.SetEnabled(context.Background(), true); err == nil {
		t.Fatal("安装失败必须返回错误")
	}
	cfg, err := Load(m.ConfigDir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Enabled {
		t.Fatal("安装失败不得把 schedule.json 写成 enabled")
	}
}

func TestManagerGetFailuresAndComputedView(t *testing.T) {
	t.Run("nil clock uses system clock", func(t *testing.T) {
		m := NewManager(t.TempDir(), "/tmp/qka", &fakeScheduler{}, nil)
		if m.Now == nil {
			t.Fatal("nil clock 应替换为默认时钟")
		}
	})
	t.Run("missing scheduler", func(t *testing.T) {
		m := NewManager(t.TempDir(), "/tmp/qka", nil, time.Now)
		if _, err := m.Get(context.Background()); err == nil {
			t.Fatal("缺少 scheduler 必须返回错误")
		}
	})
	t.Run("load failure", func(t *testing.T) {
		root := t.TempDir()
		file := filepath.Join(root, "file")
		if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		m := NewManager(filepath.Join(file, "child"), "/tmp/qka", &fakeScheduler{}, time.Now)
		if _, err := m.Get(context.Background()); err == nil {
			t.Fatal("配置读取失败必须向上传递")
		}
	})
	t.Run("status failure", func(t *testing.T) {
		fake := &fakeScheduler{failStatus: true}
		m := NewManager(t.TempDir(), "/tmp/qka", fake, time.Now)
		if _, err := m.Get(context.Background()); err == nil {
			t.Fatal("原生调度状态读取失败必须向上传递")
		}
	})
	t.Run("view fields", func(t *testing.T) {
		now := time.Date(2026, 10, 30, 8, 0, 0, 0, time.Local)
		fake := &fakeScheduler{installed: true}
		m := NewManager(t.TempDir(), "/tmp/qka", fake, func() time.Time { return now })
		cfg := Default()
		cfg.IntervalMinutes = 45
		cfg.Action = ActionCheckin
		cfg.LastRun = &LastRun{At: "2026-10-30T00:00:00Z", Result: "success"}
		if err := Save(m.ConfigDir, cfg); err != nil {
			t.Fatal(err)
		}
		view, err := m.Get(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if view.IntervalMinutes != 45 || view.Action != ActionCheckin || view.Timezone != TimezoneLocal ||
			!view.Installed || view.NextRun != "" || view.LastRun == nil || view.Now != now {
			t.Fatalf("view 字段不完整: %+v", view)
		}
	})
}

func TestManagerSetEnabledFailurePaths(t *testing.T) {
	t.Run("missing scheduler", func(t *testing.T) {
		m := NewManager(t.TempDir(), "/tmp/qka", nil, time.Now)
		if _, err := m.SetEnabled(context.Background(), true); err == nil {
			t.Fatal("缺少 scheduler 必须返回错误")
		}
	})
	t.Run("missing executable", func(t *testing.T) {
		m := NewManager(t.TempDir(), "", &fakeScheduler{}, time.Now)
		if _, err := m.SetEnabled(context.Background(), true); err == nil {
			t.Fatal("启用时缺少可执行文件路径必须失败")
		}
	})
	t.Run("load failure", func(t *testing.T) {
		root := t.TempDir()
		file := filepath.Join(root, "file")
		if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		m := NewManager(filepath.Join(file, "child"), "/tmp/qka", &fakeScheduler{}, time.Now)
		if _, err := m.SetEnabled(context.Background(), true); err == nil {
			t.Fatal("配置读取失败必须向上传递")
		}
	})
	t.Run("remove failure preserves enabled state", func(t *testing.T) {
		fake := &fakeScheduler{failRemove: true}
		m := NewManager(t.TempDir(), "/tmp/qka", fake, time.Now)
		cfg := Default()
		cfg.Enabled = true
		if err := Save(m.ConfigDir, cfg); err != nil {
			t.Fatal(err)
		}
		if _, err := m.SetEnabled(context.Background(), false); err == nil {
			t.Fatal("系统任务移除失败必须返回错误")
		}
		got, err := Load(m.ConfigDir)
		if err != nil || !got.Enabled {
			t.Fatalf("移除失败不能改写配置: %+v %v", got, err)
		}
	})
	t.Run("save failure rolls back install", func(t *testing.T) {
		root := t.TempDir()
		dir := filepath.Join(root, "config")
		fake := &fakeScheduler{}
		fake.onInstall = func() {
			if err := os.RemoveAll(dir); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(dir, []byte("block"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		m := NewManager(dir, "/tmp/qka", fake, time.Now)
		if _, err := m.SetEnabled(context.Background(), true); err == nil {
			t.Fatal("配置持久化失败必须返回错误")
		}
		if fake.removes != 1 {
			t.Fatalf("持久化失败后应回滚原生任务，Remove 次数=%d", fake.removes)
		}
	})
	t.Run("disable save failure returns error", func(t *testing.T) {
		root := t.TempDir()
		dir := filepath.Join(root, "config")
		fake := &fakeScheduler{}
		fake.onRemove = func() {
			if err := os.RemoveAll(dir); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(dir, []byte("block"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		m := NewManager(dir, "/tmp/qka", fake, time.Now)
		if _, err := m.SetEnabled(context.Background(), false); err == nil {
			t.Fatal("停用后配置持久化失败必须返回错误")
		}
	})
}

func TestManagerSetConfig(t *testing.T) {
	t.Run("missing scheduler", func(t *testing.T) {
		m := NewManager(t.TempDir(), "/tmp/qka", nil, time.Now)
		if _, err := m.SetConfig(context.Background(), 30, ActionAll); err == nil {
			t.Fatal("missing scheduler must fail")
		}
	})
	t.Run("lock failure", func(t *testing.T) {
		root := t.TempDir()
		file := filepath.Join(root, "file")
		if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		m := NewManager(filepath.Join(file, "child"), "/tmp/qka", &fakeScheduler{}, time.Now)
		if _, err := m.SetConfig(context.Background(), 30, ActionAll); err == nil {
			t.Fatal("lock failure must surface")
		}
	})
	t.Run("load failure", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, FileName), []byte("{"), 0o600); err != nil {
			t.Fatal(err)
		}
		m := NewManager(dir, "/tmp/qka", &fakeScheduler{}, time.Now)
		if _, err := m.SetConfig(context.Background(), 30, ActionAll); err == nil {
			t.Fatal("配置读取失败必须向上传递")
		}
	})
	t.Run("invalid config", func(t *testing.T) {
		m := NewManager(t.TempDir(), "/tmp/qka", &fakeScheduler{}, time.Now)
		if _, err := m.SetConfig(context.Background(), 1, ActionAll); !errors.Is(err, ErrInvalidInterval) {
			t.Fatalf("非法间隔应被拒绝，实际 %v", err)
		}
		if _, err := m.SetConfig(context.Background(), DefaultIntervalMinutes, "invalid"); !errors.Is(err, ErrInvalidAction) {
			t.Fatalf("非法动作应被拒绝，实际 %v", err)
		}
	})
	t.Run("disabled saves without installing", func(t *testing.T) {
		fake := &fakeScheduler{}
		m := NewManager(t.TempDir(), "/tmp/qka", fake, time.Now)
		view, err := m.SetConfig(context.Background(), 60, ActionKeepalive)
		if err != nil {
			t.Fatal(err)
		}
		if view.IntervalMinutes != 60 || view.Action != ActionCheckin || fake.installs != 0 {
			t.Fatalf("停用配置应保存但不安装: view=%+v fake=%+v", view, fake)
		}
	})
	t.Run("enabled requires executable", func(t *testing.T) {
		fake := &fakeScheduler{}
		m := NewManager(t.TempDir(), "", fake, time.Now)
		cfg := Default()
		cfg.Enabled = true
		if err := Save(m.ConfigDir, cfg); err != nil {
			t.Fatal(err)
		}
		if _, err := m.SetConfig(context.Background(), 60, ActionAll); err == nil {
			t.Fatal("启用状态缺少 executable 必须报错")
		}
	})
	t.Run("enabled reinstalls", func(t *testing.T) {
		fake := &fakeScheduler{}
		m := NewManager(t.TempDir(), "/tmp/qka", fake, time.Now)
		cfg := Default()
		cfg.Enabled = true
		if err := Save(m.ConfigDir, cfg); err != nil {
			t.Fatal(err)
		}
		view, err := m.SetConfig(context.Background(), 60, ActionCheckin)
		if err != nil {
			t.Fatal(err)
		}
		if fake.installs != 1 || view.IntervalMinutes != 60 || view.Action != ActionCheckin {
			t.Fatalf("启用状态修改应重装任务并保存: view=%+v fake=%+v", view, fake)
		}
	})
	t.Run("install failure keeps previous config", func(t *testing.T) {
		fake := &fakeScheduler{failInstall: true}
		m := NewManager(t.TempDir(), "/tmp/qka", fake, time.Now)
		cfg := Default()
		cfg.Enabled = true
		if err := Save(m.ConfigDir, cfg); err != nil {
			t.Fatal(err)
		}
		if _, err := m.SetConfig(context.Background(), 60, ActionCheckin); err == nil {
			t.Fatal("安装失败必须返回错误")
		}
		got, err := Load(m.ConfigDir)
		if err != nil || got.IntervalMinutes != DefaultIntervalMinutes || got.Action != ActionCheckin {
			t.Fatalf("安装失败不应改变配置: %+v %v", got, err)
		}
	})
	t.Run("save failure restores previous installed task", func(t *testing.T) {
		root := t.TempDir()
		dir := filepath.Join(root, "config")
		fake := &fakeScheduler{}
		fake.onInstall = func() {
			if err := os.RemoveAll(dir); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(dir, []byte("block"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		m := NewManager(dir, "/tmp/qka", fake, time.Now)
		cfg := Default()
		cfg.Enabled = true
		// Build the initial enabled config before the fake scheduler sabotages the directory.
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := Save(dir, cfg); err != nil {
			t.Fatal(err)
		}
		if _, err := m.SetConfig(context.Background(), 60, ActionCheckin); err == nil {
			t.Fatal("配置持久化失败必须返回错误")
		}
		if fake.installs != 2 || len(fake.installSpecs) != 2 {
			t.Fatalf("持久化失败应恢复旧任务，Install 次数=%d specs=%+v", fake.installs, fake.installSpecs)
		}
		if got := fake.installSpecs[len(fake.installSpecs)-1]; got.IntervalMinutes != cfg.IntervalMinutes {
			t.Fatalf("持久化失败后应恢复旧间隔，got=%+v want=%+v", got, cfg)
		}
	})
	t.Run("disabled save failure returns error", func(t *testing.T) {
		dir := t.TempDir()
		fake := &fakeScheduler{}
		m := NewManager(dir, "/tmp/qka", fake, time.Now)
		if err := Save(dir, Default()); err != nil {
			t.Fatal(err)
		}
		saveErr := errors.New("save failed")
		old := saveConfig
		saveConfig = func(string, Config) error { return saveErr }
		t.Cleanup(func() { saveConfig = old })
		if _, err := m.SetConfig(context.Background(), 60, ActionCheckin); err == nil {
			t.Fatal("disabled save failure must surface")
		}
	})
}

func TestManagerLoadFailureBranches(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewManager(dir, "/tmp/qka", &fakeScheduler{}, time.Now)
	if _, err := m.Get(context.Background()); err == nil {
		t.Fatal("Get load failure must surface")
	}
	if _, err := m.SetEnabled(context.Background(), true); err == nil {
		t.Fatal("SetEnabled load failure must surface")
	}
}
