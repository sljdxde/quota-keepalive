// Package workbuddy implements the WorkBuddy CN browser-login protocol
// verified against Magpie's opencode-workbuddy-auth plugin.
package workbuddy

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultBaseURL is the official WorkBuddy CN API origin.
	DefaultBaseURL = "https://copilot.tencent.com"
	// ProtocolBaseline records the upstream behavior this port follows.
	ProtocolBaseline = "@magpie-community/opencode-workbuddy-auth@0.2.0"

	userAgent       = "WorkBuddy/5.9.3"
	loginPageVer    = "2.0.0"
	defaultPoll     = time.Second
	defaultTimeout  = 5 * time.Minute
	maxResponseSize = 1 << 20
)

// LoginStatus is the user-visible browser-login state.
type LoginStatus string

const (
	LoginIdle                LoginStatus = "idle"
	LoginPending             LoginStatus = "pending"
	LoginPendingVerification LoginStatus = "pending_verification"
	LoginVerified            LoginStatus = "verified"
	LoginReauthRequired      LoginStatus = "reauth_required"
	LoginRetryable           LoginStatus = "retryable"
	LoginCancelled           LoginStatus = "cancelled"
	LoginExpired             LoginStatus = "expired"
	LoginFailed              LoginStatus = "failed"
)

// Login stages are safe to display and never contain credentials.
const (
	StageWaitingForBrowser = "waiting_for_browser"
)

var (
	// ErrUnsupportedTarget means this service only handles WorkBuddy CN.
	ErrUnsupportedTarget = errors.New("仅支持 WorkBuddy CN 浏览器登录")
	errUnsafeAuthURL     = errors.New("官方登录入口不安全")
	errNetwork           = errors.New("无法连接 WorkBuddy CN，请检查网络后重试")
	errInvalidResponse   = errors.New("WorkBuddy CN 返回了无法识别的响应")
	errLoginRejected     = errors.New("WorkBuddy CN 未完成授权")
	errCredentialStore   = errors.New("无法安全保存授权结果")
	errBrowserOpen       = errors.New("无法打开 WorkBuddy CN 官方登录页")
	randomRead           = rand.Read
)

// LoginView is the only login state exposed to the GUI or MCP layers.
type LoginView struct {
	SessionID      string      `json:"session_id,omitempty"`
	Target         string      `json:"target"`
	Status         LoginStatus `json:"status"`
	Stage          string      `json:"stage,omitempty"`
	Message        string      `json:"message"`
	OfficialOrigin string      `json:"official_origin,omitempty"`
	Protocol       string      `json:"protocol"`
	CanCancel      bool        `json:"can_cancel"`
	StartedAt      time.Time   `json:"started_at,omitempty"`
	ExpiresAt      time.Time   `json:"expires_at,omitempty"`
}

// PendingCredential is stored only in the secure credential backend. It is
// intentionally not returned by LoginView, MCP, the database, or the UI.
type PendingCredential struct {
	Target           string `json:"target"`
	SessionID        string `json:"session_id"`
	Protocol         string `json:"protocol"`
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	AccessExpiresAt  int64  `json:"access_expires_at"`
	RefreshExpiresAt int64  `json:"refresh_expires_at"`
	Domain           string `json:"domain"`
	TokenType        string `json:"token_type"`
	UID              string `json:"uid"`
	EnterpriseID     string `json:"enterprise_id,omitempty"`
	IdentityType     string `json:"identity_type,omitempty"`
	Nickname         string `json:"nickname,omitempty"`
	PhoneNumber      string `json:"phone_number,omitempty"`
	MaskedIdentity   string `json:"masked_identity"`
}

// PendingStore saves the completed login result without creating an account
// or source. Implementations must use Keychain/protected-file storage.
type PendingStore interface {
	Save(ctx context.Context, sessionID string, payload []byte) error
	Load(ctx context.Context, sessionID string) ([]byte, error)
	Delete(ctx context.Context, sessionID string) error
}

// PendingVerifier verifies a browser-login result without mutating the target
// application. It only persists an account after read-only checks pass.
type PendingVerifier interface {
	Verify(ctx context.Context, credential PendingCredential) VerificationOutcome
}

