package registry

import (
	"testing"

	"qka/internal/store"
)

const ts = "2026-10-29T10:00:00Z"

func seedAccount(t *testing.T, st *store.Store, target, id string, enabled bool) {
	t.Helper()
	en := 1
	if !enabled {
		en = 0
	}
	if _, err := st.DB().Exec(
		`INSERT INTO accounts (id, target, display_name, masked_identity, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, target, "User", "****1234", en, ts, ts); err != nil {
		t.Fatalf("seed account 失败: %v", err)
	}
}

func seedSource(t *testing.T, st *store.Store, target, account, id, kind string, readOnly bool, health string) {
	t.Helper()
	ro := 0
	if readOnly {
		ro = 1
	}
	if _, err := st.DB().Exec(
		`INSERT INTO sources (id, target, account_id, kind, read_only, health, pinned, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, target, account, kind, ro, health, 0, ts, ts); err != nil {
		t.Fatalf("seed source 失败: %v", err)
	}
}
