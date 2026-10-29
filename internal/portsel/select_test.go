package portsel

import (
	"errors"
	"testing"
)

func TestSelectDefaultsWhenFree(t *testing.T) {
	ep, err := Select(func(int) bool { return true })
	if err != nil {
		t.Fatalf("选择失败: %v", err)
	}
	if ep.Port != DefaultPort {
		t.Errorf("默认空闲应选 %d，实际 %d", DefaultPort, ep.Port)
	}
	if ep.Host != Host || ep.Path != Path {
		t.Errorf("端点身份不符: %+v", ep)
	}
}

func TestSelectSkipsOccupiedDefault(t *testing.T) {
	ep, err := Select(func(port int) bool { return port != DefaultPort })
	if err != nil {
		t.Fatalf("选择失败: %v", err)
	}
	if ep.Port != DefaultPort+1 {
		t.Errorf("默认被占应顺序选 %d，实际 %d", DefaultPort+1, ep.Port)
	}
}

func TestSelectSkipsToFirstFreeInRange(t *testing.T) {
	ep, err := Select(func(port int) bool { return port >= 43195 })
	if err != nil {
		t.Fatalf("选择失败: %v", err)
	}
	if ep.Port != 43195 {
		t.Errorf("应选范围内第一个可用 43195，实际 %d", ep.Port)
	}
}

func TestSelectCanUseLastPortInRange(t *testing.T) {
	ep, err := Select(func(port int) bool { return port == MaxPort })
	if err != nil {
		t.Fatalf("选择失败: %v", err)
	}
	if ep.Port != MaxPort {
		t.Errorf("应能选到范围上界 %d，实际 %d", MaxPort, ep.Port)
	}
}

func TestSelectFailsWhenRangeExhausted(t *testing.T) {
	_, err := Select(func(int) bool { return false })
	if !errors.Is(err, ErrNoFreePort) {
		t.Fatalf("范围内全占应报 ErrNoFreePort，实际 %v", err)
	}
}

func TestSelectNeverPicksAboveRange(t *testing.T) {
	// 仅范围外端口可用，绝不能被选中。
	_, err := Select(func(port int) bool { return port == MaxPort+1 })
	if !errors.Is(err, ErrNoFreePort) {
		t.Fatalf("范围外端口不得被选中，实际 err=%v", err)
	}
}
