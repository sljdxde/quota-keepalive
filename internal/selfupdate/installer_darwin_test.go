//go:build darwin

package selfupdate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDarwinApplicationBundleAndInstallerHandoff(t *testing.T) {
	root := t.TempDir()
	bundle := filepath.Join(root, "QKA.app")
	executable := filepath.Join(bundle, "Contents", "MacOS", "QKA")
	if err := os.MkdirAll(filepath.Dir(executable), 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := applicationBundle(executable)
	if err != nil || got != bundle {
		t.Fatalf("applicationBundle = %q, %v", got, err)
	}
	svc := &Service{Executable: func() (string, error) { return executable, nil }}
	if got, err := svc.installer(); err != nil || got != bundle {
		t.Fatalf("installer = %q, %v", got, err)
	}
	if _, err := applicationBundle(filepath.Join(root, "QKA")); err == nil {
		t.Fatal("non-bundle executable must fail")
	}
	missing := filepath.Join(root, "Missing.app", "Contents", "MacOS", "QKA")
	if _, err := applicationBundle(missing); err == nil {
		t.Fatal("missing bundle must fail")
	}

	oldPID := updateProcessID
	updateProcessID = func() int { return 1 << 30 }
	t.Cleanup(func() { updateProcessID = oldPID })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pkg := filepath.Join(root, "QKA.dmg")
	if err := os.WriteFile(pkg, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := startInstaller(ctx, bundle, pkg); err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := os.Stat(filepath.Join(root, "qka-install.sh")); err != nil {
		t.Fatal(err)
	}
	if err := startInstaller(ctx, "", pkg); err == nil {
		t.Fatal("empty application bundle must fail")
	}
	if err := startInstaller(context.Background(), bundle,
		filepath.Join(root, "missing", "QKA.dmg")); err == nil {
		t.Fatal("unwritable installer directory must fail")
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if err := startInstaller(canceled, bundle, pkg); err == nil {
		t.Fatal("canceled installer launch must fail")
	}
	for _, required := range []string{
		"com.qka.agent",
		"codesign --verify --deep --strict",
		"ditto \"$SRC\" \"$NEW\"",
		"pgrep -f \"$APP/Contents/MacOS/qka\"",
		"rm -rf \"$OLD\"",
		"rm -f \"$0\"",
		"mv \"$OLD\" \"$APP\"",
	} {
		if !strings.Contains(macInstallerScript, required) {
			t.Errorf("installer script missing %q", required)
		}
	}
}

func TestDarwinInstallerUsesRuntimeExecutable(t *testing.T) {
	if _, err := (&Service{}).installer(); err == nil {
		t.Fatal("test binary is not an application bundle")
	}
}
