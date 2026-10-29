package portsel

import (
	"net"
	"testing"
)

func TestPortFreeDetectsListeningPort(t *testing.T) {
	l, err := net.Listen("tcp", Host+":0")
	if err != nil {
		t.Fatalf("准备监听失败: %v", err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port

	if PortFree(port) {
		t.Errorf("正在监听的端口 %d 应被判为占用", port)
	}
}

func TestPortFreeReturnsTrueAfterClose(t *testing.T) {
	l, err := net.Listen("tcp", Host+":0")
	if err != nil {
		t.Fatalf("准备监听失败: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatalf("关闭监听失败: %v", err)
	}

	if !PortFree(port) {
		t.Errorf("已释放的端口 %d 应被判为空闲", port)
	}
}

func TestSelectAndPersistWithRealPortCheck(t *testing.T) {
	dir := t.TempDir()

	ep, err := SelectAndPersist(dir, PortFree)
	if err != nil {
		t.Fatalf("真实环境选择并持久化失败: %v", err)
	}
	if ep.Port < MinPort || ep.Port > MaxPort {
		t.Errorf("选中端口 %d 超出允许范围", ep.Port)
	}
}
