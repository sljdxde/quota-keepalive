package cli

import (
	"context"
	"net/http"
)

// trayHandler 启动菜单栏 GUI：装配组合根，在后台 goroutine 启动 MCP HTTP 网关，
// 然后在主 goroutine 进入 Wails 事件循环（托盘 + 面板 + 主窗口），阻塞直到退出。
func trayHandler(ctx context.Context, env *Env, args []string) (*Response, error) {
	c, err := parseCommonFlags("tray", args)
	if err != nil {
		return nil, err
	}

	built, err := c.buildApp(ctx, env)
	if err != nil {
		return nil, err
	}
	defer func() { _ = built.Close() }()

	// MCP HTTP 网关在后台运行；GUI 事件循环占据主 goroutine。
	var httpSrv *http.Server
	httpSrv, err = built.Serve(ctx, c.listenAddr())
	if err != nil {
		return nil, err
	}
	go func() { _ = httpSrv.ListenAndServe() }()

	guiApp := built.GUI()
	if runErr := guiApp.Run(); runErr != nil {
		_ = httpSrv.Shutdown(context.Background())
		return nil, runErr
	}
	_ = httpSrv.Shutdown(context.Background())
	return nil, nil
}
