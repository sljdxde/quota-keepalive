package providerruntime

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DiscoverPlugins finds QKA provider plugin packages in a directory. Program
// arguments, package names, and paths never pass through a shell.
func DiscoverPlugins(root string) ([]Plugin, error) {
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []Plugin
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		manifest, err := readPackage(dir)
		if err != nil || manifest.Name == "" || !isProviderPackage(manifest.Name) {
			continue
		}
		if !hasEntry(dir, manifest) {
			continue
		}
		out = append(out, Plugin{Spec: manifest.Name, Target: dir})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Spec < out[j].Spec })
	return out, nil
}

type packageManifest struct {
	Name    string          `json:"name"`
	Version string          `json:"version"`
	Main    string          `json:"main"`
	Exports json.RawMessage `json:"exports"`
}

func readPackage(dir string) (packageManifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return packageManifest{}, err
	}
	var manifest packageManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return packageManifest{}, err
	}
	return manifest, nil
}

func isProviderPackage(name string) bool {
	return strings.HasPrefix(name, "@magpie-community/opencode-") &&
		strings.HasSuffix(name, "-auth")
}

func hasEntry(dir string, manifest packageManifest) bool {
	candidates := []string{"index.mjs", "index.js", "index.cjs"}
	if manifest.Main != "" {
		candidates = append([]string{manifest.Main}, candidates...)
	}
	for _, candidate := range candidates {
		info, err := os.Stat(filepath.Join(dir, candidate))
		if err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

// FindPluginReturning returns a descriptive error when a provider package is
// absent from the bundled runtime.
func FindPluginReturning(root, spec string) error {
	_, err := os.Stat(filepath.Join(root, spec))
	if errors.Is(err, fs.ErrNotExist) {
		return errors.New("供应商插件未随包安装")
	}
	return err
}