// LoginConfig injects network, browser, storage, and timing seams.
type LoginConfig struct {
	BaseURL      string
	HTTPClient   *http.Client
	PendingStore PendingStore
	Verifier     PendingVerifier
	OpenBrowser  func(string) error
	PollInterval time.Duration
	Timeout      time.Duration
	Now          func() time.Time
}

type loginSession struct {
	id     string
	view   LoginView
	cancel context.CancelFunc
}

// LoginService coordinates at most one active browser login per target.
type LoginService struct {
	baseURL      string
	client       *http.Client
	store        PendingStore
	verifier     PendingVerifier
	openBrowser  func(string) error
	pollInterval time.Duration
	timeout      time.Duration
	now          func() time.Time

	mu       sync.Mutex
	sessions map[string]*loginSession
}

// NewLoginService constructs the WorkBuddy CN browser-login coordinator.
func NewLoginService(cfg LoginConfig) *LoginService {
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaultPoll
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &LoginService{
		baseURL:      baseURL,
		client:       cfg.HTTPClient,
		store:        cfg.PendingStore,
		verifier:     cfg.Verifier,
		openBrowser:  cfg.OpenBrowser,
		pollInterval: cfg.PollInterval,
		timeout:      cfg.Timeout,
		now:          cfg.Now,
		sessions:     map[string]*loginSession{},
	}
}

// Begin opens the official state/authUrl and starts polling. Repeated calls
// while a login is active return the same session without opening a second tab.
func (s *LoginService) Begin(ctx context.Context, target string) (LoginView, error) {
	if target != "workbuddy-cn" {
		return LoginView{}, ErrUnsupportedTarget
	}

	s.mu.Lock()
	if active := s.sessions[target]; active != nil && active.view.Status == LoginRetryable {
		sessionID := active.id
		active.view.Status = LoginPendingVerification
		active.view.Stage = ""
		active.view.Message = "正在重试只读验证；尚未创建账号"
		active.view.CanCancel = false
		s.mu.Unlock()
		return s.retryPendingVerification(ctx, target, sessionID), nil
	}
	if active := s.sessions[target]; active != nil &&
		(active.view.Status == LoginPending || active.view.Status == LoginPendingVerification) {
		view := active.view
		s.mu.Unlock()
		return view, nil
	}

	started := s.now()
	sessionID, err := randomID()
	if err != nil {
		s.mu.Unlock()
		return LoginView{}, errCredentialStore
	}
	loginCtx, cancel := context.WithTimeout(context.Background(), s.timeout)
	sess := &loginSession{
		id: sessionID,
		view: LoginView{
			SessionID: sessionID,
			Target:    target,
			Status:    LoginPending,
			Message:   "正在打开 WorkBuddy CN 官方登录页",
			Protocol:  ProtocolBaseline,
			CanCancel: true,
			StartedAt: started,
			ExpiresAt: started.Add(s.timeout),
		},
		cancel: cancel,
	}
	s.sessions[target] = sess
	s.mu.Unlock()

	state, err := s.requestState(loginCtx)
	if err != nil {
		s.finishFromContext(target, sess.id, err)
		cancel()
		return s.Status(context.Background(), target), nil
	}

	authURL, err := s.prepareAuthURL(state.AuthURL)
	if err != nil {
		s.fail(target, sess.id, err)
		cancel()
		return s.Status(context.Background(), target), nil
	}
	parsed, _ := url.Parse(authURL)
	origin := parsed.Scheme + "://" + parsed.Host

	s.mu.Lock()
	current := s.sessions[target]
	if current != sess || current.view.Status != LoginPending {
		s.mu.Unlock()
		cancel()
		return s.Status(context.Background(), target), nil
	}
	current.view.Stage = StageWaitingForBrowser
	current.view.Message = "请在 WorkBuddy CN 官方页面完成登录"
	current.view.OfficialOrigin = origin
	s.mu.Unlock()

	if s.openBrowser == nil {
		s.fail(target, sess.id, errBrowserOpen)
		cancel()
		return s.Status(context.Background(), target), nil
	}
	if err := s.openBrowser(authURL); err != nil {
		s.fail(target, sess.id, errBrowserOpen)
		cancel()
		return s.Status(context.Background(), target), nil
	}

	go s.poll(loginCtx, target, sess.id, state.State)
	return s.Status(context.Background(), target), nil
}

