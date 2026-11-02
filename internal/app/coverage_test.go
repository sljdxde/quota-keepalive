package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"qka/internal/accounts"
	"qka/internal/adapterhost"
	"qka/internal/capabilities"
	"qka/internal/engine"
	"qka/internal/gui"
	"qka/internal/providerruntime"
	"qka/internal/providers"
	"qka/internal/settings"
	"qka/internal/status"
	"qka/internal/store"
	"qka/internal/workbuddy"
)

func coverageApp(t *testing.T) *App {
	t.Helper()
	root := t.TempDir()
	adapters := filepath.Join(root, "adapters")
	if err := os.Mkdir(adapters, 0o700); err != nil {
		t.Fatal(err)
	}
	a, err := Build(context.Background(), Config{
		ConfigDir:    filepath.Join(root, "config"),
		AdaptersRoot: adapters,
		BunPath:      "unused",
		Version:      "test",
		Now: func() time.Time {
			return time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func TestBuildFailuresAndAccessors(t *testing.T) {
	if _, err := Build(context.Background(), Config{}); err == nil ||
		!strings.Contains(err.Error(), "缺少 ConfigDir") {
		t.Fatalf("空配置目录错误 = %v", err)
	}
	file := filepath.Join(t.TempDir(), "config-file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(context.Background(), Config{ConfigDir: file}); err == nil {
		t.Fatal("ConfigDir 为文件时 store.Open 应失败")
	}
	if _, err := Build(context.Background(), Config{
		ConfigDir:    t.TempDir(),
		AdaptersRoot: filepath.Join(t.TempDir(), "missing"),
	}); err == nil {
		t.Fatal("适配器目录不可读时 Build 应失败")
	}

	a := coverageApp(t)
	if a.Store() == nil || a.ConfigDir() == "" || a.AdaptersRoot() == "" ||
		len(a.LoadedAdapters()) != 0 {
		t.Fatalf("accessors 未返回装配结果: %#v", a)
	}
}

func TestAdapterDiscoveryViewsAndCapabilities(t *testing.T) {
	root := t.TempDir()
	adapters := filepath.Join(root, "adapters")
	for _, dir := range []string{"_template", "node_modules", "empty"} {
		if err := os.MkdirAll(filepath.Join(adapters, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(adapters, "not-a-dir"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := Build(context.Background(), Config{
		ConfigDir: filepath.Join(root, "config"), AdaptersRoot: adapters,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if len(a.LoadedAdapters()) != 0 {
		t.Fatalf("无效目录应被跳过: %+v", a.LoadedAdapters())
	}

	viewApp := &App{loaded: []Loaded{
		{Manifest: adapterhost.Manifest{ID: "with", Capabilities: []string{"keepalive"}}, Host: &adapterhost.Host{}},
		{Manifest: adapterhost.Manifest{ID: "failed"}, LoadErr: errors.New("failed")},
	}}
	views := viewApp.registryAdapters()
	if len(views) != 2 || views[0].Disabled || !views[1].Disabled {
		t.Fatalf("registry views = %+v", views)
	}
	if got := viewApp.capabilitiesFor("with"); len(got) != 1 || got[0] != "checkin" {
		t.Fatalf("capabilities = %v", got)
	}
	if got := viewApp.capabilitiesFor("missing"); len(got) != 1 || got[0] != "checkin" {
		t.Fatalf("default capabilities = %v", got)
	}
}

func TestAccountManagementAndDiscoverErrors(t *testing.T) {
	a := coverageApp(t)
	ctx := context.Background()
	if err := a.accounts.Discover(ctx, "target", []accounts.DiscoveredAccount{{
		ID: "acct", SourceID: "src", SourceKind: accounts.KindManaged,
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.manageAccounts(ctx, map[string]any{"action": "list", "target": "target"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.manageAccounts(ctx, map[string]any{
		"action": "set_enabled", "target": "target", "account_id": "missing", "enabled": true,
	}); err == nil {
		t.Fatal("启用不存在账号应失败")
	}
	errCtx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := a.manageAccounts(errCtx, map[string]any{
		"action": "set_enabled", "target": "target", "account_id": "acct", "enabled": true,
	}); err == nil {
		t.Fatal("启用查询失败应回传")
	}
	if _, err := a.manageAccounts(errCtx, map[string]any{
		"action": "pin", "target": "target", "account_id": "acct", "source_id": "missing",
	}); err == nil {
		t.Fatal("固定来源写入失败应回传")
	}
	if _, err := a.manageAccounts(errCtx, map[string]any{
		"action": "delete", "target": "target", "account_id": "missing",
	}); err == nil {
		t.Fatal("删除写入失败应回传")
	}
	if _, err := a.manageAccounts(ctx, map[string]any{"action": "unknown"}); err == nil {
		t.Fatal("未知 action 应失败")
	}
	if _, err := a.discoverAccounts(ctx, "missing"); err == nil {
		t.Fatal("未运行适配器 discover 应失败")
	}
	if _, err := a.discoverAccounts(ctx, ""); err == nil {
		t.Fatal("空目标 discover 应失败")
	}
}

func TestToolDispatchHelpersAndLockFallbacks(t *testing.T) {
	a := coverageApp(t)
	ctx := context.Background()
	for _, name := range []string{"agents_list", "plan_validate", "doctor_run", "feed_list"} {
		if _, err := a.CallTool(ctx, name, nil); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := a.CallTool(ctx, "unknown", nil); err == nil {
		t.Fatal("未知工具应失败")
	}
	if _, err := a.CallTool(ctx, "checkin_run", rawJSON(`{"target":"missing"}`)); err == nil {
		t.Fatal("未运行适配器签到应失败")
	}
	if _, err := a.CallTool(ctx, "checkin_run", rawJSON(`{"target":"missing","operation":"keepalive"}`)); err == nil {
		t.Fatal("未运行适配器保活应失败")
	}
	if !a.lastInvocation(ctx).IsZero() {
		t.Fatal("空日志最近调用应为零值")
	}
	if _, err := a.store.DB().ExecContext(ctx,
		`INSERT INTO logs(ts,target,account_id,operation,state,level,message) VALUES(?,?,?,?,?,?,?)`,
		"invalid", "t", "a", "checkin", "ok", "info", "x"); err != nil {
		t.Fatal(err)
	}
	if !a.lastInvocation(ctx).IsZero() {
		t.Fatal("无效时间戳应返回零值")
	}
	if a.effectsComplete() {
		t.Fatal("无适配器时 effectsComplete 应为 false")
	}
	a.loaded = []Loaded{{Manifest: adapterhost.Manifest{ID: "failed"}}}
	if !a.effectsComplete() {
		t.Fatal("失败适配器不阻断其余适配器完整性判断")
	}
	a.loaded = []Loaded{{Manifest: adapterhost.Manifest{Actions: []adapterhost.Action{{Op: "x"}}}, Host: &adapterhost.Host{}}}
	if !a.effectsComplete() {
		t.Fatal("完整适配器 effectsComplete 应为 true")
	}
	a.loaded = nil
	if strArg(nil, "x") != "" || strArg(map[string]any{"x": 1}, "x") != "" ||
		floatArg(nil, "x") != 0 || floatArg(map[string]any{"x": "1"}, "x") != 0 {
		t.Fatal("参数辅助函数边界错误")
	}

	a.cfg.ConfigDir = ""
	if _, err := a.RunAllGuarded(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := a.RunOperationGuarded(ctx, "missing", "a", "checkin"); err == nil {
		t.Fatal("无适配器操作应失败")
	}
	if _, err := a.RunAllActionSummary(ctx, "invalid"); err == nil {
		t.Fatal("非法 action 应失败")
	}
	a.cfg.ConfigDir = filepath.Join(t.TempDir(), "config-file")
	if err := os.WriteFile(a.cfg.ConfigDir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.RunAllGuarded(ctx); err == nil {
		t.Fatal("runlock 获取失败应回传")
	}
	if _, err := a.RunOperationGuarded(ctx, "x", "a", "checkin"); err == nil {
		t.Fatal("单操作 runlock 获取失败应回传")
	}
	if _, err := a.acquireRunLock(); err == nil {
		t.Fatal("acquireRunLock 失败应回传")
	}
	unlock, err := (&App{}).acquireRunLock()
	if err != nil {
		t.Fatal(err)
	}
	unlock()
}

func TestRunAllProviderExecutionViewErrors(t *testing.T) {
	a := coverageApp(t)
	ctx := context.Background()
	if view, accountsByProvider, err := a.providerExecutionView(ctx); err != nil ||
		len(view.Providers) != 0 || len(accountsByProvider) != 0 {
		t.Fatalf("未配置 provider 时应为空: %+v %+v %v", view, accountsByProvider, err)
	}
	a.cfg.ProviderPluginsRoot = "plugins"
	a.providers = providers.NewService(
		a.store.DB(), a.accounts, a.credentials, a.engine,
		func(context.Context) (providers.Runtime, error) {
			return nil, errors.New("provider unavailable")
		},
		a.now,
	)
	if _, _, err := a.providerExecutionView(ctx); err == nil {
		t.Fatal("provider 启动失败应由执行域同步返回")
	}
	summary, err := a.RunAllActionSummary(ctx, "all")
	if err == nil || summary.NeedsAttention != 0 {
		t.Fatalf("provider 失败应回传且不伪造账号失败: summary=%+v err=%v", summary, err)
	}
}

func TestRunAllSkipsOnlyPluginOwnedAccountsAtSameTarget(t *testing.T) {
	a := coverageApp(t)
	ctx := context.Background()
	if err := a.accounts.Discover(ctx, "workbuddy-cn", []accounts.DiscoveredAccount{{
		ID: "official", DisplayName: "Official", MaskedIdentity: "****icial",
		SourceID: "official-source", SourceKind: accounts.KindManaged,
		Health: accounts.HealthHealthy,
	}}); err != nil {
		t.Fatal(err)
	}
	fake := &fakeProviderRuntime{providers: []providerruntime.Provider{{
		ID: "workbuddy-cn", Name: "WorkBuddy CN", Checkin: true,
		Accounts: []providerruntime.Account{{
			Key: "plugin", AccountID: "plugin@example.com",
		}},
	}}}
	a.cfg.ProviderPluginsRoot = "plugins"
	a.providers = providers.NewService(
		a.store.DB(), a.accounts, a.credentials, a.engine,
		func(context.Context) (providers.Runtime, error) { return fake, nil },
		a.now,
	)
	a.status = status.NewService(a.store.DB(), a.now, func(string) []string {
		return []string{"checkin"}
	})
	// Provider plugins ship disabled until the user enables them; this case is
	// about two execution lanes sharing one target, so enable it explicitly.
	if _, err := a.providers.List(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.providers.SetEnabled(ctx, "workbuddy-cn", true); err != nil {
		t.Fatal(err)
	}
	oldOperation := executeRunOperation
	t.Cleanup(func() { executeRunOperation = oldOperation })
	var calls []string
	executeRunOperation = func(
		_ *App, _ context.Context, target, account, operation string,
	) (any, error) {
		calls = append(calls, target+"/"+account+"/"+operation)
		return engine.Observation{Executed: true, State: engine.Already}, nil
	}
	summary, err := a.RunAllActionSummary(ctx, "checkin")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(calls, []string{"workbuddy-cn/official/checkin"}) {
		t.Fatalf("应只跳过插件拥有的账号，实际调用 %v", calls)
	}
	if summary.Attempted != 2 || summary.Succeeded != 2 {
		t.Fatalf("官方与插件账号都应执行一次: %+v", summary)
	}
}

type errorSecretStore struct{}

func (errorSecretStore) Set(context.Context, string, string, string) error {
	return errors.New("store unavailable")
}
func (errorSecretStore) Get(context.Context, string, string) (string, error) {
	return "", errors.New("store unavailable")
}
func (errorSecretStore) Delete(context.Context, string, string) error {
	return errors.New("store unavailable")
}

func TestProviderRuntimeFactoryBranches(t *testing.T) {
	oldDiscover, oldLaunch := discoverProviderPlugins, launchProviderRuntime
	t.Cleanup(func() {
		discoverProviderPlugins, launchProviderRuntime = oldDiscover, oldLaunch
	})

	a := &App{cfg: Config{ConfigDir: t.TempDir()}}
	factory := a.providerRuntimeFactory()
	if _, err := factory(context.Background()); err == nil {
		t.Fatal("空插件根目录应失败")
	}
	a.cfg.ProviderPluginsRoot = filepath.Join(t.TempDir(), "missing")
	if _, err := factory(context.Background()); err == nil {
		t.Fatal("插件发现失败应回传")
	}
	discoverProviderPlugins = func(string) ([]providerruntime.Plugin, error) { return nil, nil }
	if _, err := factory(context.Background()); err == nil {
		t.Fatal("没有插件应失败")
	}

	discoverProviderPlugins = func(string) ([]providerruntime.Plugin, error) {
		return []providerruntime.Plugin{{Spec: "p", Target: "t"}}, nil
	}
	a.credentials = capabilities.NewCredentialService(
		failingAppSecretStore{}, failingAppSecretStore{},
		capabilities.Grant{"credentials.resolve": true}, nil,
	)
	launchProviderRuntime = func(context.Context, providerruntime.LaunchConfig) (*providerruntime.Host, error) {
		return &providerruntime.Host{}, nil
	}
	host, err := factory(context.Background())
	if err != nil || host == nil {
		t.Fatalf("无凭据时应使用空 auth 启动: host=%v err=%v", host, err)
	}

	a.credentials = capabilities.NewCredentialService(
		failingAppSecretStore{}, errorSecretStore{},
		capabilities.Grant{"credentials.resolve": true}, nil,
	)
	if _, err := factory(context.Background()); err == nil {
		t.Fatal("凭据解析非 NotFound 错误应回传")
	}

	fileStore := newAppSecretStore()
	a.credentials = capabilities.NewCredentialService(
		failingAppSecretStore{}, fileStore,
		capabilities.Grant{"credentials.resolve": true, "credentials.managed": true}, nil,
	)
	ref := capabilities.CredentialRef{
		Target: "qka-provider", AccountID: "runtime", SourceID: "auth", Kind: capabilities.Managed,
	}
	if err := a.credentials.Persist(context.Background(), ref, `{"token":"ok"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := factory(context.Background()); err != nil {
		t.Fatalf("有效凭据应启动: %v", err)
	}
	if err := a.credentials.Persist(context.Background(), ref, "not-json"); err != nil {
		t.Fatal(err)
	}
	if _, err := factory(context.Background()); err != nil {
		t.Fatalf("无效凭据应退化为空 JSON: %v", err)
	}

	a.credentials = nil
	if err := os.MkdirAll(a.cfg.ConfigDir, 0o700); err != nil {
		t.Fatal(err)
	}
	authPath := providers.StaleAuthPath(a.cfg.ConfigDir)
	if err := os.Remove(authPath); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.MkdirAll(authPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := factory(context.Background()); err == nil {
		t.Fatal("auth 文件写入失败应回传")
	}
	if err := os.Remove(authPath); err != nil {
		t.Fatal(err)
	}
	launchProviderRuntime = func(context.Context, providerruntime.LaunchConfig) (*providerruntime.Host, error) {
		return nil, errors.New("launch failed")
	}
	if _, err := factory(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "provider runtime") {
		t.Fatalf("启动失败应包装错误: %v", err)
	}
}

type fakeProviderRuntime struct {
	authorize providerruntime.Authorization
	providers []providerruntime.Provider
}

func (f *fakeProviderRuntime) Providers(context.Context) ([]providerruntime.Provider, error) {
	return f.providers, nil
}
func (f *fakeProviderRuntime) Prompt(context.Context, string, int, map[string]string) (providerruntime.Prompt, error) {
	return providerruntime.Prompt{}, nil
}
func (f *fakeProviderRuntime) Authorize(context.Context, string, int, map[string]string, string) (providerruntime.Authorization, error) {
	return f.authorize, nil
}
func (f *fakeProviderRuntime) Finish(context.Context, string, string) (providerruntime.Saved, error) {
	return providerruntime.Saved{}, nil
}
func (f *fakeProviderRuntime) APIKey(context.Context, string, int, map[string]string, string, string) (providerruntime.Saved, error) {
	return providerruntime.Saved{}, nil
}
func (f *fakeProviderRuntime) Usage(context.Context, string, string) (providerruntime.Usage, error) {
	return providerruntime.Usage{}, nil
}
func (f *fakeProviderRuntime) Checkin(context.Context, string, string) (providerruntime.Checkin, error) {
	return providerruntime.Checkin{Outcome: "done"}, nil
}
func (f *fakeProviderRuntime) Load(context.Context, string, string) (providerruntime.Loader, error) {
	return providerruntime.Loader{}, nil
}
func (f *fakeProviderRuntime) Fetch(context.Context, providerruntime.FetchRequest) (providerruntime.FetchResponse, error) {
	return providerruntime.FetchResponse{}, nil
}
func (f *fakeProviderRuntime) SignOut(context.Context, string, string) error { return nil }
func (f *fakeProviderRuntime) ReadAuth() ([]byte, error)                     { return nil, os.ErrNotExist }
func (f *fakeProviderRuntime) RemoveAuth() error                             { return nil }
func (f *fakeProviderRuntime) Close() error                                  { return nil }

func TestGUIDependenciesAndState(t *testing.T) {
	oldExecutable, oldHome, oldOpen := appExecutable, userHomeDir, openBrowser
	t.Cleanup(func() {
		appExecutable, userHomeDir, openBrowser = oldExecutable, oldHome, oldOpen
	})

	a := coverageApp(t)
	state := &guiState{app: a, settings: settings.Default(), audit: []gui.AuditLine{}}
	if state.currentSettings() == nil {
		t.Fatal("currentSettings 为空")
	}
	changed := settings.Default()
	if err := changed.Set("launch_at_login", false); err != nil {
		t.Fatal(err)
	}
	if err := state.saveSettings(changed); err != nil {
		t.Fatal(err)
	}
	if _, err := state.logs(context.Background(), gui.LogFilter{}, 10, 0); err != nil {
		t.Fatal(err)
	}
	if view, err := state.agentStatus(context.Background()); err != nil || len(view.Hosts) == 0 {
		t.Fatalf("agentStatus = %+v err=%v", view, err)
	}
	if view, err := state.plugins(context.Background()); err != nil || view.DevMode {
		t.Fatalf("plugins = %+v err=%v", view, err)
	}
	if err := state.setDevMode(true); err != nil {
		t.Fatal(err)
	}
	if view, err := state.plugins(context.Background()); err != nil || !view.DevMode {
		t.Fatalf("dev mode = %+v err=%v", view, err)
	}

	t.Setenv("HOME", t.TempDir())
	appExecutable = func() (string, error) { return "/Applications/QKA", nil }
	userHomeDir = func() (string, error) { return "", errors.New("home unavailable") }
	if err := state.afterSaveSettings(changed); err == nil {
		t.Fatal("home 获取失败应回传")
	}
	appExecutable = func() (string, error) { return "", errors.New("exe unavailable") }
	if err := state.afterSaveSettings(changed); err == nil {
		t.Fatal("可执行文件获取失败应回传")
	}
	appExecutable = oldExecutable
	userHomeDir = oldHome
	if err := state.afterSaveSettings(changed); err != nil {
		t.Fatal(err)
	}

	bad := &guiState{app: &App{cfg: Config{ConfigDir: filepath.Join(t.TempDir(), "file")}}}
	if err := os.WriteFile(bad.app.cfg.ConfigDir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := bad.saveSettings(settings.Default()); err == nil {
		t.Fatal("设置保存失败应回传")
	}

	deps := a.guiDeps()
	if deps.Settings == nil || deps.TokenSpeedGet == nil || deps.QuitApp == nil ||
		deps.ProviderBeginLogin == nil || deps.ProviderRun == nil {
		t.Fatalf("GUI deps 不完整: %+v", deps)
	}
	// 反馈地址提供器每次提交时重新读设置；默认未配置时返回空串（跟随内置地址）。
	if deps.Feedback == nil || deps.Feedback.EndpointProvider == nil {
		t.Fatal("反馈地址提供器未接线")
	}
	if got := deps.Feedback.EndpointProvider(); got != "" {
		t.Fatalf("默认反馈地址应为空，实际 %q", got)
	}
	if token, err := deps.TokenSpeedGet(context.Background()); err != nil || token.OutputTokensPerSecond != 0 {
		t.Fatalf("TokenSpeedGet = %+v err=%v", token, err)
	}
	if err := deps.AfterSaveSettings(changed); err != nil {
		t.Fatal(err)
	}
	if _, err := deps.ScheduleSetEnabled(context.Background(), false); err != nil {
		t.Fatal(err)
	}

	opened := ""
	openBrowser = func(url string) error { opened = url; return nil }
	fake := &fakeProviderRuntime{
		providers: []providerruntime.Provider{{ID: "provider", Methods: []providerruntime.Method{{Type: "oauth"}}}},
		authorize: providerruntime.Authorization{Session: "s", URL: "https://login.invalid"},
	}
	a.providers = providers.NewService(
		a.store.DB(), a.accounts, a.credentials, a.engine,
		func(context.Context) (providers.Runtime, error) { return fake, nil },
		a.now,
	)
	view, err := deps.ProviderBeginLogin(context.Background(), "provider", 0, nil)
	if err != nil || view.Status != "authorized" || opened != view.URL {
		t.Fatalf("ProviderBeginLogin = %+v opened=%q err=%v", view, opened, err)
	}

	a.providers = providers.NewService(
		a.store.DB(), a.accounts, a.credentials, a.engine,
		func(context.Context) (providers.Runtime, error) { return nil, errors.New("no runtime") },
		a.now,
	)
	if err := deps.ProviderRun(context.Background(), "provider", "account", "checkin"); err == nil {
		t.Fatal("ProviderRun 的运行时错误应回传")
	}
	if err := a.RunProviderAccount(context.Background(), "provider", "account", "checkin"); err == nil {
		t.Fatal("RunProviderAccount 的运行时错误应回传")
	}

	if guiApp := a.GUI(); guiApp == nil {
		t.Fatal("GUI 构造失败")
	}
	deps.QuitApp()

	noServices := &App{cfg: Config{ConfigDir: t.TempDir()}, now: time.Now}
	if noServices.workBuddyLoginService() == nil {
		t.Fatal("未接线时应构造本地登录服务")
	}
}

func TestRunAccountAndGUIErrorPaths(t *testing.T) {
	a := coverageApp(t)
	a.cfg.ConfigDir = filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(a.cfg.ConfigDir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := a.RunAccount(context.Background(), "workbuddy-cn", "a", "refresh"); err == nil {
		t.Fatal("runlock 获取失败应回传")
	}

	a.cfg.ConfigDir = filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(a.cfg.ConfigDir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	deps := a.guiDeps()
	if deps.Settings == nil {
		t.Fatal("设置加载失败应回退默认值")
	}
}

func TestWorkBuddyManagedCredentialStoreErrors(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	acct := accounts.NewService(st.DB())
	creds := capabilities.NewCredentialService(
		failingAppSecretStore{}, failingAppSecretStore{},
		capabilities.Grant{"credentials.resolve": true, "credentials.managed": true}, nil,
	)
	storeAdapter := &workBuddyManagedCredentialStore{
		credentials: creds,
		accounts:    acct,
		ref: capabilities.CredentialRef{
			Target: "workbuddy-cn", AccountID: "a", SourceID: "s", Kind: capabilities.Managed,
		},
	}
	if _, err := storeAdapter.Load(ctx); err == nil {
		t.Fatal("缺失凭据应回传")
	}
}

func TestDiscoverViaHostCallFailure(t *testing.T) {
	a := &App{hosts: map[string]*adapterhost.Host{"bad": {}}}
	if _, err := a.discoverViaHost(context.Background(), "bad"); err == nil {
		t.Fatal("未启动适配器 Call 应失败")
	}
}

func TestRunAllGuardedAndStatusRequestFailure(t *testing.T) {
	a := coverageApp(t)
	if _, err := a.RunAllGuarded(context.Background()); err != nil {
		t.Fatal(err)
	}
	a.cfg.ConfigDir = ""
	if _, err := a.RunAllGuarded(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.RunOperationGuarded(context.Background(), "missing", "a", "checkin"); err == nil {
		t.Fatal("无适配器单操作应失败")
	}
	a.status = status.NewService(a.store.DB(), a.now, func(string) []string { return nil })
	if _, err := a.RunAllActionSummary(context.Background(), "all"); err != nil {
		t.Fatal(err)
	}
}

func TestOptionalUsageAndEngineHelpers(t *testing.T) {
	if optionalUsageValue(1, false) != nil {
		t.Fatal("未知用量应返回 nil")
	}
	if got := optionalUsageValue(0, true); got == nil || *got != 0 {
		t.Fatal("已知零用量应保留指针")
	}
	a := &App{now: time.Now}
	obs := engine.Observation{}
	if err := a.recordReviewUsage(context.Background(), engine.Key{}, &obs, workbuddyReview()); err != nil {
		t.Fatal(err)
	}
	a.writeWorkBuddyCheckinLog(context.Background(), "a", obs)
}

func workbuddyReview() workbuddy.CheckinReview {
	return workbuddy.CheckinReview{}
}
