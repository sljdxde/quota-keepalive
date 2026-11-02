package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSiblingBunFindsWindowsExecutableName(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bun.exe"), []byte("test"), 0o700); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "bun.exe")
	if got := siblingBun(dir); got != want {
		t.Fatalf("应发现 Windows Bun 文件名，实际 %q want %q", got, want)
	}
}

func TestSiblingBunPrefersExactSiblingOverPath(t *testing.T) {
	dir := t.TempDir()
	want := filepath.Join(dir, "bun")
	if err := os.WriteFile(want, []byte("test"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got := siblingBun(dir); got != want {
		t.Fatalf("应优先使用可执行文件同级 Bun，实际 %q want %q", got, want)
	}
}