func (s *LoginService) retryPendingVerification(ctx context.Context, target, sessionID string) LoginView {
	if s.verifier == nil || s.store == nil {
		s.applyVerification(target, sessionID, VerificationOutcome{
			Status: LoginRetryable, Message: "只读验证暂不可用，请稍后重试；未创建账号",
		})
		return s.Status(context.Background(), target)
	}
	raw, err := s.store.Load(ctx, sessionID)
	if err != nil {
		s.applyVerification(target, sessionID, VerificationOutcome{
			Status: LoginReauthRequired, Message: "待验证登录结果不存在，请重新登录；未创建账号",
		})
		return s.Status(context.Background(), target)
	}
	var credential PendingCredential
	if err := json.Unmarshal(raw, &credential); err != nil || credential.SessionID != sessionID {
		if deleteErr := s.deletePending(sessionID); deleteErr != nil {
			s.applyVerification(target, sessionID, VerificationOutcome{
				Status:  LoginRetryable,
				Message: "待验证登录结果无效，且临时登录凭据清理失败，请重试；未创建账号",
			})
			return s.Status(context.Background(), target)
		}
		s.applyVerification(target, sessionID, VerificationOutcome{
			Status: LoginFailed, Message: "待验证登录结果无效，请重新登录；未创建账号",
		})
		return s.Status(context.Background(), target)
	}
	s.applyVerification(target, sessionID, s.verifier.Verify(ctx, credential))
	return s.Status(context.Background(), target)
}

// Status returns the current or most recent login state for a target.
func (s *LoginService) Status(_ context.Context, target string) LoginView {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.sessions[target]
	if sess == nil {
		return LoginView{
			Target:   target,
			Status:   LoginIdle,
			Message:  "尚未发起 WorkBuddy CN 官方登录",
			Protocol: ProtocolBaseline,
		}
	}
	return sess.view
}

// Cancel ends an active browser login. The saved login result, if any, is
// removed before the cancelled state is returned.
func (s *LoginService) Cancel(ctx context.Context, target string) (LoginView, error) {
	s.mu.Lock()
	sess := s.sessions[target]
	if sess == nil {
		s.mu.Unlock()
		return LoginView{}, ErrUnsupportedTarget
	}
	if sess.view.Status != LoginPending {
		view := sess.view
		s.mu.Unlock()
		return view, nil
	}
	sessionID := sess.id
	sess.view.Status = LoginCancelled
	sess.view.Stage = ""
	sess.view.Message = "已取消 WorkBuddy CN 官方登录；未创建账号"
	sess.view.CanCancel = false
	cancel := sess.cancel
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if err := s.deletePending(sessionID); err != nil {
		s.noteCleanupFailure(target, sessionID)
		return s.Status(ctx, target), err
	}
	return s.Status(ctx, target), nil
}

func (s *LoginService) poll(ctx context.Context, target, sessionID, state string) {
	token, err := s.pollToken(ctx, state)
	if err != nil {
		s.finishFromContext(target, sessionID, err)
		return
	}
	account, err := s.pollIdentity(ctx, state, token)
	if err != nil {
		s.finishFromContext(target, sessionID, err)
		return
	}

	payload := s.pendingCredential(target, sessionID, token, account)
	raw, _ := json.Marshal(payload)
	if s.store == nil {
		s.fail(target, sessionID, errCredentialStore)
		return
	}
	if err := ctx.Err(); err != nil {
		s.finishFromContext(target, sessionID, err)
		return
	}
	if err := s.store.Save(ctx, sessionID, raw); err != nil {
		s.fail(target, sessionID, errCredentialStore)
		return
	}
	if err := ctx.Err(); err != nil {
		cleanupErr := s.deletePending(sessionID)
		s.finishFromContext(target, sessionID, err)
		if cleanupErr != nil {
			s.noteCleanupFailure(target, sessionID)
		}
		return
	}

	s.mu.Lock()
	current := s.sessions[target]
	if current == nil || current.id != sessionID || current.view.Status != LoginPending {
		s.mu.Unlock()
		if err := s.deletePending(sessionID); err != nil {
			s.noteCleanupFailure(target, sessionID)
		}
		return
	}
	current.view.Status = LoginPendingVerification
	current.view.Stage = ""
	current.view.Message = "授权已取得，等待只读验证；尚未创建账号"
	current.view.CanCancel = false
	cancel := current.cancel
	if s.verifier == nil && current.cancel != nil {
		current.cancel()
	}
	s.mu.Unlock()

	if s.verifier == nil {
		return
	}
	verifyCtx, cancelVerify := context.WithTimeout(context.Background(), s.timeout)
	defer cancelVerify()
	outcome := s.verifier.Verify(verifyCtx, payload)
	s.applyVerification(target, sessionID, outcome)
	if cancel != nil {
		cancel()
	}
}

