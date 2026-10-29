package capabilities

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFileSecretStoreRoundTrip(t *testing.T) {
	store := NewFileSecretStore(t.TempDir())
	ctx := context.Background()

	if err := store.Set(ctx, "qka", "acct", "s3cret"); err != nil {
		t.Fatalf("Set 失败: %v", err)
	}
	got, err := store.Get(ctx, "qka", "acct")
	if err != nil {
		t.Fatalf("Get 失败: %v", err)
	}
	if got != "s3cret" {
		t.Errorf("期望 s3cret，实际 %q", got)
	}
}

func TestFileSecretStoreNotFound(t *testing.T) {
	store := NewFileSecretStore(t.TempDir())
	if _, err := store.Get(context.Background(), "qka", "missing"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("未找到应返回 ErrSecretNotFound，实际 %v", err)
	}
}

func TestFileSecretStoreDelete(t *testing.T) {
	store := NewFileSecretStore(t.TempDir())
	ctx := context.Background()
	_ = store.Set(ctx, "qka", "acct", "x")
	if err := store.Delete(ctx, "qka", "acct"); err != nil {
		t.Fatalf("Delete 失败: %v", err)
	}
	if _, err := store.Get(ctx, "qka", "acct"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("删除后应未找到，实际 %v", err)
	}
}

func TestFileSecretStorePermissions(t *testing.T) {
	dir := t.TempDir()
	store := NewFileSecretStore(dir)
	_ = store.Set(context.Background(), "qka", "acct", "x")

	info, err := os.Stat(filepath.Join(dir, "secrets", "qka", "acct.secret"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("凭据文件权限应为 0600，实际 %o", perm)
	}
}
