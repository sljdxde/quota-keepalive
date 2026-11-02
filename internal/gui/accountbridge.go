package gui

import (
	"context"
	"errors"

	"qka/internal/accounts"
	"qka/internal/features"
	"qka/internal/status"
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

// AccountRunner executes one account-operation through the shared app chain.
type AccountRunner interface {
	RunAccount(ctx context.Context, target, accountID, operation string) error
}

// realBridge 是 AccountBridge 的具体实现。
type realBridge struct {
	acct   *accounts.Service
	hosts  HostDiscoverer
	logins LoginManager
	runner AccountRunner
	status func(context.Context) (status.View, error)
	tasks  func(string) []string
}

// NewAccountBridge 组装 AccountBridge。
func NewAccountBridge(
	acct *accounts.Service,
	hosts HostDiscoverer,
	logins LoginManager,
) *realBridge {
	return &realBridge{
		acct:   acct,
		hosts:  hosts,
		logins: logins,
	}
}

// WithRuntime adds today's operation projection and the shared single-account
// runner. It is optional so lifecycle-only tests can use the narrow bridge.
func (b *realBridge) WithRuntime(
	statusFn func(context.Context) (status.View, error),
	runner AccountRunner,
) *realBridge {
	b.status = statusFn
	b.runner = runner
	return b
}

// WithTaskCapabilities adds the target's supported automatic operations so the
// account page can render a stable toggle/button for every task.
func (b *realBridge) WithTaskCapabilities(tasks func(string) []string) *realBridge {
	b.tasks = tasks
	return b
}

func (b *realBridge) List(ctx context.Context) ([]AccountView, error) {
	list, err := b.acct.List(ctx, "workbuddy-cn")
	if err != nil {
		return nil, err
	}
	out := make([]AccountView, 0, len(list))
	var view status.View
	if b.status != nil {
		view, err = b.status(ctx)
		if err != nil {
			return nil, err
		}
	}
	for _, account := range list {
		entry := AccountView{
			Account:         account,
			TaskPreferences: map[string]bool{},
		}
		prefs, err := b.acct.TaskPreferences(ctx, account.Target, account.ID)
		if err != nil {
			return nil, err
		}
		for _, current := range view.Accounts {
			if current.Target == account.Target && current.AccountID == account.ID {
				entry.SourceHealth = current.SourceHealth
				entry.Operations = append([]status.OpStatus{}, current.Operations...)
				break
			}
		}
		operations := []string{}
		if b.tasks != nil {
			for _, operation := range b.tasks(account.Target) {
				if features.OperationAvailable(operation) {
					operations = append(operations, operation)
				}
			}
		}
		for _, op := range entry.Operations {
			if !features.OperationAvailable(op.Operation) {
				continue
			}
			if !containsString(operations, op.Operation) {
				operations = append(operations, op.Operation)
			}
		}
		for operation := range prefs {
			if !features.OperationAvailable(operation) {
				continue
			}
			if !containsString(operations, operation) {
				operations = append(operations, operation)
			}
		}
		entry.TaskOperations = operations
		for _, operation := range operations {
			enabled, ok := prefs[operation]
			entry.TaskPreferences[operation] = !ok || enabled
		}
		out = append(out, entry)
	}
	return out, nil
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
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

func (b *realBridge) Run(ctx context.Context, target, accountID, operation string) error {
	if b.runner == nil {
		return errors.New("账号执行链未接线")
	}
	return b.runner.RunAccount(ctx, target, accountID, operation)
}

func (b *realBridge) SetTaskEnabled(
	ctx context.Context,
	target, accountID, operation string,
	enabled bool,
) error {
	return b.acct.SetTaskEnabled(ctx, target, accountID, operation, enabled)
}
