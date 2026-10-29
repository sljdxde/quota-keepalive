package capabilities

import (
	"context"
	"errors"
	"testing"

	"qka/internal/store"
)

func newLogService(t *testing.T, grant Grant) (*LogService, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("打开 store 失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return NewLogService(st.DB(), grant, fixedNow), st
}

func TestLogWriteWithinNamespace(t *testing.T) {
	svc, st := newLogService(t, GrantFromNeeds([]string{"log.write"}))
	ns := Namespace{Target: "wb", AccountID: "u1", Operation: "checkin"}

	if err := svc.Write(context.Background(), ns, "info", "done"); err != nil {
		t.Fatalf("Write 失败: %v", err)
	}

	var target, account, op, msg string
	err := st.DB().QueryRowContext(context.Background(),
		`SELECT target, account_id, operation, message FROM logs WHERE target = ? AND account_id = ?`,
		"wb", "u1").
		Scan(&target, &account, &op, &msg)
	if err != nil {
		t.Fatalf("未查到日志: %v", err)
	}
	if target != "wb" || account != "u1" || op != "checkin" || msg != "done" {
		t.Errorf("日志命名空间/内容错误: %s/%s/%s/%s", target, account, op, msg)
	}
}

func TestLogRejectedWithoutGrant(t *testing.T) {
	svc, _ := newLogService(t, GrantFromNeeds(nil))
	ns := Namespace{Target: "wb", AccountID: "u1", Operation: "checkin"}
	if err := svc.Write(context.Background(), ns, "info", "x"); !errors.Is(err, ErrHookNotGranted) {
		t.Fatalf("未授权 log.write 应被拒绝，实际 %v", err)
	}
}

func TestLogRejectsUnsafeNamespace(t *testing.T) {
	svc, _ := newLogService(t, GrantFromNeeds([]string{"log.write"}))
	ns := Namespace{Target: "wb", AccountID: "u1", Operation: "../x"}
	if err := svc.Write(context.Background(), ns, "info", "x"); !errors.Is(err, ErrUnsafeNamespace) {
		t.Fatalf("非法命名空间应被拒绝，实际 %v", err)
	}
}
