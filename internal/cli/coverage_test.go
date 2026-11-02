package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"qka/internal/app"
	"qka/internal/schedule"
)

func coverageEnv(t *testing.T) (*Env, string, string) {
	t.Helper()
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	adapters := filepath.Join(root, "adapters")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(adapters, 0o700); err != nil {
		t.Fatal(err)
	}
	return &Env{Getenv: func(key string) string {
		switch key {
		case "QKA_CONFIG_DIR":
			return configDir
		case "QKA_ADAPTERS_DIR":
			return adapters
		default:
			return ""
		}
	}}, configDir, adapters
}

func TestDefaultEnvAndOutputAccessors(t *testing.T) {
	env := DefaultEnv()
	if env.Args == nil || env.Stdout == nil || env.Stderr == nil || env.Getenv == nil {
		t.Fatalf("DefaultEnv 未接全进程依赖: %+v", env)
	}
	if got := (ErrOutputOverflow{Limit: 9}).Error(); !strings.Contains(got, "9") {
		t.Fatalf("overflow 文案缺少上限: %q", got)
	}
	buf := NewBoundedBuffer(5)
	if _, err := buf.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 3 || buf.String() != "abc" {
		t.Fatalf("bounded buffer accessors = %d/%q", buf.Len(), buf.String())
	}
}

