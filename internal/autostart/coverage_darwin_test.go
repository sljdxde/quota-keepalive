//go:build darwin

package autostart

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAssociatedBundleIdentifiersIgnoresUnrelatedLayouts(t *testing.T) {
	if got := associatedBundleIdentifiers("/Applications/QKA.app/qka"); got != "" {
		t.Fatalf("non-MacOS executable should not enumerate associated bundles: %q", got)
	}

	contents := filepath.Join(t.TempDir(), "QKA.app", "Contents")
	exe := filepath.Join(contents, "MacOS", "qka")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := associatedBundleIdentifiers(exe); got != "" {
		t.Fatalf("missing Info.plist should not add bundle identifiers: %q", got)
	}
	if err := os.WriteFile(filepath.Join(contents, "Info.plist"), []byte("<plist/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := associatedBundleIdentifiers(exe); got != "" {
		t.Fatalf("Info.plist without a bundle identifier should be ignored: %q", got)
	}
}

func TestOnReportsDirectoryCreationFailure(t *testing.T) {
	homeFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(homeFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := On(homeFile, "/Applications/QKA.app/Contents/MacOS/qka", ""); err == nil {
		t.Fatal("On should return the LaunchAgents directory creation error")
	}
}
