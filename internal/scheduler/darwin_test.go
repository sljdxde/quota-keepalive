//go:build darwin

package scheduler

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDarwinInstallRemoveWithFakeLaunchctl(t *testing.T) {
	home := t.TempDir()
	var calls []string
	loaded := false
	s := newDarwinScheduler(home, "501", func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+args[0])
		switch args[0] {
		case "print":
			if loaded {
				return []byte("loaded"), nil
			}
			return []byte("Could not find service"), errors.New("not loaded")
		case "bootout":
			loaded = false
			return nil, nil
		case "bootstrap":
			loaded = true
			return nil, nil
		default:
			t.Fatalf("unexpected launchctl action %q", args[0])
			return nil, nil
		}
	})
	spec := Spec{
		Executable:      "/Applications/QKA.app/Contents/MacOS/qka",
		ConfigDir:       "/tmp/qka-config",
		IntervalMinutes: 30,
		Enabled:         true,
	}
	if err := s.Install(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "Library", "LaunchAgents",
		LaunchdLabel(spec.ConfigDir)+".plist")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Install 应写入 plist: %v", err)
	}
	status, err := s.Status(context.Background(), spec)
	if err != nil || !status.Installed {
		t.Fatalf("安装后状态应为 installed: %+v err=%v", status, err)
	}
	if !containsCall(calls, "launchctl bootstrap") {
		t.Fatalf("安装应经受控参数加载 LaunchAgent，实际 %v", calls)
	}
	if err := s.Remove(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Remove 应删除 plist，实际 %v", err)
	}
	if err := s.Remove(context.Background(), spec); err != nil {
		t.Fatalf("重复 Remove 应幂等，实际 %v", err)
	}
}

func TestDarwinDifferentConfigDirsDoNotOverwrite(t *testing.T) {
	home := t.TempDir()
	s := newDarwinScheduler(home, "501", func(context.Context, string, ...string) ([]byte, error) {
		return nil, nil
	})
	ctx := context.Background()
	a := Spec{Executable: "/tmp/qka", ConfigDir: "/tmp/a", IntervalMinutes: 30, Enabled: true}
	b := Spec{Executable: "/tmp/qka", ConfigDir: "/tmp/b", IntervalMinutes: 30, Enabled: true}
	if err := s.Install(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := s.Install(ctx, b); err != nil {
		t.Fatal(err)
	}
	if a := s.plistPath(a); a == s.plistPath(b) {
		t.Fatal("不同配置目录不得共享同一 plist")
	}
}

func TestDarwinPlistIncludesConfigDirAndBundleIdentity(t *testing.T) {
	home := t.TempDir()
	app := filepath.Join(home, "QKA.app", "Contents")
	exe := filepath.Join(app, "MacOS", "qka")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "Info.plist"), []byte(
		`<plist><dict><key>CFBundleIdentifier</key><string>com.qka.agent</string></dict></plist>`),
		0o644); err != nil {
		t.Fatal(err)
	}
	data, err := LaunchdPlist(Spec{
		Executable: exe, ConfigDir: "/tmp/QKA config & data", IntervalMinutes: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"/tmp/QKA config &amp; data",
		"<key>AssociatedBundleIdentifiers</key>",
		"<string>com.qka.agent</string>",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("plist 缺少 %q:\n%s", want, text)
		}
	}
}

