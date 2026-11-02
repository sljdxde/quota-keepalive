package app

import (
	"context"
	"fmt"
)

// manageAccounts 处理 accounts_manage：list/discover/set_enabled/pin/delete。
func (a *App) manageAccounts(ctx context.Context, args map[string]any) (any, error) {
	action := strArg(args, "action")
	target := strArg(args, "target")

	switch action {
	case "", "list":
		return a.accounts.List(ctx, target)

	case "discover":
		return a.discoverAccounts(ctx, target)

	case "set_enabled":
		accountID := strArg(args, "account_id")
		enabled, _ := args["enabled"].(bool)
		if err := a.accounts.SetEnabled(ctx, target, accountID, enabled); err != nil {
			return nil, err
		}
		return a.accounts.List(ctx, target)

	case "pin":
		accountID := strArg(args, "account_id")
		sourceID := strArg(args, "source_id")
		pinned, _ := args["pinned"].(bool)
		if err := a.accounts.PinSource(ctx, target, accountID, sourceID, pinned); err != nil {
			return nil, err
		}
		return a.accounts.List(ctx, target)

	case "delete":
		accountID := strArg(args, "account_id")
		providerOwned := false
		var err error
		if a.providers != nil && a.cfg.ProviderPluginsRoot != "" {
			providerOwned, err = a.providers.HasProvider(ctx, target)
		}
		if err == nil {
			if providerOwned {
				err = a.providers.DeleteAccount(ctx, target, accountID)
			} else {
				err = a.accounts.Delete(ctx, target, accountID)
			}
		}
		if err != nil {
			return nil, err
		}
		return a.accounts.List(ctx, target)

	default:
		return nil, fmt.Errorf("未知 accounts_manage action: %s", action)
	}
}

// discoverAccounts 调用适配器 accounts.discover，把脱敏账号落库（幂等），返回最新清单。
func (a *App) discoverAccounts(ctx context.Context, target string) (any, error) {
	disc, err := a.discoverViaHost(ctx, target)
	if err != nil {
		return nil, err
	}
	if err := a.accounts.Discover(ctx, target, disc); err != nil {
		return nil, err
	}
	return a.accounts.List(ctx, target)
}
