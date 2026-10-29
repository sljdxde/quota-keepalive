package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileCreatesContentAtomically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := WriteFile(path, []byte(`{"a":1}`), 0o600); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读回失败: %v", err)
	}
	if string(got) != `{"a":1}` {
		t.Errorf("内容不符: %q", got)
	}
}

func TestWriteFileIsOwnerOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat 失败: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("期望 0600，实际 %#o", perm)
	}
}

func TestWriteFileLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "state.json" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("期望仅留下 state.json、无临时残留，实际: %v", names)
	}
}

func TestWriteFileReplacesExistingContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	_ = WriteFile(path, []byte("old"), 0o600)
	if err := WriteFile(path, []byte("new"), 0o600); err != nil {
		t.Fatalf("覆盖写入失败: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "new" {
		t.Errorf("期望新内容，实际 %q", got)
	}
}

func TestEnsureDirCreatesOwnerOnlyDirectory(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "qka", "nested")
	if err := EnsureDir(dir, 0o700); err != nil {
		t.Fatalf("建目录失败: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat 失败: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("期望目录")
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("期望 0700，实际 %#o", perm)
	}
}
