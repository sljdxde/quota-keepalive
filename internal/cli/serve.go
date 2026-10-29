package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"qka/internal/app"
)

// Version 是本体版本，发布时由 -ldflags "-X qka/internal/cli.Version=..." 注入。
var Version = "dev"

// serveHandler 启动 MCP 网关与常驻服务，阻塞直到收到中断信号。
func serveHandler(ctx context.Context, env *Env, args []string) (*Response, error) {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOut := fs.Bool("json", false, "以稳定 JSON 输出")
	addrFlag := fs.String("addr", "", "监听地址（如 127.0.0.1:43189）")
	portFlag := fs.Int("port", 0, "监听端口（默认 43189）")
	adaptersFlag := fs.String("adapters-dir", "", "适配器父目录")
	bunFlag := fs.String("bun", "", "Bun 运行时路径")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	listenAddr := *addrFlag
	if listenAddr == "" {
		port := *portFlag
		if port == 0 {
			port = defaultPort
		}
		listenAddr = fmt.Sprintf("127.0.0.1:%d", port)
	}

	cfg := app.Config{
		ConfigDir:    ResolveConfigDir(env.Getenv),
		AdaptersRoot: resolveAdaptersRoot(env, *adaptersFlag),
		BunPath:      resolveBun(env, *bunFlag),
		Version:      Version,
	}

	built, err := app.Build(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer func() { _ = built.Close() }()

	httpSrv, err := built.Serve(ctx, listenAddr)
	if err != nil {
		return nil, err
	}

	// 启动后立即输出一次状态，随后常驻（输出由本形态自行处理，返回 nil）。
	resp := &Response{
		Status: "serving",
		Data: map[string]any{
			"addr":          httpSrv.Addr,
			"config_dir":    cfg.ConfigDir,
			"adapters_root": cfg.AdaptersRoot,
		},
	}
	if *jsonOut {
		_ = writeSuccess(env.Stdout, resp)
	} else {
		fmt.Fprintf(env.Stdout,
			"qka 服务已启动：%s（配置目录 %s）。按 Ctrl+C 停止。\n",
			httpSrv.Addr, cfg.ConfigDir)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-sigCh:
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	return nil, nil
}
