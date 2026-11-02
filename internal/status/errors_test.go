package status

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync"
	"testing"
	"time"

	"qka/internal/accounts"
	"qka/internal/engine"
	"qka/internal/store"
)

var registerStatusRowError sync.Once

type statusRowErrorDriver struct{}

func (statusRowErrorDriver) Open(string) (driver.Conn, error) { return statusRowErrorConn{}, nil }

type statusRowErrorConn struct{}

func (statusRowErrorConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare")
}
func (statusRowErrorConn) Close() error              { return nil }
func (statusRowErrorConn) Begin() (driver.Tx, error) { return nil, errors.New("begin") }
func (statusRowErrorConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return &statusRowErrorRows{}, nil
}

type statusRowErrorRows struct{ calls int }

func (*statusRowErrorRows) Columns() []string {
	return []string{"health", "pinned", "kind", "read_only"}
}
func (*statusRowErrorRows) Close() error { return nil }
func (r *statusRowErrorRows) Next(dest []driver.Value) error {
	r.calls++
	if r.calls == 1 {
		dest[0], dest[1], dest[2], dest[3] = "healthy", int64(0), "managed", int64(0)
		return nil
	}
	return errors.New("rows failed")
}

func TestNewServiceDefaultsCapabilities(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := NewService(st.DB(), func() time.Time { return fixedNow() }, nil)
	if got := svc.caps("anything"); len(got) != 1 || got[0] != "checkin" {
		t.Fatalf("默认能力应只有 checkin，实际 %v", got)
	}
}

func TestStatusPropagatesReadErrors(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		mutate func(*store.Store)
		call   func(*Service) error
	}{
		{"accounts query", func(st *store.Store) { _, _ = st.DB().Exec(`DROP TABLE accounts`) },
			func(s *Service) error { _, err := s.Status(ctx); return err }},
		{"sources query", func(st *store.Store) { _, _ = st.DB().Exec(`DROP TABLE sources`) },
			func(s *Service) error { _, err := s.Status(ctx); return err }},
		{"preferences query", func(st *store.Store) { _, _ = st.DB().Exec(`DROP TABLE account_task_preferences`) },
			func(s *Service) error { _, err := s.Status(ctx); return err }},
		{"daily ops query", func(st *store.Store) { _, _ = st.DB().Exec(`DROP TABLE daily_ops`) },
			func(s *Service) error { _, err := s.Status(ctx); return err }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, svc := setup(t)
			tc.mutate(st)
			if err := tc.call(svc); err == nil {
				t.Fatal("数据库读取错误应返回")
			}
		})
	}
}

func TestStatusPropagatesScanErrors(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		mutate func(*store.Store)
	}{
		{"accounts", func(st *store.Store) {
			_, _ = st.DB().Exec(`DROP TABLE accounts`)
			_, _ = st.DB().Exec(`CREATE TABLE accounts (target TEXT,id TEXT,display_name TEXT,masked_identity TEXT,enabled INTEGER)`)
			_, _ = st.DB().Exec(`INSERT INTO accounts VALUES (NULL,'x','x','x',1)`)
		}},
		{"sources", func(st *store.Store) {
			_, _ = st.DB().Exec(`DROP TABLE sources`)
			_, _ = st.DB().Exec(`CREATE TABLE sources (health TEXT,pinned INTEGER,kind TEXT,read_only INTEGER,target TEXT,account_id TEXT,id TEXT)`)
			_, _ = st.DB().Exec(`INSERT INTO sources VALUES (NULL,0,'managed',0,'qoder-cn','q_1','s1')`)
		}},
		{"preferences", func(st *store.Store) {
			_, _ = st.DB().Exec(`DROP TABLE account_task_preferences`)
			_, _ = st.DB().Exec(`CREATE TABLE account_task_preferences (operation TEXT,enabled INTEGER,target TEXT,account_id TEXT)`)
			_, _ = st.DB().Exec(`INSERT INTO account_task_preferences VALUES (NULL,1,'qoder-cn','q_1')`)
		}},
		{"daily ops", func(st *store.Store) {
			_, _ = st.DB().Exec(`DROP TABLE daily_ops`)
			_, _ = st.DB().Exec(`CREATE TABLE daily_ops (operation TEXT,state TEXT,credits REAL,balance REAL,credits_known INTEGER,balance_known INTEGER,target TEXT,account_id TEXT,day_key TEXT)`)
			day := engine.BeijingDay(fixedNow())
			_, _ = st.DB().Exec(`INSERT INTO daily_ops VALUES (NULL,'x',0,0,0,0,'qoder-cn','q_1',?)`, day)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, svc := setup(t)
			tc.mutate(st)
			if _, err := svc.Status(ctx); err == nil {
				t.Fatal("数据库 Scan 错误应返回")
			}
		})
	}
}

