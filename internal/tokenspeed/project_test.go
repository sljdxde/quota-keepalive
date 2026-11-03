package tokenspeed

import (
	"path/filepath"
	"testing"
)

// TestProjectNameDecodesWorkspaceDirs 覆盖从目录名反推项目名的分支，
// 包括非 ASCII 码点（~4E2A 形式）与「整段都是分隔符」的退化情况。
func TestProjectNameDecodesWorkspaceDirs(t *testing.T) {
	cases := []struct {
		name   string
		source string
		path   string
		want   string
	}{
		{
			name:   "dsh workspace with escaped runes",
			source: "dsh",
			path: filepath.Join("/root", "sessions",
				"--Users-me-0041~5DE5~4F5C--", "session-abc", "session.v4.jsonl.zstd"),
			want: "0041工作",
		},
		{
			name:   "qoder project dir",
			source: "qoder",
			path:   filepath.Join("/root", "projects", "-Users-me-SpaceNotes", "a.jsonl"),
			want:   "SpaceNotes",
		},
		{
			name:   "claude project dir",
			source: "claude",
			path:   filepath.Join("/root", "projects", "-Users-me-demo", "a.jsonl"),
			want:   "demo",
		},
		{
			name:   "unknown source has no project",
			source: "codex",
			path:   filepath.Join("/root", "sessions", "2026", "11", "02", "rollout.jsonl"),
			want:   "",
		},
		{
			name:   "degenerate separators collapse to empty",
			source: "qoder",
			path:   filepath.Join("/root", "projects", "-Users-me------", "a.jsonl"),
			want:   "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := projectName(tc.source, tc.path); got != tc.want {
				t.Fatalf("projectName(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

// TestProjectBaseNameRejectsUnusablePaths 覆盖绝对路径取名与拒绝根目录。
func TestProjectBaseNameRejectsUnusablePaths(t *testing.T) {
	if got := projectBaseName("/Users/me/004个人代码仓库/quota-keepalive-agent"); got != "quota-keepalive-agent" {
		t.Fatalf("应取最后一段，实际 %q", got)
	}
	for _, bad := range []string{"", "   ", "/", ".", ".."} {
		if got := projectBaseName(bad); got != "" {
			t.Fatalf("projectBaseName(%q) 应为空，实际 %q", bad, got)
		}
	}
}

// TestSessionDisplayLabelFallsBack 覆盖「项目名 > 模型名 > 短 ID」的回落顺序。
func TestSessionDisplayLabelFallsBack(t *testing.T) {
	if got := sessionDisplayLabel("proj", "model", "agent"); got != "proj" {
		t.Fatalf("有项目名时应优先，实际 %q", got)
	}
	if got := sessionDisplayLabel("  ", "model", "agent"); got != "model" {
		t.Fatalf("无项目名时应回落到模型名，实际 %q", got)
	}
	if got := sessionDisplayLabel("", "", "agent"); got != "会话 "+sessionID("agent") {
		t.Fatalf("都没有时应回落到短 ID，实际 %q", got)
	}
}
