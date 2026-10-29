package pairing

import (
	"bytes"
	"testing"
)

func TestIssueReturnsPlaintext(t *testing.T) {
	p := New()
	plain, err := p.Issue("host-1")
	if err != nil {
		t.Fatalf("Issue 失败: %v", err)
	}
	if plain == "" {
		t.Error("凭据明文不应为空")
	}
}

func TestVerifyCorrectToken(t *testing.T) {
	p := New()
	plain, _ := p.Issue("host-1")
	if !p.Verify("host-1", plain) {
		t.Error("正确凭据应通过校验")
	}
	if p.Verify("host-1", "wrong-token") {
		t.Error("错误凭据不应通过")
	}
	if p.Verify("unknown-host", plain) {
		t.Error("未知宿主不应通过")
	}
}

func TestTokensIndependentPerHost(t *testing.T) {
	p := New()
	p1, _ := p.Issue("host-1")
	p2, _ := p.Issue("host-2")
	if p.Verify("host-1", p2) {
		t.Error("宿主 2 的凭据不得用于宿主 1")
	}
	if p.Verify("host-2", p1) {
		t.Error("宿主 1 的凭据不得用于宿主 2")
	}
}

func TestRevokeOneHostOnly(t *testing.T) {
	p := New()
	p1, _ := p.Issue("host-1")
	p2, _ := p.Issue("host-2")

	p.Revoke("host-1")
	if p.Verify("host-1", p1) {
		t.Error("吊销后旧凭据应失效")
	}
	if !p.Verify("host-2", p2) {
		t.Error("吊销一个宿主不应影响其他宿主")
	}
}

func TestRotateOnlyInvalidatesThatHost(t *testing.T) {
	p := New()
	old, _ := p.Issue("host-1")
	other, _ := p.Issue("host-2")

	newTok, err := p.Rotate("host-1")
	if err != nil {
		t.Fatalf("Rotate 失败: %v", err)
	}
	if p.Verify("host-1", old) {
		t.Error("旋转后旧凭据应失效")
	}
	if !p.Verify("host-1", newTok) {
		t.Error("旋转后新凭据应有效")
	}
	if !p.Verify("host-2", other) {
		t.Error("旋转一个宿主不应影响其他宿主")
	}
}

func TestStoredStateHasHashesOnly(t *testing.T) {
	p := New()
	plain, _ := p.Issue("host-1")
	data, err := p.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(plain)) {
		t.Errorf("存储中不得包含明文，实际 %s", data)
	}
}