func TestPreferredSourceSelectsImportedAndDisabled(t *testing.T) {
	st, svc := setup(t)
	ctx := context.Background()
	if _, err := st.DB().Exec(`UPDATE sources SET health='reauth_required' WHERE target='qoder-cn' AND account_id='q_1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`INSERT INTO sources
		(id,target,account_id,kind,read_only,health,pinned,created_at,updated_at)
		VALUES ('imp','qoder-cn','q_1','imported',1,'healthy',0,'t','t')`); err != nil {
		t.Fatal(err)
	}
	got, err := svc.preferredSource(ctx, "qoder-cn", "q_1")
	if err != nil || got.health != "healthy" || got.reviewable {
		t.Fatalf("健康导入来源应被选中且不可复查，实际 %+v %v", got, err)
	}
	if _, err := st.DB().Exec(`DELETE FROM sources WHERE target='qoder-cn' AND account_id='q_2'`); err != nil {
		t.Fatal(err)
	}
	got, err = svc.preferredSource(ctx, "qoder-cn", "q_2")
	if err != nil || got.health != "disabled" {
		t.Fatalf("无来源应为 disabled，实际 %+v %v", got, err)
	}
}

func TestStatusManualFallbackWithoutSource(t *testing.T) {
	st, svc := setupWorkBuddyManualFallback(t)
	if _, err := st.DB().Exec(`DELETE FROM sources`); err != nil {
		t.Fatal(err)
	}
	view, err := svc.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, account := range view.Accounts {
		if account.AccountID == "wb_1" {
			op := account.Operations[0]
			if op.NextAction == "" || account.SourceHealth != "disabled" {
				t.Fatalf("无来源时兜底应给出下一步，实际 %+v %+v", account, op)
			}
			return
		}
	}
	t.Fatal("缺少 wb_1")
}

func TestStatusSkipsDisabledAccounts(t *testing.T) {
	st, svc := setup(t)
	acc := accounts.NewService(st.DB())
	if err := acc.SetEnabled(context.Background(), "qoder-cn", "q_1", false); err != nil {
		t.Fatal(err)
	}
	view, err := svc.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, account := range view.Accounts {
		if account.AccountID == "q_1" {
			t.Fatal("停用账号不应出现在状态中")
		}
	}
}

func TestPlanValidateErrorAndConflictBranches(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		mutate func(*store.Store)
	}{
		{"accounts query", func(st *store.Store) { _, _ = st.DB().Exec(`DROP TABLE accounts`) }},
		{"sources query", func(st *store.Store) { _, _ = st.DB().Exec(`DROP TABLE sources`) }},
		{"preferences query", func(st *store.Store) { _, _ = st.DB().Exec(`DROP TABLE account_task_preferences`) }},
		{"daily op query", func(st *store.Store) { _, _ = st.DB().Exec(`DROP TABLE daily_ops`) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, svc := setup(t)
			tc.mutate(st)
			if _, err := svc.PlanValidate(ctx); err == nil {
				t.Fatal("计划预演读取错误应返回")
			}
		})
	}

	st, svc := setup(t)
	seedOp(t, st, "q_1", "checkin", "manual_required")
	plan, err := svc.PlanValidate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range plan.Items {
		if item.AccountID == "q_1" && item.Operation == "checkin" {
			if !contains(item.Conflicts, "同日已终态") {
				t.Fatalf("非 WorkBuddy manual_required 应终态，实际 %+v", item)
			}
			break
		}
	}

	st, svc = setup(t)
	if _, err := st.DB().Exec(`DELETE FROM sources`); err != nil {
		t.Fatal(err)
	}
	plan, err = svc.PlanValidate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range plan.Items {
		if item.AccountID == "q_2" && !contains(item.Conflicts, "无可用来源") {
			t.Fatalf("无来源应报告冲突，实际 %+v", item)
		}
	}
}

func TestPlanValidateMarksActiveRunningLease(t *testing.T) {
	st, svc := setup(t)
	day := engine.BeijingDay(fixedNow())
	leaseExpires := fixedNow().Add(5 * time.Minute).Format(time.RFC3339)
	if _, err := st.DB().ExecContext(context.Background(), `
INSERT INTO daily_ops
  (target, account_id, operation, day_key, state, lease_expires_at, updated_at)
VALUES ('qoder-cn', 'q_1', 'checkin', ?, 'running', ?, ?)`,
		day, leaseExpires, fixedNow().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}

	plan, err := svc.PlanValidate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range plan.Items {
		if item.AccountID == "q_1" && item.Operation == "checkin" {
			if !contains(item.Conflicts, "执行中，租约仍有效") {
				t.Fatalf("active lease should conflict: %+v", item)
			}
			return
		}
	}
	t.Fatal("missing q_1/checkin plan item")
}

func TestPreviewTransitionBranches(t *testing.T) {
	noon := time.Date(2026, 10, 29, 10, 0, 0, 0, engine.Beijing)
	afternoon := time.Date(2026, 10, 29, 14, 0, 0, 0, engine.Beijing)
	if got := previewTransition("checkin", noon, false); got != "no_change" {
		t.Fatalf("不可执行应为 no_change，实际 %s", got)
	}
	if got := previewTransition("keepalive", noon, true); got != "completed" {
		t.Fatalf("保活应为 completed，实际 %s", got)
	}
	if got := previewTransition("checkin", noon, true); got != "signed_provisional" {
		t.Fatalf("午前应为 provisional，实际 %s", got)
	}
	if got := previewTransition("checkin", afternoon, true); got != "signed_confirmed" {
		t.Fatalf("午后应为 confirmed，实际 %s", got)
	}
}

func TestPreferredSourceRowsError(t *testing.T) {
	registerStatusRowError.Do(func() { sql.Register("status-row-error", statusRowErrorDriver{}) })
	db, err := sql.Open("status-row-error", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	svc := NewService(db, func() time.Time { return fixedNow() }, nil)
	if _, err := svc.preferredSource(context.Background(), "qoder-cn", "q_1"); err == nil {
		t.Fatal("来源遍历错误应通过 rows.Err 返回")
	}
}
