package store

import (
	"context"
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
