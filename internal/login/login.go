// Package login 实现可选官方登录状态机：发起官方入口、只读验证、超时/取消/拒绝，
// 只有 verified 才创建受管来源。不注入脚本、不读 DOM、不录键盘/剪贴板，无密码/验证码字段。
package login

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"sync"
	"time"
)

// Status 是登录会话状态。
type Status string

const (
	StatusPending   Status = "pending"
	StatusVerified  Status = "verified"
	StatusCancelled Status = "cancelled"
	StatusExpired   Status = "expired"
	StatusRejected  Status = "rejected"
)

// Kind 是官方入口类型。
type Kind string

const (
	KindHTTPS     Kind = "https"
	KindDeepLink  Kind = "deep_link"
	KindAppWindow Kind = "app_window"
)

var (
	// ErrNonOfficialEntry 非白名单域名或不安全 scheme。
	ErrNonOfficialEntry = errors.New("非官方入口：域名不在白名单或 scheme 不安全")
	// ErrSessionNotFound 会话不存在。
	ErrSessionNotFound = errors.New("登录会话不存在")
	// ErrBeginCooldown 发起过于频繁。
	ErrBeginCooldown = errors.New("发起过于频繁，处于冷却窗口")
)

// Entry 是官方登录入口（只含目标/类型/目的地/用途/超时，无凭据字段）。
type Entry struct {
	Target     string `json:"target"`
	Kind       Kind   `json:"kind"`
	Location   string `json:"location"`
	Purpose    string `json:"purpose"`
	TimeoutSec int    `json:"timeout_sec"`
}

// Session 是一次登录会话。
type Session struct {
	ID        string    `json:"id"`
	Target    string    `json:"target"`
	Status    Status    `json:"status"`
	StartedAt time.Time `json:"started_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Service 管理登录会话与发起冷却。
type Service struct {
	mu            sync.Mutex
	allowedHosts  map[string]bool
	sessions      map[string]*Session
	lastBegin     map[string]time.Time
	beginCooldown time.Duration
}

// NewService 构造登录服务，allowedHosts 为厂商官方域名白名单。
func NewService(allowedHosts []string) *Service {
	m := map[string]bool{}
	for _, h := range allowedHosts {
		m[h] = true
	}
	return &Service{
		allowedHosts: m,
		sessions:     map[string]*Session{},
		lastBegin:    map[string]time.Time{},
	}
}

// SetBeginCooldown 设置发起冷却。
func (s *Service) SetBeginCooldown(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.beginCooldown = d
}

func (s *Service) validateEntry(entry Entry) error {
	u, err := url.Parse(entry.Location)
	if err != nil {
		return ErrNonOfficialEntry
	}
	if u.Scheme != "https" {
		return ErrNonOfficialEntry
	}
	if !s.allowedHosts[u.Hostname()] {
		return ErrNonOfficialEntry
	}
	return nil
}

// Begin 发起官方登录：校验入口、检查冷却，返回 pending 会话。
func (s *Service) Begin(entry Entry, now time.Time) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.beginCooldown > 0 {
		if last, ok := s.lastBegin[entry.Target]; ok && now.Sub(last) < s.beginCooldown {
			return Session{}, ErrBeginCooldown
		}
	}
	if err := s.validateEntry(entry); err != nil {
		return Session{}, err
	}

	timeout := time.Duration(entry.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	sess := &Session{
		ID:        newSessionID(),
		Target:    entry.Target,
		Status:    StatusPending,
		StartedAt: now,
		ExpiresAt: now.Add(timeout),
	}
	s.sessions[sess.ID] = sess
	s.lastBegin[entry.Target] = now
	return *sess, nil
}

func (s *Service) get(id string) (*Session, error) {
	sess, ok := s.sessions[id]
	if !ok {
		return nil, ErrSessionNotFound
	}
	return sess, nil
}

// Verify 只读验证：pending 超时则 expired，其余返回当前状态。
func (s *Service) Verify(id string, now time.Time) Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, err := s.get(id)
	if err != nil {
		return ""
	}
	if sess.Status == StatusPending && now.After(sess.ExpiresAt) {
		sess.Status = StatusExpired
	}
	return sess.Status
}

func (s *Service) transition(id string, target Status) (Status, error) {
	sess, err := s.get(id)
	if err != nil {
		return "", err
	}
	if sess.Status == StatusPending {
		sess.Status = target
	}
	return sess.Status, nil
}

// Cancel 用户取消。
func (s *Service) Cancel(id string, _ time.Time) Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, _ := s.transition(id, StatusCancelled)
	return st
}

// Reject 目标拒绝。
func (s *Service) Reject(id string, _ time.Time) Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, _ := s.transition(id, StatusRejected)
	return st
}

// MarkVerified 只读验证通过。
func (s *Service) MarkVerified(id string, _ time.Time) Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, _ := s.transition(id, StatusVerified)
	return st
}

// CanCreateManagedSource 只有 verified 才创建受管来源。
func (s *Service) CanCreateManagedSource(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, err := s.get(id)
	if err != nil {
		return false
	}
	return sess.Status == StatusVerified
}

func newSessionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