func (s *LoginService) applyVerification(target, sessionID string, outcome VerificationOutcome) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.sessions[target]
	if current == nil || current.id != sessionID || current.view.Status != LoginPendingVerification {
		return
	}
	status := outcome.Status
	switch status {
	case LoginVerified, LoginReauthRequired, LoginRetryable, LoginFailed:
	default:
		status = LoginFailed
	}
	message := strings.TrimSpace(outcome.Message)
	if message == "" {
		message = "只读验证未通过；未创建账号"
	}
	current.view.Status = status
	current.view.Message = message
	current.view.Stage = ""
	current.view.CanCancel = false
}

func (s *LoginService) finishFromContext(target, sessionID string, err error) {
	s.mu.Lock()
	sess := s.sessions[target]
	if sess == nil || sess.id != sessionID || sess.view.Status != LoginPending {
		s.mu.Unlock()
		return
	}
	if errors.Is(err, context.DeadlineExceeded) {
		sess.view.Status = LoginExpired
		sess.view.Message = "WorkBuddy CN 官方登录已超时，未创建账号"
		sess.view.Stage = ""
		sess.view.CanCancel = false
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	s.fail(target, sessionID, err)
}

func (s *LoginService) fail(target, sessionID string, cause error) {
	s.mu.Lock()
	sess := s.sessions[target]
	if sess == nil || sess.id != sessionID || sess.view.Status != LoginPending {
		s.mu.Unlock()
		return
	}
	sess.view.Status = LoginFailed
	sess.view.Stage = ""
	sess.view.Message = publicMessage(cause) + "；未创建账号"
	sess.view.CanCancel = false
	if sess.cancel != nil {
		sess.cancel()
	}
	s.mu.Unlock()
	if err := s.deletePending(sessionID); err != nil {
		s.noteCleanupFailure(target, sessionID)
	}
}

func (s *LoginService) deletePending(sessionID string) error {
	if s.store == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return ignoreMissingPendingCredential(s.store.Delete(ctx, sessionID))
}

func (s *LoginService) noteCleanupFailure(target, sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.sessions[target]
	if sess == nil || sess.id != sessionID {
		return
	}
	sess.view.Message = strings.TrimSpace(sess.view.Message) +
		"；临时登录凭据清理失败，请重试"
}

func publicMessage(err error) string {
	switch {
	case errors.Is(err, errNetwork):
		return errNetwork.Error()
	case errors.Is(err, errUnsafeAuthURL):
		return errUnsafeAuthURL.Error()
	case errors.Is(err, errBrowserOpen):
		return errBrowserOpen.Error()
	case errors.Is(err, errCredentialStore):
		return errCredentialStore.Error()
	case errors.Is(err, errLoginRejected):
		return errLoginRejected.Error()
	default:
		return errInvalidResponse.Error()
	}
}

func randomID() (string, error) {
	b := make([]byte, 16)
	if _, err := randomRead(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (s *LoginService) prepareAuthURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", errUnsafeAuthURL
	}
	if u.Scheme != "https" {
		if !s.loopbackAllowed(u) {
			return "", errUnsafeAuthURL
		}
	}
	sessionID, err := randomID()
	if err != nil {
		return "", errCredentialStore
	}
	q := u.Query()
	q.Set("version", loginPageVer)
	q.Set("loginSessionId", sessionID)
	// force_login 是官方登录页支持的开关（取值为 1/true/yes）：带上它就会跳过
	// 「浏览器里已有登录态就直接静默授权」的路径，直接进入账号选择。
	// 不加这个参数时，用户想添加第二个账号必须先手点页面上的「切换账号」，
	// 第一次点「登录 / 添加账号」只会把浏览器里已有的账号再授权一遍——这就是
	// 「要点两次才能加成功」的原因。
	q.Set("force_login", "1")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (s *LoginService) loopbackAllowed(u *url.URL) bool {
	if u.Scheme != "http" {
		return false
	}
	base, err := url.Parse(s.baseURL)
	if err != nil || base.Scheme != "http" {
		return false
	}
	host := u.Hostname()
	baseHost := base.Hostname()
	return host == baseHost && (host == "127.0.0.1" || host == "::1" || host == "localhost")
}

type envelope struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

type stateData struct {
	State   string `json:"state"`
	AuthURL string `json:"authUrl"`
}

type tokenData struct {
	AccessToken      string `json:"accessToken"`
	RefreshToken     string `json:"refreshToken"`
	ExpiresAt        int64  `json:"expiresAt"`
	ExpiresIn        int64  `json:"expiresIn"`
	RefreshExpiresAt int64  `json:"refreshExpiresAt"`
	RefreshExpiresIn int64  `json:"refreshExpiresIn"`
	Domain           string `json:"domain"`
	TokenType        string `json:"tokenType"`
}

type accountData struct {
	UID          string          `json:"uid"`
	EnterpriseID string          `json:"enterpriseId"`
	Type         string          `json:"type"`
	LastLogin    json.RawMessage `json:"lastLogin"`
	Nickname     string          `json:"nickname"`
	PhoneNumber  string          `json:"phoneNumber"`
}

func (s *LoginService) requestState(ctx context.Context) (stateData, error) {
	endpoint := s.baseURL + "/v2/plugin/auth/state?platform=workbuddy"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader([]byte("{}")))
	if err != nil {
		return stateData{}, errNetwork
	}
	setBaseHeaders(req)
	req.Header.Set("X-No-Authorization", "true")
	req.Header.Set("X-No-User-Id", "true")
	req.Header.Set("X-No-Enterprise-Id", "true")
	req.Header.Set("X-No-Department-Info", "true")

	env, err := s.doJSON(req)
	if err != nil {
		return stateData{}, err
	}
	if env.Code != 0 {
		return stateData{}, errLoginRejected
	}
	var data stateData
	if err := json.Unmarshal(env.Data, &data); err != nil || data.State == "" || data.AuthURL == "" {
		return stateData{}, errInvalidResponse
	}
	return data, nil
}

func (s *LoginService) pollToken(ctx context.Context, state string) (tokenData, error) {
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()
	for {
		token, retry, err := s.requestToken(ctx, state)
		if err == nil && !retry {
			return token, nil
		}
		if err != nil {
			return tokenData{}, err
		}
		select {
		case <-ctx.Done():
			return tokenData{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *LoginService) requestToken(ctx context.Context, state string) (tokenData, bool, error) {
	endpoint := s.baseURL + "/v2/plugin/auth/token?state=" + url.QueryEscape(state)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return tokenData{}, false, errNetwork
	}
	setBaseHeaders(req)
	env, err := s.doJSON(req)
	if err != nil {
		return tokenData{}, false, err
	}
	if env.Code == 11217 {
		return tokenData{}, true, nil
	}
	if env.Code != 0 {
		return tokenData{}, false, errLoginRejected
	}
	var token tokenData
	if err := json.Unmarshal(env.Data, &token); err != nil ||
		token.AccessToken == "" || token.RefreshToken == "" {
		return tokenData{}, false, errInvalidResponse
	}
	return token, false, nil
}

func (s *LoginService) pollIdentity(ctx context.Context, state string, token tokenData) (accountData, error) {
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()
	for {
		account, retry, err := s.requestIdentity(ctx, state, token)
		if err == nil && !retry {
			return account, nil
		}
		if err != nil {
			return accountData{}, err
		}
		select {
		case <-ctx.Done():
			return accountData{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *LoginService) requestIdentity(ctx context.Context, state string, token tokenData) (accountData, bool, error) {
	endpoint := s.baseURL + "/v2/plugin/login/account?state=" + url.QueryEscape(state)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return accountData{}, false, errNetwork
	}
	setBaseHeaders(req)
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	req.Header.Set("X-Domain", s.effectiveDomain(token))
	req.Header.Set("X-No-User-Id", "true")
	req.Header.Set("X-No-Enterprise-Id", "true")

	env, err := s.doJSON(req)
	if err != nil {
		return accountData{}, false, err
	}
	// doJSON maps HTTP 408/429 to the token retry code; identity polling
	// treats both that status signal and Magpie's 12151 as "not ready".
	if env.Code == 12151 || env.Code == 11217 {
		return accountData{}, true, nil
	}
	if env.Code != 0 {
		return accountData{}, false, errLoginRejected
	}
	var account accountData
	if err := json.Unmarshal(env.Data, &account); err != nil || account.UID == "" {
		return accountData{}, false, errInvalidResponse
	}
	return account, false, nil
}

func (s *LoginService) doJSON(req *http.Request) (envelope, error) {
	resp, err := s.client.Do(req)
	if err != nil {
		if ctxErr := req.Context().Err(); ctxErr != nil {
			return envelope{}, ctxErr
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return envelope{}, context.DeadlineExceeded
		}
		if errors.Is(err, context.Canceled) {
			return envelope{}, context.Canceled
		}
		return envelope{}, errNetwork
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusTooManyRequests {
		return envelope{Code: 11217}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return envelope{}, errLoginRejected
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil {
		if ctxErr := req.Context().Err(); ctxErr != nil {
			return envelope{}, ctxErr
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return envelope{}, context.DeadlineExceeded
		}
		return envelope{}, errInvalidResponse
	}
	if len(body) > maxResponseSize {
		return envelope{}, errInvalidResponse
	}
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return envelope{}, errInvalidResponse
	}
	return env, nil
}

func (s *LoginService) pendingCredential(target, sessionID string, token tokenData, account accountData) PendingCredential {
	now := s.now()
	accessExpiry := token.ExpiresAt
	if accessExpiry <= 0 && token.ExpiresIn > 0 {
		accessExpiry = now.Add(time.Duration(token.ExpiresIn) * time.Second).UnixMilli()
	}
	refreshExpiry := token.RefreshExpiresAt
	if refreshExpiry <= 0 && token.RefreshExpiresIn > 0 {
		refreshExpiry = now.Add(time.Duration(token.RefreshExpiresIn) * time.Second).UnixMilli()
	}
	return PendingCredential{
		Target:           target,
		SessionID:        sessionID,
		Protocol:         ProtocolBaseline,
		AccessToken:      token.AccessToken,
		RefreshToken:     token.RefreshToken,
		AccessExpiresAt:  accessExpiry,
		RefreshExpiresAt: refreshExpiry,
		Domain:           s.effectiveDomain(token),
		TokenType:        token.TokenType,
		UID:              account.UID,
		EnterpriseID:     account.EnterpriseID,
		IdentityType:     account.Type,
		Nickname:         account.Nickname,
		PhoneNumber:      account.PhoneNumber,
		MaskedIdentity:   maskIdentity(account.UID, account.PhoneNumber),
	}
}

func (s *LoginService) effectiveDomain(token tokenData) string {
	if token.Domain != "" {
		return token.Domain
	}
	base, err := url.Parse(s.baseURL)
	if err != nil {
		return ""
	}
	return base.Host
}

func maskIdentity(uid, phone string) string {
	if len(phone) > 4 {
		return "****" + phone[len(phone)-4:]
	}
	if len(uid) > 4 {
		return "****" + uid[len(uid)-4:]
	}
	return "****"
}

func setBaseHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
}

// String keeps accidental formatting of configuration free of token data.
func (p PendingCredential) String() string {
	return fmt.Sprintf("PendingCredential{target:%s session:%s}", p.Target, p.SessionID)
}
