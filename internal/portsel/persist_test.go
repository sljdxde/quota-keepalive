package portsel

import (
	"errors"
	"testing"

	"qka/internal/atomicfile"
)

func TestSelectAndPersistWritesEndpoint(t *testing.T) {
	dir := t.TempDir()

	ep, err := SelectAndPersist(dir, func(int) bool { return true })
	if err != nil {
		t.Fatalf("选择并持久化失败: %v", err)
	}
	if ep.Port != DefaultPort {
		t.Errorf("期望默认端口 %d，实际 %d", DefaultPort, ep.Port)
	}

	got, err := atomicfile.LoadEndpoint(dir)
	if err != nil {
		t.Fatalf("读回持久化端点失败: %v", err)
	}
	if got.Port != DefaultPort {
		t.Errorf("持久化端口期望 %d，实际 %d", DefaultPort, got.Port)
	}
}

func TestSelectAndPersistKeepsPersistedPortAcrossRestart(t *testing.T) {
	dir := t.TempDir()

	// 首装：默认端口被占，顺序选到 DefaultPort+1。
	first, err := SelectAndPersist(dir, func(port int) bool { return port != DefaultPort })
	if err != nil {
		t.Fatalf("首次持久化失败: %v", err)
	}
	if first.Port != DefaultPort+1 {
		t.Fatalf("首装期望选到 %d，实际 %d", DefaultPort+1, first.Port)
	}

	// 重启：默认端口现已空闲，但必须保持已持久化端口，不静默改回默认。
	second, err := SelectAndPersist(dir, func(int) bool { return true })
	if err != nil {
		t.Fatalf("再次持久化失败: %v", err)
	}
	if second.Port != DefaultPort+1 {
		t.Errorf("重启后应保持已持久化端口 %d，实际 %d", DefaultPort+1, second.Port)
	}
}

func TestSelectAndPersistConflictWhenPairedPortOccupied(t *testing.T) {
	dir := t.TempDir()

	if _, err := SelectAndPersist(dir, func(int) bool { return true }); err != nil {
		t.Fatalf("首次持久化失败: %v", err)
	}

	// 已配对端口被占用：必须显式报错，绝不静默改端口。
	_, err := SelectAndPersist(dir, func(int) bool { return false })
	if !errors.Is(err, ErrEndpointConflict) {
		t.Fatalf("期望 ErrEndpointConflict，实际 %v", err)
	}
}
