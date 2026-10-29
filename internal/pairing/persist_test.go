package pairing

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveAndLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := New()
	plain, _ := p.Issue("host-1")

	if err := Save(dir, p); err != nil {
		t.Fatalf("Save 失败: %v", err)
	}

	loaded, err := LoadPairing(dir)
	if err != nil {
		t.Fatalf("LoadPairing 失败: %v", err)
	}
	if !loaded.Verify("host-1", plain) {
		t.Error("加载后凭据应可校验")
	}
}

func TestLoadMissingReturnsEmpty(t *testing.T) {
	p, err := LoadPairing(t.TempDir())
	if err != nil {
		t.Fatalf("缺失文件应返回空配对，实际 %v", err)
	}
	if p.Verify("host-1", "x") {
		t.Error("空配对不应通过任何校验")
	}
}

func TestPairingFilePermissions(t *testing.T) {
	dir := t.TempDir()
	_ = Save(dir, New())

	info, err := os.Stat(filepath.Join(dir, pairingFile))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("配对文件权限应为 0600，实际 %o", perm)
	}
}
