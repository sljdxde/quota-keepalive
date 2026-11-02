package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"qka/internal/adapterwiring"
	"qka/internal/doctor"
	"qka/internal/engine"
	"qka/internal/features"
	"qka/internal/feed"
	"qka/internal/logtail"
	"qka/internal/mcp"
)

// newMCPServer 构造工具已接线的 MCP 服务。
func newMCPServer(a *App) *mcp.Server {
	return mcp.NewServer(mcp.ServerDeps{
		CallTool: a.CallTool,
		Version:  a.cfg.Version,
	})
}

// CallTool 实现 mcp.ToolCaller：按工具名分发到对应实现。
func (a *App) CallTool(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	var args map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &args)
	}

	switch name {
	case "agents_list":
		return a.registry.List(ctx)

	case "accounts_manage":
		return a.manageAccounts(ctx, args)

	case "checkin_run":
		return a.runCheckin(ctx, args)

	case "status":
		return a.status.Status(ctx)

	case "plan_validate":
		return a.status.PlanValidate(ctx)

	case "log_tail":
		return a.tailLogs(ctx, args)

	case "doctor_run":
		return a.runDoctor(ctx), nil

	case "feed_list":
		return feed.List(), nil

	default:
		return nil, fmt.Errorf("未知工具: %s", name)
	}
}

func (a *App) tailLogs(ctx context.Context, args map[string]any) (any, error) {
	a.pruneLogs(ctx)
	f := logtail.Filter{
		Target:    strArg(args, "target"),
		AccountID: strArg(args, "account_id"),
		Operation: strArg(args, "operation"),
		State:     strArg(args, "state"),
	}
	return logtail.Tail(ctx, a.store.DB(), f, intArg(args, "limit"), int64Arg(args, "cursor"))
}

func (a *App) runDoctor(ctx context.Context) doctor.Report {
	in := doctor.Inputs{
		Version:                a.cfg.Version,
		EndpointReachable:      true,  // serve 形态下自身 loopback 端点可达
		PairingPresent:         false, // 配对状态由后续形态接线（pairing 包），此处保守
		CredentialReadable:     false, // 凭据由适配器/Keychain 管理，此处保守
		LastInvocation:         a.lastInvocation(ctx),
		AdapterEffectsComplete: a.effectsComplete(),
		FilePermissionsOK:      true, // store 已收敛 DB 与目录权限
	}
	return doctor.Run(a.now(), in)
}

func (a *App) runCheckin(ctx context.Context, args map[string]any) (any, error) {
	target := strArg(args, "target")
	accountID := strArg(args, "account_id")
	operation := strArg(args, "operation")
	if operation == "" {
		operation = "checkin"
	}
	return a.RunOperationGuarded(ctx, target, accountID, operation)
}

// runOperation 是类型化的单账号×操作执行核心（checkin / keepalive 共用）。
func (a *App) runOperation(ctx context.Context, target, accountID, operation string) (any, error) {
	if !features.OperationAvailable(operation) {
		return nil, features.ErrKeepaliveDisabled
	}
	if target == "workbuddy-cn" && operation == "checkin" {
		return a.runWorkBuddyCheckin(ctx, accountID)
	}
	host, ok := a.hosts[target]
	if !ok {
		return nil, fmt.Errorf("目标适配器未运行: %s", target)
	}

	key := engine.Key{Target: target, AccountID: accountID, Operation: engine.Operation(operation)}

	runner := adapterwiring.NewCheckinRunner(a.engine, host, a.now, "")
	return runner.Run(ctx, key)
}

func (a *App) lastInvocation(ctx context.Context) time.Time {
	var ts sql.NullString
	_ = a.store.DB().QueryRowContext(ctx,
		`SELECT MAX(ts) FROM logs WHERE operation <> ?`, "keepalive").Scan(&ts)
	if !ts.Valid || ts.String == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, ts.String)
	if err != nil {
		return time.Time{}
	}
	return t
}

func (a *App) effectsComplete() bool {
	if len(a.loaded) == 0 {
		return false
	}
	for _, l := range a.loaded {
		if l.Host == nil {
			continue
		}
		if len(l.Manifest.Actions) == 0 {
			return false
		}
	}
	return true
}

// --- 参数辅助 ---

func strArg(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}

func intArg(args map[string]any, key string) int {
	return int(floatArg(args, key))
}

func int64Arg(args map[string]any, key string) int64 {
	return int64(floatArg(args, key))
}

func floatArg(args map[string]any, key string) float64 {
	if args == nil {
		return 0
	}
	if v, ok := args[key].(float64); ok {
		return v
	}
	return 0
}
