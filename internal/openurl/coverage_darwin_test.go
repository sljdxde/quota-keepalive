//go:build darwin

package openurl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenUsesLaunchServicesAndRejectsInvalidURL(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "opened")
	script := "#!/bin/sh\nprintf '%s' \"$1\" > \"$QKA_OPENURL_MARKER\"\n"
	if err := os.WriteFile(filepath.Join(dir, "open"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("QKA_OPENURL_MARKER", marker)

	const raw = "https://example.com/login"
	if err := Open(raw); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != raw {
		t.Fatalf("open received %q, want %q", got, raw)
	}

	if err := Open("file:///tmp/secret"); err == nil {
		t.Fatal("invalid URL should be rejected before launching open")
	}
}
