package gui

import (
	"context"
	"errors"
	"time"

	"qka/internal/accounts"
	"qka/internal/login"
)

// ErrNoOfficialEntry 表示该目标没有可用的官方登录入口。
var ErrNoOfficialEntry = errors.New("gui: 无官方登录入口")

// HostDiscoverer 经适配器发现目标应用账号（运行 Bun 适配器）。
type HostDiscoverer interface {
	Discover(ctx context.Context, target string) ([]accounts.DiscoveredAccount, error)
}

// LoginEntries 返回目标应用的官方登录入口（target-specific，由装配提供）。
type LoginEntries interface {
	Entry(target string) (login.Entry, bool)
}

// realBridge 是 AccountBridge 的具体实现。
type realBridge struct {
	acct    *accounts.Service
	login   *login.Service
	hosts   HostDiscoverer
	entries LoginEntries
	openURL func(string) error
}

// NewAccountBridge 组装 AccountBridge。
func NewAccountBridge(
	acct *accounts.Service,
	loginSvc *login.Service,
	hosts HostDiscoverer,
	entries LoginEntries,
	openURL func(string) error,
) AccountBridge {
	return &realBridge{
		acct:    acct,
		login:   loginSvc,
		hosts:   hosts,
		entries: entries,
		openURL: openURL,
	}
}

func (b *realBridge) List(ctx context.Context) ([]accounts.Account, error) {
	return b.acct.List(ctx, "")
}

func (b *realBridge) Discover(ctx context.Context, target string) error {
	found, err := b.hosts.Discover(ctx, target)
	if err != nil {
		return err
	}
	return b.acct.Discover(ctx, target, found)
}

func (b *realBridge) BeginLogin(ctx context.Context, target string) error {
	entry, ok := b.entries.Entry(target)
	if !ok {
		return ErrNoOfficialEntry
	}
	if _, err := b.login.Begin(entry, time.Now()); err != nil {
		return err
	}
	if b.openURL != nil {
		return b.openURL(entry.Location)
	}
	return nil
}

func (b *realBridge) ImportSource(ctx context.Context, target, accountID, sourceID string) error {
	return b.acct.ImportSource(ctx, target, accountID, sourceID)
}

func (b *realBridge) SetEnabled(ctx context.Context, target, accountID string, enabled bool) error {
	return b.acct.SetEnabled(ctx, target, accountID, enabled)
}

func (b *realBridge) Delete(ctx context.Context, target, accountID string) error {
	return b.acct.Delete(ctx, target, accountID)
}

func (b *realBridge) PinSource(ctx context.Context, target, accountID, sourceID string, pinned bool) error {
	return b.acct.PinSource(ctx, target, accountID, sourceID, pinned)
}
