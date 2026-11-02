package privacy

import (
	"os"
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

func TestLoadAndSaveFailures(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(bad); err == nil {
		t.Fatal("损坏状态应报错")
	}
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(filepath.Join(blocker, "state.json")); err == nil {
		t.Fatal("非不存在类读取错误应返回")
	}
	if err := Save(NewGate(), dir); err == nil {
		t.Fatal("路径为目录时保存应失败")
	}
}
