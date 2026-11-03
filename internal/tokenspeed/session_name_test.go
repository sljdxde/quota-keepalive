package tokenspeed

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

// TestCodexSessionMetaCwdBecomesProjectName 覆盖 codex 用 session_meta.cwd
// 推导会话名：面板里应当看到项目名，而不是一串哈希。
func TestCodexSessionMetaCwdBecomesProjectName(t *testing.T) {
	now := dshFixedNow()
	service := newCoverageService(now)
	path := filepath.Join("/tmp", "codex", "rollout-2026-11-03.jsonl")
	state := &fileState{source: "codex", agentID: path}
	service.files[path] = state

	meta := map[string]any{
		"timestamp": now.Add(-2 * time.Second).UTC().Format(time.RFC3339Nano),
		"type":      "session_meta",
		"payload": map[string]any{
			"id":  "thread-1",
			"cwd": "/Users/me/004个人代码仓库/quota-keepalive-agent",
		},
	}
	raw, _ := json.Marshal(meta)
	service.ingestCodex(path, state, raw)

	if state.project != "quota-keepalive-agent" {
		t.Fatalf("state.project = %q, want quota-keepalive-agent", state.project)
	}
	if got := service.projectByAgent[path]; got != "quota-keepalive-agent" {
		t.Fatalf("projectByAgent = %q", got)
	}

	// 不可用的 cwd（根目录）不应污染项目名。
	other := &fileState{source: "codex", agentID: "other"}
	second := map[string]any{
		"timestamp": now.Add(-2 * time.Second).UTC().Format(time.RFC3339Nano),
		"type":      "session_meta",
		"payload":   map[string]any{"id": "thread-2", "cwd": "/"},
	}
	raw2, _ := json.Marshal(second)
	service.ingestCodex("other", other, raw2)
	if other.project != "" {
		t.Fatalf("根目录 cwd 不应产生项目名，实际 %q", other.project)
	}
	if _, ok := service.projectByAgent["other"]; ok {
		t.Fatal("根目录 cwd 不应登记项目名")
	}
}

// TestDSHSessionHeaderCwdBecomesProjectName 覆盖 DSH 会话头事件的 cwd：
// 目录名用 '-' 同时表示分隔符和连字符，无法无损还原，必须优先用 cwd。
func TestDSHSessionHeaderCwdBecomesProjectName(t *testing.T) {
	root := t.TempDir()
	now := dshFixedNow()
	path := dshSessionPath(root, "name-test-0000-4000-8000-000000000009")
	frame := dshFrame(t,
		`{"type":"session","version":4,"id":"name-test","cwd":"/Users/me/004个人代码仓库/quota-keepalive-agent"}`,
		dshAssistantEvent("msg-name", now, 10, 5, 15, 0),
	)
	dshWriteFile(t, path, frame)

	service := dshNewService(root, now)
	service.Poll()

	snap := service.Snapshot()
	if len(snap.Sessions) != 1 {
		t.Fatalf("会话数 = %d: %+v", len(snap.Sessions), snap.Sessions)
	}
	session := snap.Sessions[0]
	if session.Project != "quota-keepalive-agent" {
		t.Fatalf("project = %q", session.Project)
	}
	if session.Label != "quota-keepalive-agent" {
		t.Fatalf("会话名应使用项目名，实际 %q", session.Label)
	}

	// 会话头 cwd 为根目录时保持空，界面回落到模型名。
	otherRoot := t.TempDir()
	otherPath := dshSessionPath(otherRoot, "name-test-0000-4000-8000-000000000010")
	dshWriteFile(t, otherPath, dshFrame(t,
		`{"type":"session","version":4,"id":"x","cwd":"/"}`,
		dshAssistantEvent("msg-root", now, 10, 5, 15, 0),
	))
	other := dshNewService(otherRoot, now)
	other.Poll()
	if got := other.Snapshot().Sessions; len(got) != 1 || got[0].Project != "" {
		t.Fatalf("根目录 cwd 不应产生项目名: %+v", got)
	}
}

// TestDecodeWorkspaceDirEdgeCases 覆盖目录名解码的退化分支。
func TestDecodeWorkspaceDirEdgeCases(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{".", ""},
		{"--", ""},
		// 包裹目录解出空路径。
		{"----", ""},
		// "~" 后不是合法十六进制：原样保留该字符，不吞掉后续内容。
		{"--Users-me-~ZZ-test--", "test"},
		// 纯分隔符解出空名字。
		{"--////--", ""},
		// 解码后最后一段是 "."：属于无意义路径，必须回退为空。
		{"--a-.--", ""},
	}
	for _, tc := range cases {
		if got := decodeWorkspaceDir(tc.in); got != tc.want {
			t.Fatalf("decodeWorkspaceDir(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
