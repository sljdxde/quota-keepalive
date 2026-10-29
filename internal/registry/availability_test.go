package registry

import (
	"context"
	"testing"

	"qka/internal/store"
)

func TestUnavailableReasons(t *testing.T) {
	cases := []struct {
		name   string
		la     LoadedAdapter
		setup  func(t *testing.T, st *store.Store)
		reason UnavailableReason
	}{
		{
			name:   "验签失败",
			la:     LoadedAdapter{Manifest: sampleManifest(), SignatureInvalid: true},
			reason: ReasonSignatureInvalid,
		},
		{
			name:   "平台不支持",
			la:     LoadedAdapter{Manifest: sampleManifest(), PlatformUnsupported: true},
			reason: ReasonPlatformUnsupported,
		},
		{
			name:   "适配器停用",
			la:     LoadedAdapter{Manifest: sampleManifest(), Disabled: true},
			reason: ReasonAdapterDisabled,
		},
		{
			name: "需重登",
			la:   LoadedAdapter{Manifest: sampleManifest()},
			setup: func(t *testing.T, st *store.Store) {
				seedAccount(t, st, "workbuddy-cn", "u_1", true)
				seedSource(t, st, "workbuddy-cn", "u_1", "s_1", "managed", false, "reauth_required")
			},
			reason: ReasonReauthRequired,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg, st := newRegistry(t)
			if tc.setup != nil {
				tc.setup(t, st)
			}
			reg.SetAdapters([]LoadedAdapter{tc.la})
			list, err := reg.List(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if list[0].Available {
				t.Errorf("不应 available")
			}
			if list[0].UnavailableReason != tc.reason {
				t.Errorf("期望 %s，实际 %s", tc.reason, list[0].UnavailableReason)
			}
		})
	}
}

func TestDisabledAccountFallsBackToNotSignedIn(t *testing.T) {
	reg, st := newRegistry(t)
	seedAccount(t, st, "workbuddy-cn", "u_1", false)
	seedSource(t, st, "workbuddy-cn", "u_1", "s_1", "managed", false, "healthy")

	reg.SetAdapters([]LoadedAdapter{{Manifest: sampleManifest()}})
	list, _ := reg.List(context.Background())
	if list[0].Available {
		t.Errorf("账号停用不应 available")
	}
	if list[0].UnavailableReason != ReasonNotSignedIn {
		t.Errorf("期望 not_signed_in，实际 %s", list[0].UnavailableReason)
	}
}

func TestHotReloadRefreshesView(t *testing.T) {
	reg, st := newRegistry(t)
	seedAccount(t, st, "workbuddy-cn", "u_1", true)
	seedSource(t, st, "workbuddy-cn", "u_1", "s_1", "managed", false, "healthy")

	reg.SetAdapters([]LoadedAdapter{{Manifest: sampleManifest()}})
	list, _ := reg.List(context.Background())
	if list[0].Version != "1.0.0" {
		t.Fatalf("初始版本错误: %s", list[0].Version)
	}

	updated := sampleManifest()
	updated.Version = "1.1.0"
	reg.SetAdapters([]LoadedAdapter{{Manifest: updated}})
	list, _ = reg.List(context.Background())
	if list[0].Version != "1.1.0" {
		t.Errorf("热更新后版本应为 1.1.0，实际 %s", list[0].Version)
	}
}

func TestLastInvocationReflected(t *testing.T) {
	reg, st := newRegistry(t)
	seedAccount(t, st, "workbuddy-cn", "u_1", true)
	seedSource(t, st, "workbuddy-cn", "u_1", "s_1", "managed", false, "healthy")

	if _, err := st.DB().Exec(
		`INSERT INTO logs (ts, target, account_id, operation, level, message)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		"2026-10-29T09:30:00Z", "workbuddy-cn", "u_1", "checkin", "info", "ok"); err != nil {
		t.Fatal(err)
	}

	reg.SetAdapters([]LoadedAdapter{{Manifest: sampleManifest()}})
	list, _ := reg.List(context.Background())
	if list[0].LastInvocationAt != "2026-10-29T09:30:00Z" {
		t.Errorf("lastInvocationAt 应为最近调用时间，实际 %q", list[0].LastInvocationAt)
	}
}
