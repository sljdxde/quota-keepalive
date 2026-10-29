package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"qka/internal/accounts"
)

// RunAll 是「立即全部执行」的后端：与宿主调用同一条执行链，按预演（plan_validate）
// 中可执行的账号×操作逐个运行，遵守当天幂等与保活冷却；单个失败不拖垮其余，
// 最终返回聚合错误（若有）。
func (a *App) RunAll(ctx context.Context) error {
	plan, err := a.status.PlanValidate(ctx)
	if err != nil {
		return err
	}

	var failures []error
	for _, item := range plan.Items {
		// 预演标注 no_change（冲突/冷却/已终态）的项跳过，不重复发起。
		if item.Transition == "no_change" {
			continue
		}
		if _, err := a.runOperation(ctx, item.Target, item.AccountID, item.Operation); err != nil {
			failures = append(failures, fmt.Errorf("%s/%s/%s: %w",
				item.Target, item.AccountID, item.Operation, err))
		}
	}
	return errors.Join(failures...)
}

// discoverViaHost 调用目标适配器的 accounts.discover，把脱敏结果解析为发现账号。
func (a *App) discoverViaHost(ctx context.Context, target string) ([]accounts.DiscoveredAccount, error) {
	host, ok := a.hosts[target]
	if !ok {
		return nil, fmt.Errorf("目标适配器未运行: %s", target)
	}

	raw, err := host.Call(ctx, "accounts.discover")
	if err != nil {
		return nil, err
	}

	var result struct {
		Status   string `json:"status"`
		Accounts []struct {
			ID             string `json:"id"`
			DisplayName    string `json:"displayName"`
			MaskedIdentity string `json:"maskedIdentity"`
			Source         struct {
				ID     string `json:"id"`
				Kind   string `json:"kind"`
				Health string `json:"health"`
			} `json:"source"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}

	disc := make([]accounts.DiscoveredAccount, 0, len(result.Accounts))
	for _, ac := range result.Accounts {
		disc = append(disc, accounts.DiscoveredAccount{
			ID:             ac.ID,
			DisplayName:    ac.DisplayName,
			MaskedIdentity: ac.MaskedIdentity,
			SourceID:       ac.Source.ID,
			SourceKind:     accounts.SourceKind(ac.Source.Kind),
			Health:         accounts.SourceHealth(ac.Source.Health),
		})
	}
	return disc, nil
}

// Discover 实现 gui.HostDiscoverer：经适配器发现目标账号（脱敏、无凭据）。
func (a *App) Discover(ctx context.Context, target string) ([]accounts.DiscoveredAccount, error) {
	return a.discoverViaHost(ctx, target)
}
