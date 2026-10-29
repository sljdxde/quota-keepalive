package accounts

import (
	"context"
	"testing"

	"qka/internal/store"
)

func newService(t *testing.T) *Service {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return NewService(st.DB())
}

func discovered() []DiscoveredAccount {
	return []DiscoveredAccount{
		{
			ID: "q_1", DisplayName: "Qoder One", MaskedIdentity: "****4321",
			SourceID: "qs_1", SourceKind: KindManaged,
		},
		{
			ID: "q_2", DisplayName: "Qoder Two", MaskedIdentity: "****8765",
			SourceID: "qs_2", SourceKind: KindManaged,
		},
	}
}

func TestDiscoverUpsertsAccountsAndSources(t *testing.T) {
	svc := newService(t)
	if err := svc.Discover(context.Background(), "qoder-cn", discovered()); err != nil {
		t.Fatal(err)
	}

	list, err := svc.List(context.Background(), "qoder-cn")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("应发现 2 个账号，实际 %d", len(list))
	}
	first := list[0]
	if first.MaskedIdentity != "****4321" || len(first.Sources) != 1 {
		t.Errorf("账号/来源字段错误: %+v", first)
	}
	if first.Sources[0].Kind != KindManaged {
		t.Errorf("来源类型错误")
	}

	// 再次发现应幂等（不重复新增）。
	if err := svc.Discover(context.Background(), "qoder-cn", discovered()); err != nil {
		t.Fatal(err)
	}
	list2, _ := svc.List(context.Background(), "qoder-cn")
	if len(list2) != 2 {
		t.Errorf("重复发现应幂等，实际 %d 个", len(list2))
	}
}

func TestDiscoverDefaultsNewAccountsDisabledAndPreservesExistingState(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()
	if err := svc.Discover(ctx, "qoder-cn", discovered()); err != nil {
		t.Fatal(err)
	}

	list, err := svc.List(ctx, "qoder-cn")
	if err != nil {
		t.Fatal(err)
	}
	for _, acc := range list {
		if acc.Enabled {
			t.Fatalf("首次发现账号默认应停用，实际 %+v", acc)
		}
	}

	if err := svc.SetEnabled(ctx, "qoder-cn", "q_1", true); err != nil {
		t.Fatal(err)
	}
	if err := svc.PinSource(ctx, "qoder-cn", "q_1", "qs_1", true); err != nil {
		t.Fatal(err)
	}
	if err := svc.MarkSourceFailure(ctx, "qoder-cn", "q_1", "qs_1"); err != nil {
		t.Fatal(err)
	}

	if err := svc.Discover(ctx, "qoder-cn", discovered()); err != nil {
		t.Fatal(err)
	}
	acc, err := svc.Get(ctx, "qoder-cn", "q_1")
	if err != nil {
		t.Fatal(err)
	}
	if !acc.Enabled {
		t.Error("重复发现不得改写已有账号的启用状态")
	}
	if len(acc.Sources) != 1 || !acc.Sources[0].Pinned {
		t.Errorf("重复发现不得改写已有来源固定状态: %+v", acc.Sources)
	}
	if acc.Sources[0].Health != HealthHealthy {
		t.Errorf("重复验证成功应把同一来源恢复为健康，实际 %s", acc.Sources[0].Health)
	}
}

func TestImportSourceMarkedReadOnly(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()
	if err := svc.Discover(ctx, "qoder-cn", discovered()[:1]); err != nil {
		t.Fatal(err)
	}
	if err := svc.ImportSource(ctx, "qoder-cn", "q_1", "imp_1"); err != nil {
		t.Fatal(err)
	}

	acc, _ := svc.Get(ctx, "qoder-cn", "q_1")
	if len(acc.Sources) != 2 {
		t.Fatalf("应含 2 个来源，实际 %d", len(acc.Sources))
	}
	var imp *Source
	for i := range acc.Sources {
		if acc.Sources[i].ID == "imp_1" {
			imp = &acc.Sources[i]
		}
	}
	if imp == nil {
		t.Fatal("导入来源未创建")
	}
	if imp.Kind != KindImported || !imp.ReadOnly {
		t.Errorf("导入来源应 imported/read_only，实际 %+v", imp)
	}
}

