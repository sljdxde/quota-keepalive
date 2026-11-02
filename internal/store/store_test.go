package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenCreatesOwnerOnlyDatabase(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "qka")

	s, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer s.Close()

	info, err := os.Stat(filepath.Join(cfg, "qka.db"))
	if err != nil {
		t.Fatalf("stat 数据库失败: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("数据库期望 0600，实际 %#o", perm)
	}
}

func TestOpenCreatesOwnerOnlyConfigDir(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "nested", "qka")

	s, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer s.Close()

	info, err := os.Stat(cfg)
	if err != nil {
		t.Fatalf("stat 配置目录失败: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("配置目录期望 0700，实际 %#o", perm)
	}
}

func TestOpenAppliesLatestMigration(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer s.Close()

	version, err := s.SchemaVersion(context.Background())
	if err != nil {
		t.Fatalf("读取迁移版本失败: %v", err)
	}
	if version != latestVersion {
		t.Errorf("迁移版本期望 %d，实际 %d", latestVersion, version)
	}
}

func TestWorkBuddyFirstMigrationPreservesLaterUserChoices(t *testing.T) {
	dir := t.TempDir()
	first, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.db.Exec(`
INSERT INTO providers
  (id, name, enabled, created_at, updated_at)
VALUES
  ('workbuddy', 'WorkBuddy', 1, 'ts', 'ts'),
  ('qoder', 'Qoder', 1, 'ts', 'ts');
DELETE FROM schema_migrations WHERE version = 6;`); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	var workbuddy, qoder int
	if err := second.db.QueryRow(`SELECT enabled FROM providers WHERE id = 'workbuddy'`).Scan(&workbuddy); err != nil {
		t.Fatal(err)
	}
	if err := second.db.QueryRow(`SELECT enabled FROM providers WHERE id = 'qoder'`).Scan(&qoder); err != nil {
		t.Fatal(err)
	}
	if workbuddy != 1 || qoder != 0 {
		t.Fatalf("WorkBuddy-first migration mismatch: workbuddy=%d qoder=%d", workbuddy, qoder)
	}
	if _, err := second.db.Exec(`UPDATE providers SET enabled = 1 WHERE id = 'qoder'`); err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}

	third, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer third.Close()
	if err := third.db.QueryRow(`SELECT enabled FROM providers WHERE id = 'qoder'`).Scan(&qoder); err != nil {
		t.Fatal(err)
	}
	if qoder != 1 {
		t.Fatal("migration must run once and preserve later user choices")
	}
}

func TestOpenAddsUsageKnownColumns(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	rows, err := s.db.Query(`PRAGMA table_info(daily_ops)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, kind string
		var notNull, pk int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &pk); err != nil {
			t.Fatal(err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"credits_known", "balance_known"} {
		if !columns[name] {
			t.Errorf("daily_ops 缺少额度已知标记列 %s", name)
		}
	}
}

func TestOpenUpgradesV1WithoutLosingDailyOps(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "qka.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(schemaV1); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
CREATE TABLE schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL
);
INSERT INTO schema_migrations(version, applied_at) VALUES(1, '2026-10-29T00:00:00Z');`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
INSERT INTO daily_ops (target, account_id, operation, day_key, state, updated_at)
VALUES ('workbuddy-cn', 'wb_one', 'checkin', '2026-10-29', 'signed_confirmed', 'ts');`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var state string
	if err := s.db.QueryRow(`SELECT state FROM daily_ops
WHERE target = 'workbuddy-cn' AND account_id = 'wb_one'`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "signed_confirmed" {
		t.Errorf("升级不得丢失既有签到状态，实际 %q", state)
	}
	var creditsKnown, balanceKnown int
	if err := s.db.QueryRow(`SELECT credits_known, balance_known FROM daily_ops
WHERE target = 'workbuddy-cn' AND account_id = 'wb_one'`).Scan(
		&creditsKnown, &balanceKnown); err != nil {
		t.Fatal(err)
	}
	if creditsKnown != 0 || balanceKnown != 0 {
		t.Errorf("旧记录额度应保持未知，实际 credits=%d balance=%d",
			creditsKnown, balanceKnown)
	}
}

func TestReopenIsIdempotent(t *testing.T) {
	dir := t.TempDir()

	first, err := Open(dir)
	if err != nil {
		t.Fatalf("首次 Open 失败: %v", err)
	}
	v1, _ := first.SchemaVersion(context.Background())
	if err := first.Close(); err != nil {
		t.Fatalf("首次 Close 失败: %v", err)
	}

	second, err := Open(dir)
	if err != nil {
		t.Fatalf("再次 Open 失败: %v", err)
	}
	defer second.Close()

	v2, err := second.SchemaVersion(context.Background())
	if err != nil {
		t.Fatalf("再次读取版本失败: %v", err)
	}
	if v2 != v1 {
		t.Errorf("重复打开后版本漂移: %d -> %d", v1, v2)
	}
}
