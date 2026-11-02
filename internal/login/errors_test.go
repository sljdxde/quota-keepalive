package login

import (
	"testing"
	"time"
)

func TestInvalidLocationAndDefaultTimeout(t *testing.T) {
	svc := NewService([]string{"login.qoder.example.com"})
	entry := officialEntry()
	entry.Location = "https://[::1"
	if _, err := svc.Begin(entry, startAt()); err != ErrNonOfficialEntry {
		t.Fatalf("无法解析的官方入口应拒绝，实际 %v", err)
	}

	entry = officialEntry()
	entry.TimeoutSec = 0
	sess, err := svc.Begin(entry, startAt())
	if err != nil {
		t.Fatal(err)
	}
	if got := sess.ExpiresAt.Sub(sess.StartedAt); got != 120*time.Second {
		t.Fatalf("默认超时应 120 秒，实际 %v", got)
	}
}

func TestMissingSessionBranches(t *testing.T) {
	svc := NewService(nil)
	now := startAt()
	if got := svc.Verify("missing", now); got != "" {
		t.Fatalf("缺失会话验证应返回空状态，实际 %q", got)
	}
	if got := svc.Cancel("missing", now); got != "" {
		t.Fatalf("缺失会话取消应返回空状态，实际 %q", got)
	}
	if got := svc.Reject("missing", now); got != "" {
		t.Fatalf("缺失会话拒绝应返回空状态，实际 %q", got)
	}
	if got := svc.MarkVerified("missing", now); got != "" {
		t.Fatalf("缺失会话验证通过应返回空状态，实际 %q", got)
	}
	if svc.CanCreateManagedSource("missing") {
		t.Fatal("缺失会话不得创建受管来源")
	}
}
