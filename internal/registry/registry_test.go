package registry

import (
	"context"
	"testing"
	"time"

	"qka/internal/adapterhost"
	"qka/internal/store"
)

func fixedClock() time.Time {
	return time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
}

func newRegistry(t *testing.T) (*Registry, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("打开 store 失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return New(st.DB(), fixedClock), st
}

func sampleManifest() adapterhost.Manifest {
	return adapterhost.Manifest{
		ID:           "workbuddy-cn",
		Name:         "WorkBuddy CN",
		Version:      "1.0.0",
		Entry:        "index.mjs",
		Capabilities: []string{"checkin", "keepalive"},
		Needs:        []string{"http.outbound", "log.write"},
		Actions: []adapterhost.Action{
			{Op: "checkin", Effects: adapterhost.Effects{Idempotent: ptr(true)}},
			{Op: "keepalive", Effects: adapterhost.Effects{AccessesNetwork: ptr(true)}},
		},
	}
}

func ptr[T any](v T) *T { return &v }

func TestDescriptorForAdapterWithoutAccount(t *testing.T) {
	reg, _ := newRegistry(t)
	reg.SetAdapters([]LoadedAdapter{{Manifest: sampleManifest()}})

	list, err := reg.List(context.Background())
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("期望 1 个自描述，实际 %d", len(list))
	}
	d := list[0]
	if d.ID != "workbuddy-cn" || d.Name != "WorkBuddy CN" || d.Version != "1.0.0" {
		t.Errorf("身份字段错误: %s/%s/%s", d.ID, d.Name, d.Version)
	}
	if d.Available {
		t.Error("无账号来源时不应 available")
	}
	if d.UnavailableReason != ReasonNotSignedIn {
		t.Errorf("期望未登录原因，实际 %q", d.UnavailableReason)
	}
	if d.ScheduleOwner != "host" {
		t.Errorf("scheduleOwner 应固定为 host，实际 %q", d.ScheduleOwner)
	}
	if len(d.Actions) != 2 || d.Actions[0].Op != "checkin" {
		t.Errorf("支持操作错误: %+v", d.Actions)
	}
}

func TestDescriptorAvailableWithHealthySource(t *testing.T) {
	reg, st := newRegistry(t)
	seedAccount(t, st, "workbuddy-cn", "u_1", true)
	seedSource(t, st, "workbuddy-cn", "u_1", "s_1", "managed", false, "healthy")

	reg.SetAdapters([]LoadedAdapter{{Manifest: sampleManifest()}})
	list, err := reg.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	d := list[0]
	if !d.Available {
		t.Errorf("有健康来源应 available，reason=%q", d.UnavailableReason)
	}
	if len(d.Accounts) != 1 || len(d.Accounts[0].Sources) != 1 {
		t.Fatalf("账号/来源视图错误: %+v", d.Accounts)
	}
	if d.Accounts[0].Sources[0].Kind != "managed" {
		t.Errorf("来源类型错误: %+v", d.Accounts[0].Sources[0])
	}
}
