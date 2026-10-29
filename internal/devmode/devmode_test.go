package devmode

import (
	"testing"
)

func sampleLoad() UnverifiedLoad {
	return UnverifiedLoad{
		PackageName: "local-thing", SourcePath: "/Users/me/local-thing",
		Target: "qoder-cn", AccountID: "u1", SourceID: "src-1",
	}
}

func TestUnsignedBlockedByDefault(t *testing.T) {
	var events []AuditEvent
	gate := NewGate(false, nil, func(e AuditEvent) { events = append(events, e) })
	if _, err := gate.RequestLoad(sampleLoad()); err != ErrDevModeDisabled {
		t.Fatalf("未签名适配器默认应禁用，实际 %v", err)
	}
	if len(events) != 1 || events[0].Action != "load_blocked_devmode_off" {
		t.Fatal("禁用时应写审计事件")
	}
}

func TestConfirmRequiredEveryLoad(t *testing.T) {
	var confirmations int
	gate := NewGate(true, func(Confirmation) bool {
		confirmations++
		return true
	}, nil)

	grant1, err := gate.RequestLoad(sampleLoad())
	if err != nil {
		t.Fatal(err)
	}
	// 第二次加载必须再次确认（授权不缓存）。
	if _, err := gate.RequestLoad(sampleLoad()); err != nil {
		t.Fatal(err)
	}
	if confirmations != 2 {
		t.Errorf("每次加载都应重新确认，实际确认 %d 次", confirmations)
	}
	// 单次授权仅覆盖指定账号/来源。
	if !grant1.Allows("qoder-cn", "u1", "src-1") {
		t.Error("授权应覆盖当场指定账号/来源")
	}
	if grant1.Allows("qoder-cn", "u2", "src-1") {
		t.Error("授权不得覆盖其他账号")
	}
}

func TestConfirmationShowsDetails(t *testing.T) {
	var seen Confirmation
	gate := NewGate(true, func(c Confirmation) bool {
		seen = c
		return true
	}, nil)
	if _, err := gate.RequestLoad(sampleLoad()); err != nil {
		t.Fatal(err)
	}
	if seen.PackageName != "local-thing" || seen.SourcePath != "/Users/me/local-thing" ||
		seen.Target != "qoder-cn" || seen.AccountID != "u1" {
		t.Errorf("确认信息应含包名/路径/目标/账号，实际 %+v", seen)
	}
}

func TestUserDenial(t *testing.T) {
	var events []AuditEvent
	gate := NewGate(true, func(Confirmation) bool { return false },
		func(e AuditEvent) { events = append(events, e) })
	if _, err := gate.RequestLoad(sampleLoad()); err != ErrLoadDenied {
		t.Fatalf("用户拒绝应返回 ErrLoadDenied，实际 %v", err)
	}
	if len(events) != 1 || events[0].Action != "load_denied" {
		t.Fatal("拒绝应写审计事件")
	}
}
