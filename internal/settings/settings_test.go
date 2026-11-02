package settings

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultValuesMostConservative(t *testing.T) {
	s := Default()
	for _, f := range Schema {
		switch f.Type {
		case FieldBool:
			if got := s.Get(f.Key); got != f.DefaultBool() {
				t.Errorf("设置项 %s 默认值应为 %v（最保守）", f.Key, f.DefaultBool())
			}
		case FieldSelect:
			if got := s.GetString(f.Key); got != f.DefaultString() {
				t.Errorf("设置项 %s 默认值应为 %q（最保守）", f.Key, f.DefaultString())
			}
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
	if s.GetString("not_a_real_key") != "" {
		t.Fatal("未知 select key 应返回空字符串")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := Default()
	if err := s.Set("show_dock_icon", true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetString("theme", "dark"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetString("title_theme", "student"); err != nil {
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
	if got := loaded.GetString("theme"); got != "dark" {
		t.Errorf("theme 应在往返后为 dark，实际 %q", got)
	}
	if got := loaded.GetString("title_theme"); got != "student" {
		t.Errorf("title_theme 应在往返后为 student，实际 %q", got)
	}
}

func TestLoadRetiredTitleThemeFallsBackToVehicles(t *testing.T) {
	dir := t.TempDir()
	data := []byte(`{"title_theme":"cultivation"}`)
	if err := os.WriteFile(filepath.Join(dir, FileName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("旧称号主题不应导致设置加载失败: %v", err)
	}
	if got := loaded.GetString("title_theme"); got != "vehicles" {
		t.Fatalf("旧称号主题应回退到 vehicles，实际 %q", got)
	}
}

func TestLoadMissingReturnsDefault(t *testing.T) {
	loaded, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("缺失文件应返回默认而非错误: %v", err)
	}
	for _, f := range Schema {
		switch f.Type {
		case FieldBool:
			if loaded.Get(f.Key) != f.DefaultBool() {
				t.Errorf("缺失文件时 %s 应为默认值", f.Key)
			}
		case FieldSelect:
			if loaded.GetString(f.Key) != f.DefaultString() {
				t.Errorf("缺失文件时 %s 应为默认值", f.Key)
			}
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

func TestSetStringValidatesTypeAndOptions(t *testing.T) {
	s := Default()
	if err := s.SetString("show_dock_icon", "dark"); !errors.Is(err, ErrWrongType) {
		t.Fatalf("对布尔项写字符串应返回 ErrWrongType，实际 %v", err)
	}
	if err := s.SetString("theme", "sepia"); !errors.Is(err, ErrInvalidOption) {
		t.Fatalf("非法外观选项应返回 ErrInvalidOption，实际 %v", err)
	}
	if err := s.Set("theme", true); !errors.Is(err, ErrWrongType) {
		t.Fatalf("对 select 项写 bool 应返回 ErrWrongType，实际 %v", err)
	}
}
