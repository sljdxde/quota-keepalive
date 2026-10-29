package gui

import (
	"context"
	"testing"

	"qka/internal/accounts"
	"qka/internal/store"
	"qka/internal/workbuddy"
)

type fakeDiscoverer struct {
	accounts []accounts.DiscoveredAccount
}

func (f *fakeDiscoverer) Discover(_ context.Context, _ string) ([]accounts.DiscoveredAccount, error) {
	return f.accounts, nil
}

type fakeLoginManager struct {
	view       workbuddy.LoginView
	beginErr   error
	cancelErr  error
	beginCalls int
	cancelled  bool
}

func (f *fakeLoginManager) Begin(_ context.Context, _ string) (workbuddy.LoginView, error) {
	f.beginCalls++
	return f.view, f.beginErr
}

func (f *fakeLoginManager) Status(_ context.Context, _ string) workbuddy.LoginView {
	return f.view
}

func (f *fakeLoginManager) Cancel(_ context.Context, _ string) (workbuddy.LoginView, error) {
	f.cancelled = true
	return f.view, f.cancelErr
}

func newTestBridge(t *testing.T, hosts HostDiscoverer, logins LoginManager) (AccountBridge, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	acct := accounts.NewService(st.DB())
	return NewAccountBridge(acct, hosts, logins), st
}

func TestBridgeDiscoverThenList(t *testing.T) {
	hosts := &fakeDiscoverer{
		accounts: []accounts.DiscoveredAccount{
			{ID: "u1", DisplayName: "User One", MaskedIdentity: "****1234",
				SourceID: "s1", SourceKind: accounts.KindManaged},
		},
	}
	bridge, _ := newTestBridge(t, hosts, &fakeLoginManager{})

	if err := bridge.Discover(context.Background(), "workbuddy-cn"); err != nil {
		t.Fatalf("Discover 失败: %v", err)
	}
	list, err := bridge.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "u1" {
		t.Fatalf("发现后应列出 1 个账号，实际 %+v", list)
	}
}

func TestBridgeBeginLoginReturnsVisibleStateWithoutCreatingAccount(t *testing.T) {
	logins := &fakeLoginManager{
		view: workbuddy.LoginView{
			Target:    "workbuddy-cn",
			Status:    workbuddy.LoginPending,
			Stage:     workbuddy.StageWaitingForBrowser,
			Message:   "请在 WorkBuddy CN 官方页面完成登录",
			Protocol:  workbuddy.ProtocolBaseline,
			CanCancel: true,
		},
	}
	bridge, st := newTestBridge(t, &fakeDiscoverer{}, logins)

	view, err := bridge.BeginLogin(context.Background(), "workbuddy-cn")
	if err != nil {
		t.Fatalf("BeginLogin 失败: %v", err)
	}
	if view.Status != workbuddy.LoginPending || !view.CanCancel {
		t.Errorf("应返回可见的进行中状态，实际 %+v", view)
	}
	if got := bridge.LoginStatus(context.Background(), "workbuddy-cn"); got.Status != workbuddy.LoginPending {
		t.Errorf("状态查询不一致: %+v", got)
	}

	var accountsCount, sourcesCount int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM accounts`).Scan(&accountsCount); err != nil {
		t.Fatal(err)
	}
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM sources`).Scan(&sourcesCount); err != nil {
		t.Fatal(err)
	}
	if accountsCount != 0 || sourcesCount != 0 {
		t.Errorf("登录完成前不得创建账号或来源，accounts=%d sources=%d", accountsCount, sourcesCount)
	}
}

func TestBridgeLoginCancelDelegates(t *testing.T) {
	logins := &fakeLoginManager{
		view: workbuddy.LoginView{
			Target: "workbuddy-cn",
			Status: workbuddy.LoginCancelled,
		},
	}
	bridge, _ := newTestBridge(t, &fakeDiscoverer{}, logins)

	view, err := bridge.CancelLogin(context.Background(), "workbuddy-cn")
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != workbuddy.LoginCancelled || !logins.cancelled {
		t.Errorf("取消未转发到登录服务: view=%+v cancelled=%v", view, logins.cancelled)
	}
}

func TestBridgeLoginUnsupportedTarget(t *testing.T) {
	bridge, _ := newTestBridge(t, &fakeDiscoverer{}, nil)
	if _, err := bridge.BeginLogin(context.Background(), "nope"); err != ErrNoOfficialEntry {
		t.Fatalf("无官方登录服务应返回 ErrNoOfficialEntry，实际 %v", err)
	}
}
