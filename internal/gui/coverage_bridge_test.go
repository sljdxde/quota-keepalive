package gui

import (
	"context"
	"errors"
	"testing"

	"qka/internal/accounts"
	"qka/internal/status"
	"qka/internal/workbuddy"
)

type coverageHosts struct {
	found []accounts.DiscoveredAccount
	err   error
}

func (h coverageHosts) Discover(context.Context, string) ([]accounts.DiscoveredAccount, error) {
	return h.found, h.err
}

type coverageLoginManager struct {
	beginErr  error
	cancelErr error
}

func (l coverageLoginManager) Begin(context.Context, string) (workbuddy.LoginView, error) {
	return workbuddy.LoginView{Status: workbuddy.LoginPending}, l.beginErr
}

func (l coverageLoginManager) Status(context.Context, string) workbuddy.LoginView {
	return workbuddy.LoginView{Status: workbuddy.LoginPending}
}

func (l coverageLoginManager) Cancel(context.Context, string) (workbuddy.LoginView, error) {
	return workbuddy.LoginView{Status: workbuddy.LoginCancelled}, l.cancelErr
}

func TestContainsString(t *testing.T) {
	if !containsString([]string{"a", "b"}, "b") {
		t.Fatal("existing value should be found")
	}
	if containsString([]string{"a", "b"}, "c") {
		t.Fatal("missing value should not be found")
	}
}

func TestBridgeCoverageErrorPaths(t *testing.T) {
	boom := errors.New("boom")
	discovered := accounts.DiscoveredAccount{
		ID: "u1", DisplayName: "User", MaskedIdentity: "****",
		SourceID: "s1", SourceKind: accounts.KindManaged,
	}

	hostErrorBridge, _ := newTestBridge(t, coverageHosts{err: boom}, nil)
	if err := hostErrorBridge.Discover(context.Background(), "workbuddy-cn"); !errors.Is(err, boom) {
		t.Fatalf("host discovery error returned %v", err)
	}

	accountErrorBridge, st := newTestBridge(t, coverageHosts{}, nil)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if err := accountErrorBridge.Discover(context.Background(), "workbuddy-cn"); err == nil {
		t.Fatal("closed account database should report discovery error")
	}
	if _, err := accountErrorBridge.List(context.Background()); err == nil {
		t.Fatal("closed account database should report list error")
	}

	statusBridge, _ := newTestBridge(t, coverageHosts{found: []accounts.DiscoveredAccount{discovered}}, nil)
	if err := statusBridge.Discover(context.Background(), "workbuddy-cn"); err != nil {
		t.Fatal(err)
	}
	real := statusBridge.(*realBridge)
	real.WithRuntime(func(context.Context) (status.View, error) {
		return status.View{}, boom
	}, nil)
	if _, err := statusBridge.List(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("runtime status error returned %v", err)
	}

	prefBridge, prefStore := newTestBridge(t,
		coverageHosts{found: []accounts.DiscoveredAccount{discovered}}, nil)
	if err := prefBridge.Discover(context.Background(), "workbuddy-cn"); err != nil {
		t.Fatal(err)
	}
	if _, err := prefStore.DB().Exec(`DROP TABLE account_task_preferences`); err != nil {
		t.Fatal(err)
	}
	if _, err := prefStore.DB().Exec(
		`CREATE VIEW account_task_preferences AS SELECT operation, enabled FROM missing_preferences`,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := prefBridge.List(context.Background()); err == nil {
		t.Fatal("task preference query error should be returned")
	}
}

func TestBridgeCoverageLoginAndMutations(t *testing.T) {
	unsupported := workbuddy.ErrUnsupportedTarget
	bridge, _ := newTestBridge(t, coverageHosts{}, coverageLoginManager{
		beginErr: unsupported, cancelErr: unsupported,
	})
	if _, err := bridge.BeginLogin(context.Background(), "nope"); !errors.Is(err, ErrNoOfficialEntry) {
		t.Fatalf("unsupported begin login returned %v", err)
	}
	if _, err := bridge.CancelLogin(context.Background(), "nope"); !errors.Is(err, ErrNoOfficialEntry) {
		t.Fatalf("unsupported cancel login returned %v", err)
	}

	noLoginBridge, _ := newTestBridge(t, coverageHosts{}, nil)
	if got := noLoginBridge.LoginStatus(context.Background(), "workbuddy-cn"); got.Message != "无官方登录入口" {
		t.Fatalf("missing login manager status = %+v", got)
	}
	if _, err := noLoginBridge.CancelLogin(context.Background(), "workbuddy-cn"); !errors.Is(err, ErrNoOfficialEntry) {
		t.Fatalf("missing login manager cancel returned %v", err)
	}

	real := noLoginBridge.(*realBridge)
	if err := real.Run(context.Background(), "workbuddy-cn", "u1", "checkin"); err == nil {
		t.Fatal("Run without a runner should fail")
	}
	runner := &recordingRunner{}
	real.WithRuntime(nil, runner)
	if err := real.Run(context.Background(), "workbuddy-cn", "u1", "keepalive"); err != nil {
		t.Fatal(err)
	}
	if runner.calls != 1 || runner.operation != "keepalive" {
		t.Fatalf("runner was not called correctly: %+v", runner)
	}
}

func TestBridgeCoverageAccountMutations(t *testing.T) {
	hosts := coverageHosts{found: []accounts.DiscoveredAccount{{
		ID: "u1", DisplayName: "User", MaskedIdentity: "****",
		SourceID: "s1", SourceKind: accounts.KindManaged,
	}}}
	bridge, _ := newTestBridge(t, hosts, nil)
	ctx := context.Background()
	if err := bridge.Discover(ctx, "workbuddy-cn"); err != nil {
		t.Fatal(err)
	}

	if err := bridge.ImportSource(ctx, "workbuddy-cn", "u1", "external"); err != nil {
		t.Fatal(err)
	}
	if err := bridge.PinSource(ctx, "workbuddy-cn", "u1", "external", true); err != nil {
		t.Fatal(err)
	}
	if err := bridge.SetTaskEnabled(ctx, "workbuddy-cn", "u1", "checkin", false); err != nil {
		t.Fatal(err)
	}
	if err := bridge.SetEnabled(ctx, "workbuddy-cn", "u1", true); err != nil {
		t.Fatal(err)
	}
}

func TestBridgeCoverageMergesMissingOperations(t *testing.T) {
	hosts := coverageHosts{found: []accounts.DiscoveredAccount{{
		ID: "u1", DisplayName: "User", MaskedIdentity: "****",
		SourceID: "s1", SourceKind: accounts.KindManaged,
	}}}
	bridge, _ := newTestBridge(t, hosts, nil)
	ctx := context.Background()
	if err := bridge.Discover(ctx, "workbuddy-cn"); err != nil {
		t.Fatal(err)
	}
	real := bridge.(*realBridge)
	real.WithRuntime(func(context.Context) (status.View, error) {
		return status.View{Accounts: []status.AccountStatus{{
			Target: "workbuddy-cn", AccountID: "u1",
			Operations: []status.OpStatus{{Operation: "refresh"}},
		}}}, nil
	}, nil).WithTaskCapabilities(func(string) []string {
		return []string{"checkin"}
	})
	if err := bridge.SetTaskEnabled(ctx, "workbuddy-cn", "u1", "keepalive", false); err != nil {
		t.Fatal(err)
	}
	list, err := bridge.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(list[0].TaskOperations, "refresh") ||
		containsString(list[0].TaskOperations, "keepalive") {
		t.Fatalf("missing view/preference operations were not merged: %+v", list[0].TaskOperations)
	}
}
