package portsel

import (
	"errors"
	"io/fs"
	"net"
	"testing"
)

func TestDescribeOccupantParsesDocker(t *testing.T) {
	occ, err := describeOccupant(43189, func(int) ([]string, error) {
		return []string{"p1234", "ccom.docker.backend", "f3", "tIPv4"}, nil
	})
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if occ.PID != 1234 {
		t.Errorf("PID 期望 1234，实际 %d", occ.PID)
	}
	if occ.Kind != "docker" {
		t.Errorf("期望识别为 docker，实际 %q", occ.Kind)
	}
}

func TestDescribeOccupantParsesOrbStack(t *testing.T) {
	occ, err := describeOccupant(43189, func(int) ([]string, error) {
		return []string{"p5678", "cOrbStack"}, nil
	})
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if occ.Kind != "orbstack" {
		t.Errorf("期望识别为 orbstack，实际 %q", occ.Kind)
	}
}

func TestDescribeOccupantDefaultsToOther(t *testing.T) {
	occ, err := describeOccupant(43189, func(int) ([]string, error) {
		return []string{"p7", "cpython3.14"}, nil
	})
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if occ.Kind != "other" {
		t.Errorf("期望 other，实际 %q", occ.Kind)
	}
}

func TestDescribeOccupantMissingReturnsNotExist(t *testing.T) {
	_, err := describeOccupant(43189, func(int) ([]string, error) {
		return []string{}, nil
	})
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("无占用者应返回不存在，实际 %v", err)
	}
}

func TestDescribeOccupantReportsRealListener(t *testing.T) {
	l, err := net.Listen("tcp", Host+":0")
	if err != nil {
		t.Fatalf("准备监听失败: %v", err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port

	occ, err := DescribeOccupant(port)
	if err != nil {
		t.Fatalf("识别真实占用者失败: %v", err)
	}
	if occ.PID <= 0 {
		t.Error("期望有效 PID")
	}
	if occ.Name == "" {
		t.Error("期望占用者名称")
	}
	if occ.Port != port {
		t.Errorf("端口期望 %d，实际 %d", port, occ.Port)
	}
}