func TestDarwinInstallValidationAndFilesystemFailures(t *testing.T) {
	ctx := context.Background()
	t.Run("invalid executable", func(t *testing.T) {
		s := newDarwinScheduler(t.TempDir(), "501", func(context.Context, string, ...string) ([]byte, error) {
			t.Fatal("验证失败时不应运行 launchctl")
			return nil, nil
		})
		if err := s.Install(ctx, Spec{Executable: " \t", ConfigDir: "/tmp/q", IntervalMinutes: 30}); err == nil {
			t.Fatal("空 executable 必须拒绝")
		}
	})
	t.Run("invalid interval", func(t *testing.T) {
		s := newDarwinScheduler(t.TempDir(), "501", func(context.Context, string, ...string) ([]byte, error) {
			t.Fatal("验证失败时不应运行 launchctl")
			return nil, nil
		})
		if err := s.Install(ctx, Spec{Executable: "/tmp/qka", ConfigDir: "/tmp/q", IntervalMinutes: 1}); err == nil {
			t.Fatal("非法 interval 必须拒绝")
		}
	})
	t.Run("launch agents parent is a file", func(t *testing.T) {
		home := t.TempDir()
		if err := os.WriteFile(filepath.Join(home, "Library"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		s := newDarwinScheduler(home, "501", func(context.Context, string, ...string) ([]byte, error) {
			t.Fatal("filesystem failure must happen before launchctl")
			return nil, nil
		})
		if err := s.Install(ctx, Spec{Executable: "/tmp/qka", ConfigDir: "/tmp/q", IntervalMinutes: 30}); err == nil {
			t.Fatal("无法创建 LaunchAgents 时必须失败")
		}
	})
	t.Run("plist target is a non-empty directory", func(t *testing.T) {
		home := t.TempDir()
		spec := Spec{Executable: "/tmp/qka", ConfigDir: "/tmp/q", IntervalMinutes: 30}
		s := newDarwinScheduler(home, "501", func(context.Context, string, ...string) ([]byte, error) {
			t.Fatal("plist 写入失败时不应运行 launchctl")
			return nil, nil
		})
		path := s.plistPath(spec)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "keep"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := s.Install(ctx, spec); err == nil {
			t.Fatal("原子写入不能覆盖非空目录")
		}
	})
}

func TestDarwinInstallBootstrapOutcomes(t *testing.T) {
	spec := Spec{Executable: "/tmp/qka", ConfigDir: "/tmp/q", IntervalMinutes: 30}
	for _, tc := range []struct {
		name string
		out  []byte
		err  error
		want bool
	}{
		{name: "already loaded", out: []byte("Service already loaded"), err: errors.New("bootstrap returned error"), want: true},
		{name: "already bootstrapped", out: []byte("ALREADY BOOTSTRAPPED"), err: errors.New("bootstrap returned error"), want: true},
		{name: "generic failure", out: []byte("permission denied"), err: errors.New("bootstrap returned error"), want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			printCalls := 0
			s := newDarwinScheduler(home, "501", func(_ context.Context, _ string, args ...string) ([]byte, error) {
				switch args[0] {
				case "print":
					printCalls++
					if printCalls == 1 || !tc.want {
						return []byte("Could not find service"), errors.New("not loaded")
					}
					return []byte("loaded"), nil
				case "bootstrap":
					return tc.out, tc.err
				default:
					t.Fatalf("unexpected launchctl action %q", args[0])
					return nil, nil
				}
			})
			err := s.Install(context.Background(), spec)
			if tc.want && err != nil {
				t.Fatalf("already loaded 应视为成功: %v", err)
			}
			if !tc.want && err == nil {
				t.Fatal("bootstrap 错误必须返回")
			}
			path := s.plistPath(spec)
			_, statErr := os.Stat(path)
			if tc.want && statErr != nil {
				t.Fatalf("成功或已加载时应保留 plist: %v", statErr)
			}
			if !tc.want && !os.IsNotExist(statErr) {
				t.Fatalf("bootstrap 失败后应清理 plist: %v", statErr)
			}
		})
	}
}

func TestAlreadyLoadedAndDarwinStatusRemoveErrorPaths(t *testing.T) {
	for _, output := range []string{"already loaded", "already bootstrapped", "service already loaded"} {
		if !alreadyLoaded([]byte(output)) {
			t.Errorf("应识别 launchctl 输出 %q", output)
		}
	}
	if alreadyLoaded([]byte("load failed")) {
		t.Fatal("普通错误不能被视为已加载")
	}

	spec := Spec{Executable: "/tmp/qka", ConfigDir: "/tmp/q", IntervalMinutes: 30}
	home := t.TempDir()
	s := newDarwinScheduler(home, "501", func(context.Context, string, ...string) ([]byte, error) {
		return []byte("Could not find service"), errors.New("not loaded")
	})
	if status, err := s.Status(context.Background(), spec); err != nil || status.Installed {
		t.Fatalf("不存在的任务应返回未安装: %+v %v", status, err)
	}
	path := s.plistPath(spec)
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "keep"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if status, err := s.Status(context.Background(), spec); err != nil || status.Installed {
		t.Fatalf("plist 存在但 launchd 未加载时应报告未安装: %+v %v", status, err)
	}
	if err := s.Remove(context.Background(), spec); err == nil {
		t.Fatal("无法删除非空 plist 路径时应返回错误")
	}
	brokenHome := t.TempDir()
	if err := os.WriteFile(filepath.Join(brokenHome, "Library"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	broken := newDarwinScheduler(brokenHome, "501", func(context.Context, string, ...string) ([]byte, error) { return nil, nil })
	if _, err := broken.Status(context.Background(), spec); err == nil {
		t.Fatal("Status 的非 NotExist 文件系统错误必须返回")
	}
}

func containsCall(calls []string, want string) bool {
	for _, call := range calls {
		if call == want {
			return true
		}
	}
	return false
}

func TestAssociatedBundleIdentifiersMissingAndMalformed(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "QKA.app", "Contents", "MacOS", "qka")
	if got := associatedBundleIdentifiers(exe); got != "" {
		t.Fatalf("Info.plist 缺失时不应添加关联 bundle: %q", got)
	}
	info := filepath.Join(dir, "QKA.app", "Contents", "Info.plist")
	if err := os.MkdirAll(filepath.Dir(info), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(info, []byte("not a plist"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := associatedBundleIdentifiers(exe); got != "" {
		t.Fatalf("bundle id 缺失时不应添加关联项: %q", got)
	}
}
