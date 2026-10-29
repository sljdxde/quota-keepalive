package atomicfile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveAndLoadEndpointRoundTrip(t *testing.T) {
	dir := t.TempDir()
	ep := Endpoint{Host: "127.0.0.1", Port: 43189, Path: "/mcp"}

	if err := SaveEndpoint(dir, ep); err != nil {
		t.Fatalf("保存端点失败: %v", err)
	}

	got, err := LoadEndpoint(dir)
	if err != nil {
		t.Fatalf("读回端点失败: %v", err)
	}
	if got != ep {
		t.Errorf("端点往返不一致: got=%+v want=%+v", got, ep)
	}
	if url := got.URL(); url != "http://127.0.0.1:43189/mcp" {
		t.Errorf("URL 不符: %q", url)
	}
}

func TestEndpointFileIsOwnerOnly(t *testing.T) {
	dir := t.TempDir()
	if err := SaveEndpoint(dir, Endpoint{Host: "127.0.0.1", Port: 43189, Path: "/mcp"}); err != nil {
		t.Fatalf("保存端点失败: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, "endpoint.json"))
	if err != nil {
		t.Fatalf("stat 失败: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("endpoint.json 期望 0600，实际 %#o", perm)
	}
}

func TestEndpointConfigDirIsOwnerOnly(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "qka")
	if err := SaveEndpoint(cfg, Endpoint{Host: "127.0.0.1", Port: 43189, Path: "/mcp"}); err != nil {
		t.Fatalf("保存端点失败: %v", err)
	}
	info, err := os.Stat(cfg)
	if err != nil {
		t.Fatalf("stat 失败: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("配置目录期望 0700，实际 %#o", perm)
	}
}

func TestLoadEndpointMissingReturnsNotExist(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadEndpoint(dir)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("期望不存在错误，实际 %v", err)
	}
}
