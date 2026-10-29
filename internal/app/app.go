// Package app 是组合根（composition root）：把 store、适配器宿主、registry、
// 引擎与各工具实现装配为可运行的 qka，并按形态（serve 等）启动。
//
// 各业务包只承载领域逻辑并用注入的依赖做单元测试；本包负责把它们真正连起来，
// 因此也是「从真实入口端到端验证」的唯一权威装配点。
package app

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"qka/internal/accounts"
	"qka/internal/adapterhost"
	"qka/internal/capabilities"
	"qka/internal/engine"
	"qka/internal/registry"
	"qka/internal/status"
	"qka/internal/store"
)

// Config 是组合根的输入。
type Config struct {
	// ConfigDir 是 SQLite 与状态文件所在目录。
	ConfigDir string
	// AdaptersRoot 是适配器父目录（开发态顶层 adapters/，发布态随包目录）。
	AdaptersRoot string
	// BunPath 是运行适配器的 Bun 运行时路径。
	BunPath string
	// Version 是本体版本（initialize/serverInfo 与 doctor 上报）。
	Version string
	// BaseURLs 覆盖适配器目标基址（target id → baseURL），用于受控假目标/自托管。
	BaseURLs map[string]string
	// AllowedHosts 是额外的出站白名单主机（BaseURLs 的主机会自动加入）。
	AllowedHosts []string
	// Now 注入时钟（默认真实时钟）。
	Now func() time.Time
}

// Loaded 是一个适配器的加载结果（宿主或启动错误）。
type Loaded struct {
	Manifest adapterhost.Manifest
	Host     *adapterhost.Host
	LoadErr  error
}

// App 是装配好的应用。
type App struct {
	cfg      Config
	store    *store.Store
	now      func() time.Time
	holderID string

	loaded   []Loaded
	hosts    map[string]*adapterhost.Host
	engine   *engine.Engine
	registry *registry.Registry
	accounts *accounts.Service
	status   *status.Service

	pendingCredentials *capabilities.LoginCredentialStore
	credentials        *capabilities.CredentialService

	workBuddySessions workBuddyManagedSessionCache
}

// Build 装配应用：打开 store、发现并启动适配器、构造各服务。
func Build(ctx context.Context, cfg Config) (*App, error) {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.ConfigDir == "" {
		return nil, fmt.Errorf("app: 缺少 ConfigDir")
	}

	st, err := store.Open(cfg.ConfigDir)
	if err != nil {
		return nil, err
	}

	a := &App{
		cfg:      cfg,
		store:    st,
		now:      cfg.Now,
		holderID: fmt.Sprintf("qka-%d", os.Getpid()),
		hosts:    map[string]*adapterhost.Host{},
	}

	if err := a.launchAdapters(ctx); err != nil {
		_ = st.Close()
		return nil, err
	}

	a.engine = engine.New(st.DB(), a.now, a.holderID)
	a.pendingCredentials = capabilities.NewLoginCredentialStore(
		capabilities.NewKeychainStore(),
		capabilities.NewFileSecretStore(cfg.ConfigDir),
	)
	a.credentials = capabilities.NewCredentialService(
		capabilities.NewKeychainStore(),
		capabilities.NewFileSecretStore(cfg.ConfigDir),
		capabilities.Grant{
			"credentials.managed": true,
			"credentials.resolve": true,
		},
		a.now,
	)
	a.accounts = accounts.NewService(st.DB())
	a.accounts.SetManagedCredentialDeleter(a.credentials)
	a.registry = registry.New(st.DB(), a.now)
	a.registry.SetAdapters(a.registryAdapters())
	a.status = status.NewService(st.DB(), a.now, a.capabilitiesFor)

	return a, nil
}

// Serve 构造 MCP 服务（工具接到本组合根）并在 addr（应为 127.0.0.1:port）启动。
func (a *App) Serve(ctx context.Context, addr string) (*http.Server, error) {
	srv := newMCPServer(a)
	return srv.ListenAndServe(addr)
}

// Store 暴露底层 store（供形态层做额外接线）。
func (a *App) Store() *store.Store { return a.store }

// ConfigDir 返回当前配置目录（供状态输出与 GUI 接线）。
func (a *App) ConfigDir() string { return a.cfg.ConfigDir }

// AdaptersRoot 返回适配器父目录（供状态输出）。
func (a *App) AdaptersRoot() string { return a.cfg.AdaptersRoot }

// Loaded 暴露适配器加载结果（供形态层/诊断使用）。
func (a *App) LoadedAdapters() []Loaded { return a.loaded }

// Close 终止所有适配器子进程并关闭数据库。
func (a *App) Close() error {
	for _, l := range a.loaded {
		if l.Host != nil {
			_ = l.Host.Close()
		}
	}
	return a.store.Close()
}
