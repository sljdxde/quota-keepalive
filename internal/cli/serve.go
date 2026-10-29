package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func printServing(env *Env, addr, configDir string) {
	fmt.Fprintf(env.Stdout,
		"QKA 服务已启动：%s（配置目录 %s）。按 Ctrl+C 停止。\n", addr, configDir)
}

// Version 是本体版本，发布时由 -ldflags "-X qka/internal/cli.Version=..." 注入。
var Version = "dev"

// serveHandler 启动 MCP 网关与常驻服务，阻塞直到收到中断信号。
func serveHandler(ctx context.Context, env *Env, args []string) (*Response, error) {
	c, err := parseCommonFlags("serve", args)
	if err != nil {
		return nil, err
	}

	built, err := c.buildApp(ctx, env)
	if err != nil {
		return nil, err
	}
	defer func() { _ = built.Close() }()

	httpSrv, err := built.Serve(ctx, c.listenAddr())
	if err != nil {
		return nil, err
	}

	// 启动后立即输出一次状态，随后常驻（输出由本形态自行处理，返回 nil）。
	resp := &Response{
		Status: "serving",
		Data: map[string]any{
			"addr":          httpSrv.Addr,
			"config_dir":    built.ConfigDir(),
			"adapters_root": built.AdaptersRoot(),
		},
	}
	if c.jsonOut {
		_ = writeSuccess(env.Stdout, resp)
	} else {
		printServing(env, httpSrv.Addr, built.ConfigDir())
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
