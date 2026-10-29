package gui

import (
	"context"
	"errors"

	"qka/internal/accounts"
	"qka/internal/workbuddy"
)

// ErrNoOfficialEntry 表示该目标没有可用的官方登录入口。
var ErrNoOfficialEntry = errors.New("gui: 无官方登录入口")

// HostDiscoverer 经适配器发现目标应用账号（运行 Bun 适配器）。
type HostDiscoverer interface {
	Discover(ctx context.Context, target string) ([]accounts.DiscoveredAccount, error)
}

// LoginManager 协调目标应用的官方浏览器登录。
type LoginManager interface {
	Begin(ctx context.Context, target string) (workbuddy.LoginView, error)
	Status(ctx context.Context, target string) workbuddy.LoginView
	Cancel(ctx context.Context, target string) (workbuddy.LoginView, error)
}

// realBridge 是 AccountBridge 的具体实现。
type realBridge struct {
	acct   *accounts.Service
	hosts  HostDiscoverer
	logins LoginManager
}

// NewAccountBridge 组装 AccountBridge。
func NewAccountBridge(
	acct *accounts.Service,
	hosts HostDiscoverer,
	logins LoginManager,
) AccountBridge {
	return &realBridge{
		acct:   acct,
		hosts:  hosts,
		logins: logins,
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

func (b *realBridge) BeginLogin(ctx context.Context, target string) (workbuddy.LoginView, error) {
	if b.logins == nil {
		return workbuddy.LoginView{}, ErrNoOfficialEntry
	}
	view, err := b.logins.Begin(ctx, target)
	if errors.Is(err, workbuddy.ErrUnsupportedTarget) {
		return workbuddy.LoginView{}, ErrNoOfficialEntry
	}
	return view, err
}

func (b *realBridge) LoginStatus(ctx context.Context, target string) workbuddy.LoginView {
	if b.logins == nil {
		return workbuddy.LoginView{
			Target:   target,
			Status:   workbuddy.LoginIdle,
			Message:  "无官方登录入口",
			Protocol: workbuddy.ProtocolBaseline,
		}
	}
	return b.logins.Status(ctx, target)
}

func (b *realBridge) CancelLogin(ctx context.Context, target string) (workbuddy.LoginView, error) {
	if b.logins == nil {
		return workbuddy.LoginView{}, ErrNoOfficialEntry
	}
	view, err := b.logins.Cancel(ctx, target)
	if errors.Is(err, workbuddy.ErrUnsupportedTarget) {
		return workbuddy.LoginView{}, ErrNoOfficialEntry
	}
	return view, err
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
