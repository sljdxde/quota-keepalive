package cli

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
)

// launchedFromBundle 判定是否从 .app 包内启动（可执行路径位于 *.app/Contents/MacOS/）。
// 双击 .app 时没有命令行参数，此时应直接进入菜单栏形态而非打印用法。
func launchedFromBundle() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	return strings.Contains(exe, ".app/Contents/MacOS/")
}

// modeNames 是单二进制承诺的全部形态，顺序即对外展示顺序。
var modeNames = []string{"serve", "tray", "panel", "bridge", "tui", "doctor", "update", "autostart"}

// unimplemented 是尚未落地形态的统一处理器：显式报告状态，不崩溃。
func unimplemented(name string) Handler {
	return func(ctx context.Context, env *Env, args []string) (*Response, error) {
		return &Response{Status: "unimplemented", Data: map[string]any{"mode": name}}, nil
	}
}

// buildHandlers 装配形态表：serve 与 tray 为真实实现，其余形态保持显式未实现。
func buildHandlers(env *Env) map[string]Handler {
	h := map[string]Handler{}
	for _, name := range modeNames {
		h[name] = unimplemented(name)
	}
	h["serve"] = serveHandler
	h["tray"] = trayHandler
	return h
}

const usageText = `QKA — Quota KeepAlive Agent（额度保活代理）

用法：
  qka <mode> [flags]

形态：
  serve       启动 MCP 网关与常驻服务
  tray        仅菜单栏
  panel       弹出面板
  bridge      stdio 到 HTTP 桥
  tui         终端界面
  doctor      诊断
  update      自更新
  autostart   开机自启 on|off

所有形态支持 --json 输出。
`

// Run 执行一次命令，返回进程退出码。
func Run(ctx context.Context, env *Env) int {
	if len(env.Args) == 0 {
		// 从 .app 双击启动（无命令行参数）时进入菜单栏形态；终端无参数时打印用法。
		if launchedFromBundle() {
			resp, err := trayHandler(ctx, env, nil)
			if err != nil {
				return fail(env, nil, &ErrorInfo{Code: "command_failed", Message: err.Error()})
			}
			if resp == nil {
				return 0
			}
			return 0
		}
		fmt.Fprint(env.Stdout, usageText)
		return 0
	}

	name := env.Args[0]
	rest := env.Args[1:]

	handler, ok := buildHandlers(env)[name]
	if !ok {
		return fail(env, rest, &ErrorInfo{Code: "unknown_command", Message: fmt.Sprintf("未知形态：%s", name)})
	}

	// 形态自行解析其专属 flags（含 --json）；Run 只负责按契约收口输出。
	resp, err := handler(ctx, env, rest)
	if err != nil {
		return fail(env, rest, &ErrorInfo{Code: "command_failed", Message: err.Error()})
	}
	if resp == nil {
		return 0 // 形态已自行处理输出（如常驻服务）
	}
	if hasJSONFlag(rest) {
		if err := writeSuccess(env.Stdout, resp); err != nil {
			return 1
		}
	}
	return 0
}

func fail(env *Env, rest []string, info *ErrorInfo) int {
	if hasJSONFlag(rest) {
		_ = writeError(env.Stderr, info)
		return 2
	}
	fmt.Fprintf(env.Stderr, "错误：%s\n", info.Message)
	return 2
}

func hasJSONFlag(rest []string) bool {
	for _, a := range rest {
		if a == "--json" {
			return true
		}
	}
	return false
}

// ModeNames 返回形态清单（供文档与测试引用）。
func ModeNames() []string {
	out := make([]string, len(modeNames))
	copy(out, modeNames)
	sort.Strings(out)
	return out
}
