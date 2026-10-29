package privacy

import (
	"path/filepath"
	"testing"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	gate := NewGate()
	gate.Disable("a")
	gate.Disable("c")

	path := filepath.Join(t.TempDir(), "privacy.json")
	if err := Save(gate, path); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.IsEnabled("a") || loaded.IsEnabled("c") {
		t.Error("停用状态应持久化")
	}
	if !loaded.IsEnabled("b") {
		t.Error("未停用目标应保持启用")
	}
}

func TestLoadMissingFileReturnsEnabled(t *testing.T) {
	gate, err := Load(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !gate.IsEnabled("a") {
		t.Error("文件缺失应返回全启用门")
	}
}
