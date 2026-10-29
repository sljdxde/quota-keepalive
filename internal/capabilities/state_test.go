package capabilities

import (
	"context"
	"errors"
	"io/fs"
	"testing"
)

func stateGrant() Grant {
	return GrantFromNeeds([]string{"state.get", "state.set"})
}

func TestStateSetGetWithinNamespace(t *testing.T) {
	svc := NewStateService(t.TempDir(), stateGrant())
	ns := Namespace{Target: "wb", AccountID: "u1", Operation: "checkin"}

	if err := svc.Set(context.Background(), ns, "marker", []byte("v1")); err != nil {
		t.Fatalf("Set 失败: %v", err)
	}
	got, err := svc.Get(context.Background(), ns, "marker")
	if err != nil {
		t.Fatalf("Get 失败: %v", err)
	}
	if string(got) != "v1" {
		t.Errorf("期望 v1，实际 %q", got)
	}
}

func TestStateIsolatedAcrossAccounts(t *testing.T) {
	svc := NewStateService(t.TempDir(), stateGrant())
	ns1 := Namespace{Target: "wb", AccountID: "u1", Operation: "checkin"}
	ns2 := Namespace{Target: "wb", AccountID: "u2", Operation: "checkin"}

	if err := svc.Set(context.Background(), ns1, "marker", []byte("v1")); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Get(context.Background(), ns2, "marker")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("不同账号命名空间应隔离，实际 %v", err)
	}
}

func TestStateIsolatedAcrossOperations(t *testing.T) {
	svc := NewStateService(t.TempDir(), stateGrant())
	ns1 := Namespace{Target: "wb", AccountID: "u1", Operation: "checkin"}
	ns2 := Namespace{Target: "wb", AccountID: "u1", Operation: "keepalive"}

	if err := svc.Set(context.Background(), ns1, "marker", []byte("v1")); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(context.Background(), ns2, "marker"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("不同操作命名空间应隔离，实际 %v", err)
	}
}

func TestStateRejectsPathTraversal(t *testing.T) {
	svc := NewStateService(t.TempDir(), stateGrant())
	ns := Namespace{Target: "wb", AccountID: "u1", Operation: "checkin"}

	if err := svc.Set(context.Background(), ns, "../escape", []byte("x")); !errors.Is(err, ErrUnsafeNamespace) {
		t.Fatalf("路径穿越键应被拒绝，实际 %v", err)
	}
	bad := Namespace{Target: "../wb", AccountID: "u1", Operation: "checkin"}
	if err := svc.Set(context.Background(), bad, "k", []byte("x")); !errors.Is(err, ErrUnsafeNamespace) {
		t.Fatalf("路径穿越命名空间应被拒绝，实际 %v", err)
	}
}

func TestStateRejectedWithoutGrant(t *testing.T) {
	svc := NewStateService(t.TempDir(), GrantFromNeeds(nil))
	ns := Namespace{Target: "wb", AccountID: "u1", Operation: "checkin"}
	if err := svc.Set(context.Background(), ns, "k", []byte("x")); !errors.Is(err, ErrHookNotGranted) {
		t.Fatalf("未授权 state.set 应被拒绝，实际 %v", err)
	}
}
