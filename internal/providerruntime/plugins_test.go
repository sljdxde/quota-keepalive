package providerruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePackage(t *testing.T, root, dir, name, body string, entry bool) {
	t.Helper()
	path := filepath.Join(root, dir)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if body != "" {
		if err := os.WriteFile(filepath.Join(path, "package.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if entry {
		if err := os.WriteFile(filepath.Join(path, "index.mjs"), []byte("export {}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDiscoverPlugins(t *testing.T) {
	root := t.TempDir()
	writePackage(t, root, "valid", "", `{"name":"@magpie-community/opencode-valid-auth"}`, true)
	writePackage(t, root, "valid-2", "", `{"name":"@magpie-community/opencode-valid-2-auth"}`, true)
	writePackage(t, root, "bad-json", "", `{`, true)
	writePackage(t, root, "not-provider", "", `{"name":"other"}`, true)
	writePackage(t, root, "missing-entry", "", `{"name":"@magpie-community/opencode-missing-auth"}`, false)
	writePackage(t, root, ".hidden", "", `{"name":"@magpie-community/opencode-hidden-auth"}`, true)
	if err := os.WriteFile(filepath.Join(root, "file"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	plugins, err := DiscoverPlugins(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(plugins) != 2 || plugins[0].Spec != "@magpie-community/opencode-valid-2-auth" {
		t.Fatalf("plugins: %+v", plugins)
	}
	if !filepath.IsAbs(plugins[0].Target) {
		t.Fatalf("plugin target must be absolute: %s", plugins[0].Target)
	}
}

func TestReadPackageMissing(t *testing.T) {
	if _, err := readPackage(t.TempDir()); err == nil {
		t.Fatal("missing package.json should fail")
	}
}

func TestDiscoverPluginsUsesManifestMain(t *testing.T) {
	root := t.TempDir()
	writePackage(t, root, "custom", "", `{"name":"@magpie-community/opencode-custom-auth","main":"main.cjs"}`, false)
	if err := os.WriteFile(filepath.Join(root, "custom", "main.cjs"), []byte("module.exports={};"), 0o600); err != nil {
		t.Fatal(err)
	}
	plugins, err := DiscoverPlugins(root)
	if err != nil || len(plugins) != 1 {
		t.Fatalf("custom main: %+v %v", plugins, err)
	}
}

func TestDiscoverPluginsDirectoryError(t *testing.T) {
	if _, err := DiscoverPlugins(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing root should fail")
	}
}

func TestFindPluginReturning(t *testing.T) {
	root := t.TempDir()
	if err := FindPluginReturning(root, "missing"); err == nil ||
		!strings.Contains(strings.ToLower(err.Error()), "插件") {
		t.Fatalf("missing plugin should be descriptive: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "present"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := FindPluginReturning(root, "present"); err != nil {
		t.Fatal(err)
	}
}

func TestIsProviderPackageAndHasEntry(t *testing.T) {
	if !isProviderPackage("@magpie-community/opencode-x-auth") ||
		isProviderPackage("@other/opencode-x-auth") {
		t.Fatal("provider package naming mismatch")
	}
	dir := t.TempDir()
	if hasEntry(dir, packageManifest{}) {
		t.Fatal("empty dir should not have entry")
	}
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !hasEntry(dir, packageManifest{}) {
		t.Fatal("index.js should be an entry")
	}
}
