package logtail

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"qka/internal/store"
)

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

func TestPruneOldLogs(t *testing.T) {
	db := openDB(t)
	old := time.Now().Add(-31 * 24 * time.Hour).UTC().Format(time.RFC3339)
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
		t.Fatalf("应只保留 30 天内日志，实际 %+v", page.Entries)
	}
}
