package logtail

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync"
	"testing"
	"time"

	"qka/internal/store"
)

var registerRowErrorDriver sync.Once

type rowErrorDriver struct{}

func (rowErrorDriver) Open(string) (driver.Conn, error) { return rowErrorConn{}, nil }

type rowErrorConn struct{}

func (rowErrorConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("prepare") }
func (rowErrorConn) Close() error                        { return nil }
func (rowErrorConn) Begin() (driver.Tx, error)           { return nil, errors.New("begin") }
func (rowErrorConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return &rowErrorRows{}, nil
}

type rowErrorRows struct{ calls int }

func (*rowErrorRows) Columns() []string {
	return []string{"id", "ts", "target", "account_id", "operation", "state", "level", "message"}
}
func (*rowErrorRows) Close() error { return nil }
func (r *rowErrorRows) Next(dest []driver.Value) error {
	r.calls++
	if r.calls == 1 {
		dest[0] = int64(1)
		dest[1] = "2026-10-30T00:00:00Z"
		for i := 2; i < len(dest); i++ {
			dest[i] = ""
		}
		return nil
	}
	return errors.New("rows failed")
}

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return st.DB()
}

func seed(t *testing.T, db *sql.DB, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		ts := time.Now().Add(time.Duration(i) * time.Minute).UTC().Format(time.RFC3339)
		_, err := db.Exec(`INSERT INTO logs (ts,target,account_id,operation,state,level,message)
		 VALUES (?,?,?,?,?,?,?)`, ts, "workbuddy-cn", "u1", "checkin", "signed_confirmed", "info", "ok")
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestTailPagination(t *testing.T) {
	db := openDB(t)
	seed(t, db, 5)

	var got int
	var cursor int64
	for {
		page, err := Tail(context.Background(), db, Filter{}, 2, cursor)
		if err != nil {
			t.Fatal(err)
		}
		got += len(page.Entries)
		cursor = page.NextCursor
		if cursor == 0 {
			break
		}
	}
	if got != 5 {
		t.Errorf("游标分页应遍历全部 5 条，实际 %d", got)
	}
}

func TestTailLimitTooLarge(t *testing.T) {
	db := openDB(t)
	if _, err := Tail(context.Background(), db, Filter{}, MaxLimit+1, 0); err != ErrLimitTooLarge {
		t.Fatalf("超过允许上限应返回 ErrLimitTooLarge，实际 %v", err)
	}
}

func TestTailFilters(t *testing.T) {
	db := openDB(t)
	_, _ = db.Exec(`INSERT INTO logs (ts,target,account_id,operation,state,level,message)
	 VALUES (?,?,?,?,?,?,?)`, time.Now().UTC().Format(time.RFC3339),
		"qoder-cn", "q1", "checkin", "failed_final", "error", "boom")

	page, err := Tail(context.Background(), db, Filter{
		Target: "qoder-cn", AccountID: "q1", Operation: "checkin", State: "failed_final",
	}, DefaultLimit, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 1 || page.Entries[0].Message != "boom" {
		t.Fatalf("四类筛选应返回 1 条，实际 %+v", page.Entries)
	}
}

func TestTailHidesKeepaliveWhileDisabled(t *testing.T) {
	db := openDB(t)
	now := time.Now().UTC().Format(time.RFC3339)
	for _, operation := range []string{"checkin", "keepalive"} {
		if _, err := db.Exec(`INSERT INTO logs (ts,target,account_id,operation,state,level,message)
			VALUES (?,?,?,?,?,?,?)`, now, "workbuddy-cn", "u1", operation, "ok", "info", operation); err != nil {
			t.Fatal(err)
		}
	}

	page, err := Tail(context.Background(), db, Filter{}, DefaultLimit, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 1 || page.Entries[0].Operation != "checkin" {
		t.Fatalf("default log view must hide keepalive, got %+v", page.Entries)
	}

	page, err = Tail(context.Background(), db, Filter{Operation: "keepalive"}, DefaultLimit, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 0 {
		t.Fatalf("explicit keepalive filter must stay empty, got %+v", page.Entries)
	}
}

func TestTailDefaultsAndDatabaseFailures(t *testing.T) {
	db := openDB(t)
	page, err := Tail(context.Background(), db, Filter{}, 0, 0)
	if err != nil || page == nil {
		t.Fatalf("默认 limit 应可查询，实际 page=%+v err=%v", page, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Tail(context.Background(), db, Filter{}, 10, 0); err == nil {
		t.Fatal("关闭数据库后查询应失败")
	}
}

func TestTailScanFailure(t *testing.T) {
	db := openDB(t)
	if _, err := db.Exec(`DROP TABLE logs`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE logs (
		id INTEGER PRIMARY KEY, ts TEXT, target TEXT, account_id TEXT,
		operation TEXT, state TEXT, level TEXT, message TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO logs (ts,target,account_id,operation,state,level,message)
		VALUES (?,?,?,?,?,?,?)`, nil, "workbuddy-cn", "u1", "checkin", "ok", "info", "ok"); err != nil {
		t.Fatal(err)
	}
	if _, err := Tail(context.Background(), db, Filter{}, 1, 0); err == nil {
		t.Fatal("字段类型不匹配应触发 Scan 错误")
	}
}

func TestTailRowsErrorFromQueryExecution(t *testing.T) {
	registerRowErrorDriver.Do(func() { sql.Register("logtail-row-error", rowErrorDriver{}) })
	db, err := sql.Open("logtail-row-error", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := Tail(context.Background(), db, Filter{}, 10, 0); err == nil {
		t.Fatal("查询执行中途失败应通过 rows.Err 返回")
	}
}

func TestPruneOldLogs(t *testing.T) {
	db := openDB(t)
	old := time.Now().Add(-49 * time.Hour).UTC().Format(time.RFC3339)
	recent := time.Now().UTC().Format(time.RFC3339)
	_, _ = db.Exec(`INSERT INTO logs (ts,target,account_id,operation,state,level,message)
	 VALUES (?,?,?,?,?,?,?)`, old, "a", "a", "checkin", "ok", "info", "old")
	_, _ = db.Exec(`INSERT INTO logs (ts,target,account_id,operation,state,level,message)
	 VALUES (?,?,?,?,?,?,?)`, recent, "a", "a", "checkin", "ok", "info", "new")

	if err := Prune(context.Background(), db, time.Now()); err != nil {
		t.Fatal(err)
	}
	page, _ := Tail(context.Background(), db, Filter{}, DefaultLimit, 0)
	if len(page.Entries) != 1 || page.Entries[0].Message != "new" {
		t.Fatalf("应只保留 48 小时内日志，实际 %+v", page.Entries)
	}
}
