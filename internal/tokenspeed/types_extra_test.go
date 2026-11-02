package tokenspeed

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultConfigRoots(t *testing.T) {
	root := t.TempDir()
	paths := map[string]string{
		"CODEX_HOME":           filepath.Join(root, "codex"),
		"CLAUDE_CONFIG_DIR":    filepath.Join(root, "claude"),
		"QODER_CONFIG_DIR":     filepath.Join(root, "qoder"),
		"QODERCN_CONFIG_DIR":   filepath.Join(root, "qoder-cn"),
		"WORKBUDDY_CONFIG_DIR": filepath.Join(root, "workbuddy"),
		"CODEBUDDY_CONFIG_DIR": filepath.Join(root, "codebuddy"),
		"DSH_HOME":             filepath.Join(root, "dsh"),
		"XDG_DATA_HOME":        filepath.Join(root, "xdg"),
	}
	for key, value := range paths {
		t.Setenv(key, value)
	}

	cfg := DefaultConfig()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Roots) != 9 {
		t.Fatalf("roots = %+v, want nine configured roots", cfg.Roots)
	}
	want := []Root{
		{Source: "codex", Path: filepath.Join(paths["CODEX_HOME"], "sessions")},
		{Source: "claude", Path: filepath.Join(paths["CLAUDE_CONFIG_DIR"], "projects")},
		{Source: "qoder", Path: filepath.Join(paths["QODER_CONFIG_DIR"], "projects")},
		{Source: "qoder-cn", Path: filepath.Join(paths["QODERCN_CONFIG_DIR"], "projects")},
		{Source: "workbuddy", Path: filepath.Join(paths["WORKBUDDY_CONFIG_DIR"], "projects")},
		{Source: "codebuddy", Path: filepath.Join(paths["CODEBUDDY_CONFIG_DIR"], "projects")},
		{Source: "dsh", Path: filepath.Join(paths["DSH_HOME"], "sessions")},
		{Source: "zcode", Path: filepath.Join(home, ".zcode", "cli", "db", "db.sqlite")},
		{Source: "opencode", Path: filepath.Join(paths["XDG_DATA_HOME"], "opencode", "opencode.db")},
	}
	for index := range want {
		if cfg.Roots[index] != want[index] {
			t.Fatalf("root %d = %+v, want %+v", index, cfg.Roots[index], want[index])
		}
	}
	if cfg.PollInterval != 750*time.Millisecond || cfg.Now == nil {
		t.Fatalf("defaults = %+v", cfg)
	}

	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("QODER_CONFIG_DIR", "")
	t.Setenv("QODERCN_CONFIG_DIR", "")
	t.Setenv("WORKBUDDY_CONFIG_DIR", "")
	t.Setenv("CODEBUDDY_CONFIG_DIR", "")
	t.Setenv("DSH_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	cfg = DefaultConfig()
	if home == "" {
		return
	}
	fallback := []string{
		filepath.Join(home, ".codex", "sessions"),
		filepath.Join(home, ".claude", "projects"),
		filepath.Join(home, ".qoder", "projects"),
		filepath.Join(home, ".qoder-cn", "projects"),
		filepath.Join(home, ".workbuddy", "projects"),
		filepath.Join(home, ".codebuddy", "projects"),
		filepath.Join(home, ".dsh", "sessions"),
		filepath.Join(home, ".zcode", "cli", "db", "db.sqlite"),
		filepath.Join(home, ".local", "share", "opencode", "opencode.db"),
	}
	if len(cfg.Roots) != len(fallback) {
		t.Fatalf("fallback roots = %+v", cfg.Roots)
	}
	for index, path := range fallback {
		if cfg.Roots[index].Path != path {
			t.Fatalf("fallback root %d = %+v, want %s", index, cfg.Roots[index], path)
		}
	}
}

func TestFormatSpeedScaleBoundaries(t *testing.T) {
	cases := []struct {
		value float64
		want  string
	}{
		{math.NaN(), "0"},
		{math.Inf(1), "0"},
		{123456, "123k"},
		{1_234_567, "1.23M"},
		{12_345_678, "12.3M"},
		{123_456_789, "123M"},
		{1_234_567_890, "1.23B"},
		{12_345_678_900, "12.3B"},
		{123_456_789_000, "123B"},
	}
	for _, tc := range cases {
		if got := FormatSpeed(tc.value); got != tc.want {
			t.Errorf("FormatSpeed(%v) = %q, want %q", tc.value, got, tc.want)
		}
	}
}

func TestTitleAndSourceEdgeCases(t *testing.T) {
	if got := NormalizeTitleTheme("not-a-theme"); got != TitleThemeVehicles {
		t.Fatalf("unknown theme = %q", got)
	}
	if got := ThemeTitle(TitleThemeVehicles, math.NaN(), true); got != "步行" {
		t.Fatalf("NaN title = %q, want first rung", got)
	}
	if got := sourceLabel("custom"); got != "custom" {
		t.Fatalf("custom source label = %q", got)
	}
	if got := clampTokens(math.NaN()); got != 0 {
		t.Fatalf("NaN tokens = %d", got)
	}
	if got := clampTokens(math.Inf(1)); got != maxTokenValue {
		t.Fatalf("infinite tokens = %d", got)
	}
	if got := clampTokens(float64(maxTokenValue) + 1); got != maxTokenValue {
		t.Fatalf("large tokens = %d", got)
	}
}
