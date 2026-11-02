package accounts

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestRemoveSourceErrorBranches(t *testing.T) {
	ctx := context.Background()

	t.Run("query error", func(t *testing.T) {
		svc := newService(t)
		if _, err := svc.db.Exec(`DROP TABLE sources`); err != nil {
			t.Fatal(err)
		}
		if err := svc.RemoveSource(ctx, "qoder-cn", "q_1", "qs_1"); err == nil {
			t.Fatal("source lookup error should surface")
		}
	})

	t.Run("begin transaction error", func(t *testing.T) {
		svc := newService(t)
		if err := svc.Discover(ctx, "qoder-cn", discovered()[:1]); err != nil {
			t.Fatal(err)
		}
		beginErr := errors.New("begin failed")
		old := beginRemoveSourceTx
		beginRemoveSourceTx = func(*sql.DB, context.Context, *sql.TxOptions) (*sql.Tx, error) {
			return nil, beginErr
		}
		t.Cleanup(func() { beginRemoveSourceTx = old })
		if err := svc.RemoveSource(ctx, "qoder-cn", "q_1", "qs_1"); !errors.Is(err, beginErr) {
			t.Fatalf("begin error = %v", err)
		}
	})

	t.Run("source delete error", func(t *testing.T) {
		svc := newService(t)
		if err := svc.Discover(ctx, "qoder-cn", discovered()[:1]); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.db.Exec(`
CREATE TRIGGER fail_source_delete BEFORE DELETE ON sources
BEGIN SELECT RAISE(ABORT, 'source delete failed'); END`); err != nil {
			t.Fatal(err)
		}
		if err := svc.RemoveSource(ctx, "qoder-cn", "q_1", "qs_1"); err == nil {
			t.Fatal("source delete error should surface")
		}
	})

	t.Run("account delete error", func(t *testing.T) {
		svc := newService(t)
		if err := svc.Discover(ctx, "qoder-cn", discovered()[:1]); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.db.Exec(`
CREATE TRIGGER fail_account_delete BEFORE DELETE ON accounts
BEGIN SELECT RAISE(ABORT, 'account delete failed'); END`); err != nil {
			t.Fatal(err)
		}
		if err := svc.RemoveSource(ctx, "qoder-cn", "q_1", "qs_1"); err == nil {
			t.Fatal("account delete error should surface")
		}
	})
}
