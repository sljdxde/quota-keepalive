package gui

import (
	"context"
	"testing"

	"qka/internal/accounts"
	"qka/internal/status"
)

// TestBridgeListAddsTaskPreferenceOnlyOperations 覆盖「操作状态里没有、但账号
// 任务偏好中启用了可用操作」时的补全分支（保持与状态投影一致）。
func TestBridgeListAddsTaskPreferenceOnlyOperations(t *testing.T) {
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
	if err := accountService.SetTaskEnabled(ctx, "workbuddy-cn", "u1", "refresh", true); err != nil {
		t.Fatal(err)
	}

	real := bridge.(*realBridge).WithRuntime(
		func(context.Context) (status.View, error) { return status.View{}, nil },
		&recordingRunner{},
	)
	list, err := real.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("accounts = %+v", list)
	}
	if len(list[0].TaskOperations) != 1 || list[0].TaskOperations[0] != "refresh" {
		t.Fatalf("任务偏好里的可用操作应补进任务列表: %+v", list[0].TaskOperations)
	}
	if !list[0].TaskPreferences["refresh"] {
		t.Fatalf("任务偏好投影错误: %+v", list[0].TaskPreferences)
	}
}
