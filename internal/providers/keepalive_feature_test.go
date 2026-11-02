package providers

import (
	"context"
	"errors"
	"testing"

	"qka/internal/features"
)

// TestKeepaliveStaysGloballyHidden 锁死「保活已全局停用」这一前提：
// 只要 features.KeepaliveAvailable=false，provider 侧就不允许把 keepalive
// 列入执行计划、账号操作列表或公开执行入口。
func TestKeepaliveStaysGloballyHidden(t *testing.T) {
	if features.KeepaliveAvailable {
		t.Fatal("keepalive 已全局停用；若恢复该功能，必须同时重新审计保活外呼的风控")
	}
	provider := ProviderView{ID: "fixture", Checkin: true, Keepalive: true}
	account := AccountView{
		Key:             "a1",
		Enabled:         true,
		TaskPreferences: map[string]bool{"checkin": true, "keepalive": true},
	}
	for _, action := range []string{"keepalive", "all", "checkin"} {
		for _, operation := range providerOperations(provider, account, action) {
			if operation == "keepalive" {
				t.Fatalf("action %q 仍会执行 keepalive", action)
			}
		}
	}

	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.service.RunAllView(ctx, View{}, "keepalive", nil); !errors.Is(err, features.ErrKeepaliveDisabled) {
		t.Fatalf("RunAllView(keepalive) = %v, want ErrKeepaliveDisabled", err)
	}
	if _, err := f.service.RunAll(ctx, "keepalive", nil); !errors.Is(err, features.ErrKeepaliveDisabled) {
		t.Fatalf("RunAll(keepalive) = %v, want ErrKeepaliveDisabled", err)
	}
}

// TestOperationViewsOnlyExposeCheckin 验证账号页只暴露 checkin，保活不会
// 从视图投影里泄露成可执行操作。
func TestOperationViewsOnlyExposeCheckin(t *testing.T) {
	f := newFixture(t)
	ops, err := f.service.operationViews(context.Background(), "fixture", "a1",
		"2026-10-30", map[string]bool{"checkin": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || ops[0].Operation != "checkin" {
		t.Fatalf("operation views = %+v, want only checkin", ops)
	}
}
