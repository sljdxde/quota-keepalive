package market

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"testing"
)

func signRelease(t *testing.T, artifact []byte) (ed25519.PublicKey, Release) {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	sum := sha256.Sum256(artifact)
	sig := ed25519.Sign(priv, artifact)
	return pub, Release{
		ID: "workbuddy-cn", Version: "1.2.0",
		Artifact: artifact, SHA256: sum[:], Signature: sig,
	}
}

func TestVerifyValidRelease(t *testing.T) {
	pub, r := signRelease(t, []byte("manifest+effects"))
	if err := VerifyRelease(pub, r); err != nil {
		t.Fatalf("合法发布应通过验证，实际: %v", err)
	}
}

func TestVerifyTamperedArtifact(t *testing.T) {
	pub, r := signRelease(t, []byte("manifest+effects"))
	r.Artifact = []byte("manifest+effects-tampered")
	if err := VerifyRelease(pub, r); err != ErrHashMismatch {
		t.Fatalf("篡改制品应哈希不匹配，实际: %v", err)
	}
}

func TestVerifyTamperedEffects(t *testing.T) {
	// 效应声明是签名内容：篡改效应（在制品内）即验签失败。
	pub, r := signRelease(t, []byte(`{"actions":[{"effects":{"spendsCredits":false}}]}`))
	tampered := []byte(`{"actions":[{"effects":{"spendsCredits":true}}]}`)
	r.Artifact = tampered
	sum := sha256.Sum256(tampered)
	r.SHA256 = sum[:] // 哈希若被重算，签名仍应失败
	if err := VerifyRelease(pub, r); err != ErrBadSignature {
		t.Fatalf("篡改效应应签名失败，实际: %v", err)
	}
}

func TestVerifyBadSignature(t *testing.T) {
	pub, r := signRelease(t, []byte("manifest"))
	r.Signature = []byte("bad-signature")
	if err := VerifyRelease(pub, r); err != ErrBadSignature {
		t.Fatalf("坏签名应验证失败，实际: %v", err)
	}
}

func TestActivateAfterVerification(t *testing.T) {
	m := NewManager(&InstalledRelease{ID: "a", Version: "1.0"})
	if err := m.Activate(InstalledRelease{ID: "a", Version: "1.1"}); err != nil {
		t.Fatal(err)
	}
	if m.Current().Version != "1.1" {
		t.Fatalf("验证通过后应激活新版本，实际 %s", m.Current().Version)
	}
}

func TestInFlightVersionPinned(t *testing.T) {
	m := NewManager(&InstalledRelease{ID: "a", Version: "1.0"})
	m.BeginTask()
	if err := m.Activate(InstalledRelease{ID: "a", Version: "1.1"}); err != ErrVersionPinned {
		t.Fatalf("执行中应固定版本，实际: %v", err)
	}
	m.EndTask()
	if err := m.Activate(InstalledRelease{ID: "a", Version: "1.1"}); err != nil {
		t.Fatalf("任务结束后应可切换，实际: %v", err)
	}
}

func TestRollbackToPrevious(t *testing.T) {
	m := NewManager(&InstalledRelease{ID: "a", Version: "1.0"})
	m.Activate(InstalledRelease{ID: "a", Version: "1.1"})
	if err := m.Rollback(); err != nil {
		t.Fatalf("应回退上一版，实际: %v", err)
	}
	if m.Current().Version != "1.0" {
		t.Fatalf("应回退到 1.0，实际 %s", m.Current().Version)
	}
}

func TestNoFallbackDisables(t *testing.T) {
	m := NewManager(&InstalledRelease{ID: "a", Version: "1.0"})
	if err := m.Rollback(); err != ErrNoFallback {
		t.Fatalf("无回退应返回 ErrNoFallback，实际: %v", err)
	}
	if m.Current() != nil {
		t.Fatal("无回退应禁用（current 为 nil），绝不以未知版本访问凭据")
	}
}
