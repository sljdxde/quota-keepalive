package gui

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"qka/internal/accounts"
	"qka/internal/features"
	"qka/internal/status"
	"qka/internal/store"
	"qka/internal/workbuddy"
)

type fakeDiscoverer struct {
	accounts      []accounts.DiscoveredAccount
	discoverCalls int
}

func (f *fakeDiscoverer) Discover(_ context.Context, _ string) ([]accounts.DiscoveredAccount, error) {
	f.discoverCalls++
	return f.accounts, nil
}

type fakeLoginManager struct {
	view       workbuddy.LoginView
	beginErr   error
	cancelErr  error
	beginCalls int
	cancelled  bool
}

type recordingRunner struct {
	target    string
	accountID string
	operation string
	calls     int
}

func (r *recordingRunner) RunAccount(_ context.Context, target, accountID, operation string) error {
	r.target = target
	r.accountID = accountID
	r.operation = operation
	r.calls++
	return nil
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

func TestBridgeListExposesMaskedIdentityAndSourceMetadata(t *testing.T) {
	hosts := &fakeDiscoverer{
		accounts: []accounts.DiscoveredAccount{
			{ID: "u1", DisplayName: "User One", MaskedIdentity: "****1234",
				SourceID: "s1", SourceKind: accounts.KindManaged},
		},
	}
	bridge, _ := newTestBridge(t, hosts, &fakeLoginManager{})
	ctx := context.Background()
	if err := bridge.Discover(ctx, "workbuddy-cn"); err != nil {
		t.Fatal(err)
	}

	list, err := bridge.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	projection := string(raw)
	for _, want := range []string{`"masked_identity":"****1234"`, `"kind":"managed"`, `"health":"healthy"`} {
		if !strings.Contains(projection, want) {
			t.Errorf("账号列表缺少 %s: %s", want, projection)
		}
	}
	for _, secret := range []string{"access_token", "refresh_token", "cookie"} {
		if strings.Contains(projection, secret) {
			t.Errorf("账号列表不得包含 %s: %s", secret, projection)
		}
	}
}

func TestBridgeDeleteDoesNotDispatchToTargetApplication(t *testing.T) {
	hosts := &fakeDiscoverer{
		accounts: []accounts.DiscoveredAccount{
			{ID: "u1", DisplayName: "User One", MaskedIdentity: "****1234",
				SourceID: "s1", SourceKind: accounts.KindManaged},
		},
	}
	bridge, _ := newTestBridge(t, hosts, &fakeLoginManager{})
	ctx := context.Background()
	if err := bridge.Discover(ctx, "workbuddy-cn"); err != nil {
		t.Fatal(err)
	}
	hosts.discoverCalls = 0

	if err := bridge.Delete(ctx, "workbuddy-cn", "u1"); err != nil {
		t.Fatal(err)
	}
	if hosts.discoverCalls != 0 {
		t.Errorf("删除 QKA 映射不得调用目标应用适配器，实际 %d 次", hosts.discoverCalls)
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

func TestBridgeListOnlyReturnsWorkBuddyCNWithTodayState(t *testing.T) {
	hosts := &fakeDiscoverer{
		accounts: []accounts.DiscoveredAccount{
			{ID: "u1", DisplayName: "User One", MaskedIdentity: "****1234",
				SourceID: "s1", SourceKind: accounts.KindManaged},
		},
	}
	bridge, _ := newTestBridge(t, hosts, &fakeLoginManager{})
	ctx := context.Background()
	if err := bridge.Discover(ctx, "workbuddy-cn"); err != nil {
		t.Fatal(err)
	}
	if err := bridge.Discover(ctx, "qoder-cn"); err != nil {
		t.Fatal(err)
	}

	real := bridge.(*realBridge)
	real.WithRuntime(func(context.Context) (status.View, error) {
		return status.View{Accounts: []status.AccountStatus{{
			Target: "workbuddy-cn", AccountID: "u1", SourceHealth: "healthy",
			Operations: []status.OpStatus{{
				Target: "workbuddy-cn", AccountID: "u1", Operation: "checkin",
				State: "manual_required", Message: "自动签到被 WorkBuddy 拒绝",
				NextAction: "请回 WorkBuddy App 签到，完成后再次执行 checkin_run 复查",
			}},
		}}}, nil
	}, nil).WithTaskCapabilities(func(target string) []string {
		if target == "workbuddy-cn" {
			return []string{"checkin", "keepalive"}
		}
		return nil
	})
	if err := bridge.SetTaskEnabled(ctx, "workbuddy-cn", "u1", "keepalive", false); err != nil {
		t.Fatal(err)
	}

	list, err := bridge.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Target != "workbuddy-cn" {
		t.Fatalf("账号页只应返回 WorkBuddy CN 账号，实际 %+v", list)
	}
	if len(list[0].Operations) != 1 ||
		list[0].Operations[0].State != "manual_required" ||
		list[0].Operations[0].NextAction == "" {
		t.Errorf("账号列表应带今日操作状态和下一步，实际 %+v", list[0].Operations)
	}
	if list[0].SourceHealth != "healthy" {
		t.Errorf("账号列表应带来源健康状态，实际 %q", list[0].SourceHealth)
	}
	if !containsString(list[0].TaskOperations, "checkin") {
		t.Fatalf("账号列表应带任务能力，实际 %+v", list[0].TaskOperations)
	}
	if containsString(list[0].TaskOperations, "keepalive") {
		t.Fatalf("保活任务不应暴露，实际 %+v", list[0].TaskOperations)
	}
	if !list[0].TaskPreferences["checkin"] {
		t.Errorf("任务偏好投影错误，实际 %+v", list[0].TaskPreferences)
	}
}

func TestBridgeListFiltersGloballyDisabledTasksAndMergesVisiblePreferences(t *testing.T) {
	hosts := &fakeDiscoverer{
		accounts: []accounts.DiscoveredAccount{{
			ID: "u1", DisplayName: "User One", MaskedIdentity: "****1234",
			SourceID: "s1", SourceKind: accounts.KindManaged,
		}},
	}
	bridge, st := newTestBridge(t, hosts, &fakeLoginManager{})
	ctx := context.Background()
	if err := bridge.Discover(ctx, "workbuddy-cn"); err != nil {
		t.Fatal(err)
	}
	accountService := accounts.NewService(st.DB())
	for _, operation := range []string{"refresh", "keepalive"} {
		if err := accountService.SetTaskEnabled(ctx, "workbuddy-cn", "u1", operation, true); err != nil {
			t.Fatal(err)
		}
	}
	real := bridge.(*realBridge).
		WithRuntime(func(context.Context) (status.View, error) {
			return status.View{Accounts: []status.AccountStatus{{
				Target: "workbuddy-cn", AccountID: "u1",
				Operations: []status.OpStatus{
					{Operation: "keepalive"},
					{Operation: "refresh"},
				},
			}}}, nil
		}, &recordingRunner{}).
		WithTaskCapabilities(func(string) []string {
			return []string{"checkin", "keepalive"}
		})

	list, err := real.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("accounts = %+v", list)
	}
	got := []string{}
	for _, operation := range list[0].TaskOperations {
		if operation == "keepalive" {
			t.Fatal("globally disabled keepalive leaked into task operations")
		}
		got = append(got, operation)
	}
	for _, want := range []string{"checkin", "refresh"} {
		found := false
		for _, operation := range got {
			if operation == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("visible task %q missing from %v", want, got)
		}
	}
}

func TestBoundAccountsRunRejectsUnsupportedTargetAndOperation(t *testing.T) {
	runner := &recordingRunner{}
	accts := &loginBoundAccounts{runner: runner}
	svc := NewBoundService(Deps{Accounts: accts})

	for _, tc := range []struct {
		target    string
		operation string
	}{
		{"workbuddy-cn", "unsupported"},
		{"workbuddy-cn", ""},
	} {
		if err := svc.AccountsRun(tc.target, "u1", tc.operation); !errors.Is(err, errUnsupportedAccountAction) {
			t.Errorf("不支持的账号动作应被拒绝: target=%s operation=%s err=%v",
				tc.target, tc.operation, err)
		}
	}
	if runner.calls != 0 {
		t.Fatalf("被拒绝的动作不得进入执行链，实际调用 %d 次", runner.calls)
	}
	if err := svc.AccountsRun("workbuddy-cn", "u1", "keepalive"); !errors.Is(err, features.ErrKeepaliveDisabled) {
		t.Fatalf("保活应被全局拒绝，实际 %v", err)
	}

	if err := svc.AccountsRun("workbuddy-cn", "u1", "checkin"); err != nil {
		t.Fatal(err)
	}
	if runner.calls != 1 || runner.target != "workbuddy-cn" ||
		runner.accountID != "u1" || runner.operation != "checkin" {
		t.Errorf("支持的账号动作应进入执行链，实际 %+v", runner)
	}
	if err := svc.AccountsRun("workbuddy-cn", "u1", "refresh"); err != nil {
		t.Fatal(err)
	}
	if runner.calls != 2 || runner.operation != "refresh" {
		t.Errorf("只读额度刷新应进入执行链，实际 %+v", runner)
	}
}
