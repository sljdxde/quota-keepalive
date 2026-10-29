package privacy

import (
	"errors"
	"testing"
)

func TestGateEnabledByDefault(t *testing.T) {
	gate := NewGate()
	if !gate.IsEnabled("a") {
		t.Error("目标默认应启用")
	}
	if err := gate.Check("a"); err != nil {
		t.Errorf("默认 Check 应通过，实际 %v", err)
	}
}

func TestDisableShortCircuitsSourceAndSink(t *testing.T) {
	gate := NewGate()
	credReads, dispatches := 0, 0

	readCred := func() error {
		return gate.Guard("a", func() error { credReads++; return nil })
	}
	dispatch := func() error {
		return gate.Guard("a", func() error { dispatches++; return nil })
	}

	// 启用时正常。
	if err := readCred(); err != nil {
		t.Fatal(err)
	}
	if err := dispatch(); err != nil {
		t.Fatal(err)
	}
	if credReads != 1 || dispatches != 1 {
		t.Fatal("计数初始化错误")
	}

	// 停用：源（凭据读取）与汇（派发）同时短路。
	gate.Disable("a")
	if err := readCred(); !errors.Is(err, ErrTargetDisabled) {
		t.Errorf("停用后凭据读取应短路，实际 %v", err)
	}
	if err := dispatch(); !errors.Is(err, ErrTargetDisabled) {
		t.Errorf("停用后派发应短路，实际 %v", err)
	}
	if credReads != 1 || dispatches != 1 {
		t.Error("停用后应零凭据读取、零派发")
	}

	// 重开即恢复。
	gate.Enable("a")
	if err := readCred(); err != nil {
		t.Errorf("重开后应恢复，实际 %v", err)
	}
	if credReads != 2 {
		t.Error("重开后凭据读取应恢复")
	}
}

func TestDisableOneTargetDoesNotAffectOthers(t *testing.T) {
	gate := NewGate()
	gate.Disable("a")

	if gate.IsEnabled("a") {
		t.Error("a 应停用")
	}
	if !gate.IsEnabled("b") {
		t.Error("停用 a 不应影响 b")
	}
	if err := gate.Check("b"); err != nil {
		t.Errorf("b 应正常，实际 %v", err)
	}
}
