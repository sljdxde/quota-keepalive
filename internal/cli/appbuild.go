package cli

import (
	"context"
	"flag"
	"fmt"
	"io"

	"qka/internal/app"
	"qka/internal/workbuddy"
)

// commonFlags 是 serve / tray 共用的启动参数。
type commonFlags struct {
	jsonOut  bool
	addr     string
	port     int
	adapters string
	bun      string
}

func parseCommonFlags(name string, args []string) (*commonFlags, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	c := &commonFlags{}
	fs.BoolVar(&c.jsonOut, "json", false, "以稳定 JSON 输出")
	fs.StringVar(&c.addr, "addr", "", "监听地址（如 127.0.0.1:43189）")
	fs.IntVar(&c.port, "port", 0, "监听端口（默认 43189）")
	fs.StringVar(&c.adapters, "adapters-dir", "", "适配器父目录")
	fs.StringVar(&c.bun, "bun", "", "Bun 运行时路径")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *commonFlags) listenAddr() string {
	if c.addr != "" {
		return c.addr
	}
	port := c.port
	if port == 0 {
		port = defaultPort
	}
	return fmt.Sprintf("127.0.0.1:%d", port)
}

// buildApp 按共用参数装配组合根。
func (c *commonFlags) buildApp(ctx context.Context, env *Env) (*app.App, error) {
	return c.buildAppAt(ctx, env, ResolveConfigDir(env.Getenv))
}

func (c *commonFlags) buildAppAt(ctx context.Context, env *Env, configDir string) (*app.App, error) {
	cfg := app.Config{
		ConfigDir:           configDir,
		AdaptersRoot:        resolveAdaptersRoot(env, c.adapters),
		BunPath:             resolveBun(env, c.bun),
		ProviderPluginsRoot: resolveProviderPluginsRoot(env, ""),
		Version:             Version,
		BaseURLs:            productionBaseURLs(),
		AllowedHosts:        productionAllowedHosts(),
	}
	return app.Build(ctx, cfg)
}

func productionBaseURLs() map[string]string {
	return map[string]string{"workbuddy-cn": workbuddy.DefaultBaseURL}
}

func productionAllowedHosts() []string {
	return []string{"copilot.tencent.com"}
}
