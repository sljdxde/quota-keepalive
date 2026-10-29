package settings

import (
	"path/filepath"
	"testing"
)

func TestDefaultValuesMostConservative(t *testing.T) {
	s := Default()
	for _, f := range Schema {
		if got := s.Get(f.Key); got != f.Default {
			t.Errorf("设置项 %s 默认值应为 %v（最保守）", f.Key, f.Default)
		}
	}
}

func TestSetUnknownKeyRejected(t *testing.T) {
	s := Default()
	if err := s.Set("not_a_real_key", true); err == nil {
		t.Fatal("设置未知 key 应被 schema 校验拒绝")
	}
}

func TestGetUnknownKeyReturnsDefault(t *testing.T) {
	s := Default()
	if s.Get("not_a_real_key") {
		t.Fatal("未知 key 应返回零值默认")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := Default()
	if err := s.Set("show_dock_icon", true); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(dir); err != nil {
		t.Fatalf("保存失败: %v", err)
	}

	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	if !loaded.Get("show_dock_icon") {
		t.Error("show_dock_icon 应在往返后为 true")
	}
	if loaded.Get("launch_at_login") {
		t.Error("未设置项应保持默认 false")
	}
}

func TestLoadMissingReturnsDefault(t *testing.T) {
	loaded, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("缺失文件应返回默认而非错误: %v", err)
	}
	for _, f := range Schema {
		if loaded.Get(f.Key) != f.Default {
			t.Errorf("缺失文件时 %s 应为默认值", f.Key)
		}
	}
}

func TestSaveCreatesBackup(t *testing.T) {
	dir := t.TempDir()
	s := Default()
	if err := s.Set("show_dock_icon", true); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(dir); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("launch_at_login", true); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := filepath.Glob(filepath.Join(dir, "settings.json.bak")); err != nil {
		t.Fatal(err)
	}
}