func TestEstablishManagedSourceAfterVerified(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()
	if err := svc.Discover(ctx, "qoder-cn", discovered()[:1]); err != nil {
		t.Fatal(err)
	}
	// 官方登录 verified 后建立受管来源。
	if err := svc.EstablishManagedSource(ctx, "qoder-cn", "q_1", "login_1"); err != nil {
		t.Fatal(err)
	}
	acc, _ := svc.Get(ctx, "qoder-cn", "q_1")
	var found bool
	for _, s := range acc.Sources {
		if s.ID == "login_1" && s.Kind == KindManaged && !s.ReadOnly {
			found = true
		}
	}
	if !found {
		t.Error("verified 后应建立受管来源")
	}
}

func TestPreferredSourceOrder(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()
	if err := svc.Discover(ctx, "qoder-cn", discovered()[:1]); err != nil {
		t.Fatal(err)
	}
	if err := svc.ImportSource(ctx, "qoder-cn", "q_1", "imp_1"); err != nil {
		t.Fatal(err)
	}

	// 默认：健康受管 qs_1 优先于健康导入 imp_1。
	pref, err := svc.PreferredSource(ctx, "qoder-cn", "q_1")
	if err != nil {
		t.Fatal(err)
	}
	if pref.ID != "qs_1" {
		t.Errorf("默认应选健康受管，实际 %s", pref.ID)
	}

	// 固定导入来源后，固定优先。
	if err := svc.PinSource(ctx, "qoder-cn", "q_1", "imp_1", true); err != nil {
		t.Fatal(err)
	}
	pref, _ = svc.PreferredSource(ctx, "qoder-cn", "q_1")
	if pref.ID != "imp_1" {
		t.Errorf("固定来源应优先，实际 %s", pref.ID)
	}
}

func TestSetEnabled(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()
	_ = svc.Discover(ctx, "qoder-cn", discovered()[:1])

	if err := svc.SetEnabled(ctx, "qoder-cn", "q_1", false); err != nil {
		t.Fatal(err)
	}
	acc, _ := svc.Get(ctx, "qoder-cn", "q_1")
	if acc.Enabled {
		t.Error("应已停用")
	}
}

func TestDeleteRemovesMappingOnly(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()
	_ = svc.Discover(ctx, "qoder-cn", discovered())

	if err := svc.Delete(ctx, "qoder-cn", "q_1"); err != nil {
		t.Fatal(err)
	}
	list, _ := svc.List(ctx, "qoder-cn")
	if len(list) != 1 || list[0].ID != "q_2" {
		t.Errorf("应仅删除 q_1，实际 %+v", list)
	}
	// 被删账号的来源级联删除。
	var n int
	_ = svc.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sources WHERE account_id = 'q_1'`).Scan(&n)
	if n != 0 {
		t.Errorf("删除账号应级联删其来源，实际 %d", n)
	}
}

func TestSourceFailureHandling(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()
	_ = svc.Discover(ctx, "qoder-cn", discovered()[:1])
	if err := svc.ImportSource(ctx, "qoder-cn", "q_1", "imp_1"); err != nil {
		t.Fatal(err)
	}

	// 导入来源失败：停用、不回写。
	if err := svc.MarkSourceFailure(ctx, "qoder-cn", "q_1", "imp_1"); err != nil {
		t.Fatal(err)
	}
	acc, _ := svc.Get(ctx, "qoder-cn", "q_1")
	for _, s := range acc.Sources {
		if s.ID == "imp_1" && s.Health != HealthDisabled {
			t.Errorf("导入失败应停用，实际 %s", s.Health)
		}
	}

	// 受管来源失败：转需重登。
	if err := svc.MarkSourceFailure(ctx, "qoder-cn", "q_1", "qs_1"); err != nil {
		t.Fatal(err)
	}
	acc, _ = svc.Get(ctx, "qoder-cn", "q_1")
	for _, s := range acc.Sources {
		if s.ID == "qs_1" && s.Health != HealthReauthRequired {
			t.Errorf("受管失败应转需重登，实际 %s", s.Health)
		}
	}
}

func TestFailureIsolatedPerAccount(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()
	_ = svc.Discover(ctx, "qoder-cn", discovered())

	// q_1 受管来源失败，不影响 q_2。
	_ = svc.MarkSourceFailure(ctx, "qoder-cn", "q_1", "qs_1")
	acc2, _ := svc.Get(ctx, "qoder-cn", "q_2")
	if acc2.Sources[0].Health != HealthHealthy {
		t.Error("一个账号失败不应波及其他账号")
	}
}
