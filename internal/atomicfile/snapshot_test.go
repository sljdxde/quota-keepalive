package atomicfile

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSnapshotCanRestorePreviousEndpoint(t *testing.T) {
	dir := t.TempDir()
	original := Endpoint{Host: "127.0.0.1", Port: 43189, Path: "/mcp"}
	changed := Endpoint{Host: "127.0.0.1", Port: 43200, Path: "/mcp"}

	if err := SaveEndpoint(dir, original); err != nil {
		t.Fatalf("保存原始端点失败: %v", err)
	}
	snap, err := TakeSnapshot(dir, time.Unix(1700000000, 0))
	if err != nil {
		t.Fatalf("拍快照失败: %v", err)
	}
	if err := snap.Save(dir); err != nil {
		t.Fatalf("写快照失败: %v", err)
	}

	if err := SaveEndpoint(dir, changed); err != nil {
		t.Fatalf("改写端点失败: %v", err)
	}

	if err := RestoreLatestSnapshot(dir); err != nil {
		t.Fatalf("恢复快照失败: %v", err)
	}

	got, err := LoadEndpoint(dir)
	if err != nil {
		t.Fatalf("读回端点失败: %v", err)
	}
	if got != original {
		t.Errorf("恢复后期望 %+v，实际 %+v", original, got)
	}
}

func TestSnapshotFileIsOwnerOnly(t *testing.T) {
	dir := t.TempDir()
	_ = SaveEndpoint(dir, Endpoint{Host: "127.0.0.1", Port: 43189, Path: "/mcp"})
	snap, err := TakeSnapshot(dir, time.Unix(1700000000, 0))
	if err != nil {
		t.Fatalf("拍快照失败: %v", err)
	}
	if err := snap.Save(dir); err != nil {
		t.Fatalf("写快照失败: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, "snapshots", "snapshot-1700000000.json"))
	if err != nil {
		t.Fatalf("stat 快照失败: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("快照文件期望 0600，实际 %#o", perm)
	}
}

func TestSnapshotNeverCapturesCredentials(t *testing.T) {
	dir := t.TempDir()
	// 模拟一个凭据文件，它绝不能进入快照。
	if err := os.WriteFile(filepath.Join(dir, "credentials.json"), []byte(`{"token":"SECRET"}`), 0o600); err != nil {
		t.Fatalf("写凭据文件失败: %v", err)
	}
	if err := SaveEndpoint(dir, Endpoint{Host: "127.0.0.1", Port: 43189, Path: "/mcp"}); err != nil {
		t.Fatalf("保存端点失败: %v", err)
	}

	snap, err := TakeSnapshot(dir, time.Unix(1700000000, 0))
	if err != nil {
		t.Fatalf("拍快照失败: %v", err)
	}
	if _, ok := snap.Files["credentials.json"]; ok {
		t.Fatal("快照不得包含凭据文件 credentials.json")
	}
	if _, ok := snap.Files["endpoint.json"]; !ok {
		t.Fatal("快照应包含非凭据状态文件 endpoint.json")
	}

	encoded, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("序列化快照失败: %v", err)
	}
	if bytes.Contains(encoded, []byte("SECRET")) {
		t.Fatal("快照序列化内容中不得出现任何凭据明文")
	}
}