func TestCommonFlagsBuildAndListenAddress(t *testing.T) {
	if _, err := parseCommonFlags("test", []string{"--unknown"}); err == nil {
		t.Fatal("未知 flag 应失败")
	}
	flags, err := parseCommonFlags("test", []string{
		"--json", "--addr", "127.0.0.1:7", "--port", "8",
		"--adapters-dir", "/tmp/adapters", "--bun", "/tmp/bun",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !flags.jsonOut || flags.listenAddr() != "127.0.0.1:7" ||
		flags.adapters != "/tmp/adapters" || flags.bun != "/tmp/bun" {
		t.Fatalf("flags 解析不完整: %+v", flags)
	}
	if (&commonFlags{}).listenAddr() != "127.0.0.1:43189" {
		t.Fatal("默认监听地址错误")
	}
	if (&commonFlags{port: 8123}).listenAddr() != "127.0.0.1:8123" {
		t.Fatal("端口监听地址错误")
	}

	env, configDir, adapters := coverageEnv(t)
	built, err := (&commonFlags{}).buildAppAt(context.Background(), env, configDir)
	if err != nil {
		t.Fatal(err)
	}
	if built.ConfigDir() != configDir || built.AdaptersRoot() == "" {
		t.Fatalf("buildAppAt 装配目录错误: %s/%s", built.ConfigDir(), built.AdaptersRoot())
	}
	if err := built.Close(); err != nil {
		t.Fatal(err)
	}
	env.Getenv = func(key string) string {
		switch key {
		case "QKA_CONFIG_DIR":
			return configDir
		case "QKA_ADAPTERS_DIR":
			return adapters
		default:
			return ""
		}
	}
	built, err = (&commonFlags{}).buildApp(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	_ = built.Close()

	env.Getenv = func(string) string { return "" }
	flags.adapters = filepath.Join(t.TempDir(), "missing")
	if _, err := flags.buildAppAt(context.Background(), env, configDir); err == nil {
		t.Fatal("适配器目录缺失时 Build 应失败")
	}
}

func TestRuntimeResolutionBranches(t *testing.T) {
	oldExecutable, oldWorkingDir, oldLookPath := executablePath, workingDir, lookPath
	t.Cleanup(func() {
		executablePath, workingDir, lookPath = oldExecutable, oldWorkingDir, oldLookPath
	})

	env := &Env{Getenv: func(string) string { return "" }}
	if got := resolveAdaptersRoot(env, "override"); got != "override" {
		t.Fatalf("adapters override = %q", got)
	}
	env.Getenv = func(key string) string {
		if key == "QKA_ADAPTERS_DIR" {
			return "from-env"
		}
		return ""
	}
	if got := resolveAdaptersRoot(env, ""); got != "from-env" {
		t.Fatalf("adapters env = %q", got)
	}
	env.Getenv = func(string) string { return "" }

	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(filepath.Join(bin, "adapters"), 0o700); err != nil {
		t.Fatal(err)
	}
	executablePath = func() (string, error) { return filepath.Join(bin, "qka"), nil }
	workingDir = func() (string, error) { return t.TempDir(), nil }
	if got := resolveAdaptersRoot(env, ""); got != filepath.Join(bin, "adapters") {
		t.Fatalf("executable adapters = %q", got)
	}
	executablePath = func() (string, error) { return "", errors.New("no executable") }
	cwdWithAdapters := t.TempDir()
	if err := os.Mkdir(filepath.Join(cwdWithAdapters, "adapters"), 0o700); err != nil {
		t.Fatal(err)
	}
	workingDir = func() (string, error) { return cwdWithAdapters, nil }
	if got := resolveAdaptersRoot(env, ""); got != filepath.Join(cwdWithAdapters, "adapters") {
		t.Fatalf("cwd adapters = %q", got)
	}
	workingDir = func() (string, error) { return "", errors.New("no cwd") }
	if got := resolveAdaptersRoot(env, ""); got != "adapters" {
		t.Fatalf("fallback adapters = %q", got)
	}

	if got := resolveBun(env, "override"); got != "override" {
		t.Fatalf("bun override = %q", got)
	}
	env.Getenv = func(key string) string {
		if key == "QKA_BUN" {
			return "from-env"
		}
		return ""
	}
	if got := resolveBun(env, ""); got != "from-env" {
		t.Fatalf("bun env = %q", got)
	}
	env.Getenv = func(string) string { return "" }

	bunDir := t.TempDir()
	bunPath := filepath.Join(bunDir, "bun.exe")
	if err := os.WriteFile(bunPath, []byte("x"), 0o700); err != nil {
		t.Fatal(err)
	}
	executablePath = func() (string, error) { return filepath.Join(bunDir, "qka.exe"), nil }
	if got := resolveBun(env, ""); got != bunPath {
		t.Fatalf("sibling bun = %q", got)
	}
	executablePath = func() (string, error) { return "", errors.New("no executable") }
	lookPath = func(string) (string, error) { return "/path/to/bun", nil }
	if got := resolveBun(env, ""); got != "/path/to/bun" {
		t.Fatalf("PATH bun = %q", got)
	}
	lookPath = func(string) (string, error) { return "", errors.New("not found") }
	if got := resolveBun(env, ""); got != "bun" {
		t.Fatalf("fallback bun = %q", got)
	}

	if got := resolveProviderPluginsRoot(env, "override"); got != "override" {
		t.Fatalf("provider override = %q", got)
	}
	env.Getenv = func(key string) string {
		if key == "QKA_PROVIDER_PLUGINS_DIR" {
			return "from-env"
		}
		return ""
	}
	if got := resolveProviderPluginsRoot(env, ""); got != "from-env" {
		t.Fatalf("provider env = %q", got)
	}
	env.Getenv = func(string) string { return "" }

	execRoot := t.TempDir()
	execDir := filepath.Join(execRoot, "bin")
	execPath := filepath.Join(execDir, "provider-plugins")
	if err := os.MkdirAll(execPath, 0o700); err != nil {
		t.Fatal(err)
	}
	executablePath = func() (string, error) { return filepath.Join(execDir, "qka"), nil }
	if got := resolveProviderPluginsRoot(env, ""); got != execPath {
		t.Fatalf("executable provider plugins = %q", got)
	}

	resourcesRoot := t.TempDir()
	resourcesBin := filepath.Join(resourcesRoot, "Contents", "MacOS")
	resourcesPath := filepath.Join(resourcesRoot, "Contents", "Resources", "provider-plugins")
	if err := os.MkdirAll(resourcesPath, 0o700); err != nil {
		t.Fatal(err)
	}
	executablePath = func() (string, error) { return filepath.Join(resourcesBin, "qka"), nil }
	if got := resolveProviderPluginsRoot(env, ""); got != filepath.Clean(resourcesPath) {
		t.Fatalf("resources provider plugins = %q", got)
	}

	executablePath = func() (string, error) { return "", errors.New("no executable") }
	cwdRoot := t.TempDir()
	cwdPath := filepath.Join(cwdRoot, "provider-plugins")
	if err := os.Mkdir(cwdPath, 0o700); err != nil {
		t.Fatal(err)
	}
	workingDir = func() (string, error) { return cwdRoot, nil }
	if got := resolveProviderPluginsRoot(env, ""); got != cwdPath {
		t.Fatalf("cwd provider plugins = %q", got)
	}
	workingDir = func() (string, error) { return "", errors.New("no cwd") }
	if got := resolveProviderPluginsRoot(env, ""); got != "" {
		t.Fatalf("provider plugins fallback = %q", got)
	}
	if isDir(filepath.Join(root, "missing")) {
		t.Fatal("missing path 不应是目录")
	}
}

func TestRunDispatchAndBundleDetection(t *testing.T) {
	oldExecutable, oldRunTray, oldTrayMode := executablePath, runTrayGUI, trayModeHandler
	oldFlavor := BuildFlavor
	t.Cleanup(func() {
		executablePath, runTrayGUI, trayModeHandler = oldExecutable, oldRunTray, oldTrayMode
		BuildFlavor = oldFlavor
	})

	executablePath = func() (string, error) { return "", errors.New("unavailable") }
	if launchedFromBundle() {
		t.Fatal("可执行路径错误时不应判定为 bundle")
	}
	executablePath = func() (string, error) {
		return "/Applications/QKA.app/Contents/MacOS/qka", nil
	}
	if !launchedFromBundle() {
		t.Fatal("bundle 路径应被识别")
	}

	BuildFlavor = "gui"
	env, _, _ := coverageEnv(t)
	var stderr strings.Builder
	env.Stdout = io.Discard
	env.Stderr = &stderr
	runTrayGUI = func(*app.App) error { return nil }
	trayModeHandler = func(context.Context, *Env, []string) (*Response, error) {
		return nil, nil
	}
	if got := Run(context.Background(), env); got != 0 {
		t.Fatalf("GUI 无参数启动退出码 = %d, stderr=%q", got, stderr.String())
	}
	runTrayGUI = func(*app.App) error { return errors.New("controlled tray failure") }
	trayModeHandler = func(context.Context, *Env, []string) (*Response, error) {
		return nil, errors.New("controlled tray failure")
	}
	stderr.Reset()
	if got := Run(context.Background(), env); got != 2 || !strings.Contains(stderr.String(), "controlled") {
		t.Fatalf("GUI 启动失败应回传错误: code=%d stderr=%q", got, stderr.String())
	}

	BuildFlavor = "cli"
	executablePath = func() (string, error) { return "/tmp/qka", nil }
	var stdout strings.Builder
	env.Stdout = &stdout
	if got := Run(context.Background(), &Env{Stdout: &stdout, Stderr: &stderr}); got != 0 ||
		!strings.Contains(stdout.String(), "用法") {
		t.Fatalf("CLI 无参数应显示用法: code=%d output=%q", got, stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if got := Run(context.Background(), &Env{Args: []string{"missing"}, Stdout: &stdout, Stderr: &stderr}); got != 2 ||
		!strings.Contains(stderr.String(), "未知形态") {
		t.Fatalf("未知命令普通输出错误: code=%d stderr=%q", got, stderr.String())
	}
	if got := Run(context.Background(), &Env{
		Args: []string{"missing", "--json"}, Stdout: &stdout, Stderr: &stderr,
	}); got != 2 {
		t.Fatalf("未知命令 JSON 退出码 = %d", got)
	}

	stdout.Reset()
	if got := Run(context.Background(), &Env{
		Args: []string{"panel"}, Stdout: &stdout, Stderr: io.Discard,
	}); got != 0 || !strings.Contains(stdout.String(), "unimplemented") && stdout.Len() != 0 {
		t.Fatalf("未实现形态普通模式退出码 = %d", got)
	}
	if got := Run(context.Background(), &Env{
		Args: []string{"panel", "--json"}, Stdout: errorWriter{}, Stderr: io.Discard,
	}); got != 1 {
		t.Fatalf("JSON 写入失败退出码 = %d", got)
	}
	if got := Run(context.Background(), &Env{
		Args: []string{"serve", "--bad"}, Stdout: io.Discard, Stderr: io.Discard,
	}); got != 2 {
		t.Fatalf("handler 错误退出码 = %d", got)
	}
	serveCtx, serveCancel := context.WithCancel(context.Background())
	serveCancel()
	serveEnv, _, _ := coverageEnv(t)
	serveEnv.Stdout = io.Discard
	serveEnv.Stderr = io.Discard
	if got := Run(serveCtx, &Env{
		Args:   []string{"serve", "--addr", "127.0.0.1:0"},
		Stdout: io.Discard, Stderr: io.Discard, Getenv: serveEnv.Getenv,
	}); got != 0 {
		t.Fatalf("常驻 handler 自行输出时退出码 = %d", got)
	}
	if got := fail(&Env{Stdout: io.Discard, Stderr: errorWriter{}},
		[]string{"--json"}, &ErrorInfo{Code: "x"}); got != 2 {
		t.Fatalf("JSON 错误写入失败仍应返回 2，实际 %d", got)
	}
	if hasJSONFlag([]string{"--json=false", "json"}) {
		t.Fatal("非精确 --json 不应被识别")
	}
	if !reflect.DeepEqual(ModeNames(), []string{
		"autostart", "bridge", "doctor", "panel", "serve", "task-run", "tray", "tui", "update",
	}) {
		t.Fatalf("ModeNames = %v", ModeNames())
	}
}

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestServeAndTrayHandlers(t *testing.T) {
	oldRunTray := runTrayGUI
	t.Cleanup(func() { runTrayGUI = oldRunTray })

	env, _, _ := coverageEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, args := range [][]string{
		{"--addr", "127.0.0.1:0"},
		{"--addr", "127.0.0.1:0", "--json"},
	} {
		var out strings.Builder
		env.Stdout = &out
		env.Stderr = io.Discard
		resp, err := serveHandler(ctx, env, args)
		if err != nil || resp != nil || out.Len() == 0 {
			t.Fatalf("serveHandler(%v): resp=%v err=%v out=%q", args, resp, err, out.String())
		}
	}
	if _, err := serveHandler(context.Background(), env, []string{"--addr", "invalid"}); err == nil {
		t.Fatal("无效监听地址应失败")
	}
	if _, err := serveHandler(context.Background(), env, []string{"--bad"}); err == nil {
		t.Fatal("serve 无效 flag 应失败")
	}
	badEnv := &Env{Getenv: func(string) string { return "" }}
	if _, err := serveHandler(context.Background(), badEnv,
		[]string{"--adapters-dir", filepath.Join(t.TempDir(), "missing")}); err == nil {
		t.Fatal("serve 装配失败应回传")
	}

	runTrayGUI = func(*app.App) error { return nil }
	if resp, err := trayHandler(ctx, env, []string{"--addr", "127.0.0.1:0"}); err != nil || resp != nil {
		t.Fatalf("trayHandler 正常退出: resp=%v err=%v", resp, err)
	}
	runTrayGUI = func(*app.App) error { return errors.New("controlled GUI failure") }
	if _, err := trayHandler(ctx, env, []string{"--addr", "127.0.0.1:0"}); err == nil {
		t.Fatal("GUI 运行错误应回传")
	}
	if _, err := trayHandler(ctx, env, []string{"--bad"}); err == nil {
		t.Fatal("tray 无效 flag 应失败")
	}
	if _, err := trayHandler(ctx, badEnv, []string{"--adapters-dir", filepath.Join(t.TempDir(), "missing")}); err == nil {
		t.Fatal("tray 装配失败应回传")
	}
	if _, err := trayHandler(ctx, env, []string{"--addr", "invalid"}); err == nil {
		t.Fatal("tray HTTP 启动失败应回传")
	}
}

func TestDefaultTrayRunnerIsWiredToGUI(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("默认 tray runner 应先通过 App.GUI 访问组合根")
		}
	}()
	_ = runTrayGUI(nil)
}

type recordingTaskRunRunner struct {
	summary  app.RunSummary
	err      error
	recorded app.RunSummary
	closed   bool
}

func (r *recordingTaskRunRunner) RunAllActionSummary(context.Context, string) (app.RunSummary, error) {
	return r.summary, r.err
}

func (r *recordingTaskRunRunner) RecordTaskRun(_ context.Context, summary app.RunSummary) error {
	r.recorded = summary
	return errors.New("log unavailable")
}

func (r *recordingTaskRunRunner) Close() error {
	r.closed = true
	return nil
}

func enabledSchedule(t *testing.T, dir string, enabled bool) {
	t.Helper()
	cfg := schedule.Default()
	cfg.Enabled = enabled
	if err := schedule.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
}

func TestTaskRunHandlerAndDependencyDefaults(t *testing.T) {
	env, configDir, _ := coverageEnv(t)
	var out strings.Builder
	env.Stdout = &out
	env.Stderr = io.Discard
	if _, err := taskRunHandler(context.Background(), env, []string{"--bad"}); err == nil {
		t.Fatal("task-run 无效 flag 应失败")
	}
	if _, err := taskRunHandler(context.Background(), env, []string{"--config-dir", configDir}); err != nil ||
		!strings.Contains(out.String(), "未启用") {
		t.Fatalf("disabled task-run 输出错误: err=%v out=%q", err, out.String())
	}
	if err := os.WriteFile(filepath.Join(configDir, schedule.FileName), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := taskRunHandler(context.Background(), env, []string{"--config-dir", configDir}); err == nil {
		t.Fatal("损坏 schedule 应回传错误")
	}

	enabledDir := t.TempDir()
	enabledSchedule(t, enabledDir, true)
	env.Getenv = func(key string) string {
		if key == "QKA_ADAPTERS_DIR" {
			return filepath.Join(enabledDir, "missing")
		}
		return ""
	}
	out.Reset()
	if resp, err := taskRunHandler(context.Background(), env, []string{"--config-dir", enabledDir}); err != nil ||
		resp.Status != "failed" || !strings.Contains(out.String(), "需处理") {
		t.Fatalf("enabled task-run 失败摘要输出错误: resp=%+v err=%v out=%q", resp, err, out.String())
	}

	deps := defaultTaskRunDeps(nil)
	if deps.LoadSchedule == nil || deps.SaveLastRun == nil || deps.AcquireLock == nil ||
		deps.Build == nil || deps.Now == nil {
		t.Fatalf("defaultTaskRunDeps 不完整: %+v", deps)
	}
	lock, err := deps.AcquireLock(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_ = lock.Close()
	configFile := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(configFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := deps.Build(context.Background(), configFile); err == nil {
		t.Fatal("默认 Build 在缺适配器时不应成功")
	}
	if deps.Now().IsZero() {
		t.Fatal("默认 Now 不应为零值")
	}
}

func TestTaskRunCoverageBranches(t *testing.T) {
	dir := t.TempDir()
	enabledSchedule(t, dir, true)
	base := taskRunDeps{
		LoadSchedule: schedule.Load,
		SaveLastRun:  schedule.RecordLastRun,
		AcquireLock:  func(string) (io.Closer, error) { return nopCloser{}, nil },
		Build: func(context.Context, string) (taskRunRunner, error) {
			return &fakeTaskRunRunner{}, nil
		},
		Now: time.Now,
	}
	if _, err := runTaskRun(context.Background(), dir, taskRunDeps{}); err == nil {
		t.Fatal("依赖不完整应失败")
	}

	broken := base
	broken.LoadSchedule = func(string) (schedule.Config, error) {
		return schedule.Config{}, errors.New("load failed")
	}
	if _, err := runTaskRun(context.Background(), dir, broken); err == nil {
		t.Fatal("预检加载失败应回传")
	}
	lockFail := base
	lockFail.AcquireLock = func(string) (io.Closer, error) { return nil, errors.New("locked") }
	if _, err := runTaskRun(context.Background(), dir, lockFail); err == nil {
		t.Fatal("加锁失败应回传")
	}

	calls := 0
	secondLoadFail := base
	secondLoadFail.LoadSchedule = func(string) (schedule.Config, error) {
		calls++
		if calls == 1 {
			return schedule.Config{Enabled: true, IntervalMinutes: 30, Action: schedule.ActionAll, Timezone: schedule.TimezoneLocal}, nil
		}
		return schedule.Config{}, errors.New("reload failed")
	}
	if _, err := runTaskRun(context.Background(), dir, secondLoadFail); err == nil {
		t.Fatal("锁后重读失败应回传")
	}

	calls = 0
	secondDisabled := base
	secondDisabled.LoadSchedule = func(string) (schedule.Config, error) {
		calls++
		if calls == 1 {
			return schedule.Config{Enabled: true, IntervalMinutes: 30, Action: schedule.ActionAll, Timezone: schedule.TimezoneLocal}, nil
		}
		return schedule.Config{Enabled: false, IntervalMinutes: 30, Action: schedule.ActionAll, Timezone: schedule.TimezoneLocal}, nil
	}
	resp, err := runTaskRun(context.Background(), dir, secondDisabled)
	if err != nil || resp.Status != "disabled" {
		t.Fatalf("锁后关闭应跳过: resp=%+v err=%v", resp, err)
	}

	saveFailBuild := base
	saveFailBuild.Build = func(context.Context, string) (taskRunRunner, error) {
		return nil, errors.New("build failed")
	}
	saveFailBuild.SaveLastRun = func(string, schedule.LastRun) error { return errors.New("save failed") }
	if _, err := runTaskRun(context.Background(), dir, saveFailBuild); err == nil {
		t.Fatal("构建失败时保存失败应回传")
	}

	runner := &recordingTaskRunRunner{err: errors.New("run failed")}
	recordDeps := base
	recordDeps.Build = func(context.Context, string) (taskRunRunner, error) { return runner, nil }
	resp, err = runTaskRun(context.Background(), dir, recordDeps)
	if err != nil || resp.Status != "needs_attention" || resp.ExitCode != 1 ||
		runner.recorded.NeedsAttention != 1 || !runner.closed {
		t.Fatalf("执行失败摘要未正确收口: resp=%+v runner=%+v err=%v", resp, runner, err)
	}

	saveFailRun := base
	saveFailRun.SaveLastRun = func(string, schedule.LastRun) error { return errors.New("save result failed") }
	if _, err := runTaskRun(context.Background(), dir, saveFailRun); err == nil {
		t.Fatal("执行后保存失败应回传")
	}
}

// TestServeHandlerStopsOnSignal 覆盖常驻 serve 收到 SIGINT 后的优雅退出分支。
// 先注册测试自己的信号通道，确保信号不会透传到进程默认处理而中断 go test。
func TestServeHandlerStopsOnSignal(t *testing.T) {
	env, _, _ := coverageEnv(t)
	env.Stdout = io.Discard
	env.Stderr = io.Discard

	guard := make(chan os.Signal, 4)
	signal.Notify(guard, syscall.SIGINT)
	defer signal.Stop(guard)

	type result struct {
		resp *Response
		err  error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := serveHandler(context.Background(), env,
			[]string{"--addr", "127.0.0.1:0", "--json"})
		done <- result{resp: resp, err: err}
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		select {
		case got := <-done:
			if got.resp != nil || got.err != nil {
				t.Fatalf("serveHandler 信号退出: resp=%v err=%v", got.resp, got.err)
			}
			return
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("serveHandler 未在收到 SIGINT 后退出")
		}
		if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
			t.Fatalf("发送 SIGINT 失败: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
