package login

import (
	"testing"
	"time"
)

func startAt() time.Time {
	return time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
}

func officialEntry() Entry {
	return Entry{
		Target:     "qoder-cn",
		Kind:       KindHTTPS,
		Location:   "https://login.qoder.example.com/authorize",
		Purpose:    "每日签到",
		TimeoutSec: 120,
	}
}

func TestBeginCreatesPendingSession(t *testing.T) {
	svc := NewService([]string{"login.qoder.example.com"})
	s, err := svc.Begin(officialEntry(), startAt())
	if err != nil {
		t.Fatal(err)
	}
	if s.Status != StatusPending {
		t.Errorf("新会话应 pending，实际 %s", s.Status)
	}
	if s.Target != "qoder-cn" {
		t.Errorf("目标错误: %s", s.Target)
	}
}

func TestBeginRejectsNonWhitelistedHost(t *testing.T) {
	svc := NewService([]string{"login.qoder.example.com"})
	entry := officialEntry()
	entry.Location = "https://evil.example.com/authorize"
	if _, err := svc.Begin(entry, startAt()); err != ErrNonOfficialEntry {
		t.Errorf("非白名单域名应拒绝，实际 %v", err)
	}
}

func TestBeginRejectsInsecureScheme(t *testing.T) {
	svc := NewService([]string{"login.qoder.example.com"})
	entry := officialEntry()
	entry.Location = "http://login.qoder.example.com/authorize"
	if _, err := svc.Begin(entry, startAt()); err != ErrNonOfficialEntry {
		t.Errorf("非 HTTPS 应拒绝，实际 %v", err)
	}
}

func TestVerifyExpiresTimedOutSession(t *testing.T) {
	svc := NewService([]string{"login.qoder.example.com"})
	s, _ := svc.Begin(officialEntry(), startAt())

	// 未超时仍 pending。
	if got := svc.Verify(s.ID, startAt().Add(60*time.Second)); got != StatusPending {
		t.Errorf("未超时应 pending，实际 %s", got)
	}
	// 超过 120s → expired。
	if got := svc.Verify(s.ID, startAt().Add(121*time.Second)); got != StatusExpired {
		t.Errorf("超时应 expired，实际 %s", got)
	}
}

func TestCancelAndReject(t *testing.T) {
	svc := NewService([]string{"login.qoder.example.com"})

	s1, _ := svc.Begin(officialEntry(), startAt())
	if got := svc.Cancel(s1.ID, startAt()); got != StatusCancelled {
		t.Errorf("取消应 cancelled，实际 %s", got)
	}

	s2, _ := svc.Begin(officialEntry(), startAt().Add(time.Minute))
	if got := svc.Reject(s2.ID, startAt().Add(time.Minute)); got != StatusRejected {
		t.Errorf("拒绝应 rejected，实际 %s", got)
	}
}

func TestVerifiedOnlyCreatesManagedSource(t *testing.T) {
	svc := NewService([]string{"login.qoder.example.com"})
	s, _ := svc.Begin(officialEntry(), startAt())

	// pending 不可创建受管来源。
	if svc.CanCreateManagedSource(s.ID) {
		t.Error("pending 不应创建受管来源")
	}

	// 只读验证通过。
	if got := svc.MarkVerified(s.ID, startAt().Add(10*time.Second)); got != StatusVerified {
		t.Errorf("验证通过应 verified，实际 %s", got)
	}
	if !svc.CanCreateManagedSource(s.ID) {
		t.Error("只有 verified 才能创建受管来源")
	}
}

func TestBeginCooldown(t *testing.T) {
	svc := NewService([]string{"login.qoder.example.com"})
	svc.SetBeginCooldown(30 * time.Second)

	if _, err := svc.Begin(officialEntry(), startAt()); err != nil {
		t.Fatal(err)
	}
	// 冷却窗口内再次发起被拒绝。
	if _, err := svc.Begin(officialEntry(), startAt().Add(10*time.Second)); err != ErrBeginCooldown {
		t.Errorf("冷却窗口内应拒绝发起，实际 %v", err)
	}
	// 冷却过后可再次发起。
	if _, err := svc.Begin(officialEntry(), startAt().Add(31*time.Second)); err != nil {
		t.Errorf("冷却过后应可发起，实际 %v", err)
	}
}
