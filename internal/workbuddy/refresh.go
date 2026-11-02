package workbuddy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const managedRefreshLead = time.Minute

var (
	// ErrManagedRetryable means the refresh failure says nothing about the
	// sign-in itself and may recover without a new browser sign-in.
	ErrManagedRetryable = errors.New("WorkBuddy CN 受管来源刷新暂时失败")
	// ErrManagedReauthRequired means QKA cannot renew the managed source and
	// the user must complete the official browser sign-in again.
	ErrManagedReauthRequired = errors.New("WorkBuddy CN 受管来源需要重新登录")
)

// ManagedCredentialStore is the secure persistence and source-health boundary
// used by a managed WorkBuddy session. Implementations must only store QKA's
// copy and must never write the target application's login file.
type ManagedCredentialStore interface {
	Load(ctx context.Context) ([]byte, error)
	Save(ctx context.Context, payload []byte) error
	MarkHealthy(ctx context.Context) error
	MarkReauthRequired(ctx context.Context) error
}

// ManagedSessionConfig injects the refresh endpoint, HTTP client, secure store,
// and clock used by managed WorkBuddy requests.
type ManagedSessionConfig struct {
	BaseURL    string
	HTTPClient *http.Client
	Store      ManagedCredentialStore
	Now        func() time.Time
}

// ManagedSession renews one managed source and executes requests with its
// current access token. Refresh attempts are serialized so concurrent requests
// on the shared production session cannot spend the same refresh token twice.
type ManagedSession struct {
	baseURL string
	client  *http.Client
	store   ManagedCredentialStore
	now     func() time.Time

	refreshMu sync.Mutex
}

// NewManagedSession constructs a managed WorkBuddy request session.
func NewManagedSession(cfg ManagedSessionConfig) *ManagedSession {
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &ManagedSession{
		baseURL: baseURL,
		client:  cfg.HTTPClient,
		store:   cfg.Store,
		now:     cfg.Now,
	}
}

// Do adds the managed credential headers, renews an expiring access token
// before sending, and retries a request that receives 401 at most once after
// refreshing that source.
func (s *ManagedSession) Do(ctx context.Context, req *http.Request) (*http.Response, error) {
	return s.do(ctx, req, true)
}

// DoAllowUnauthorized keeps the normal expiring-token refresh and single 401
// retry, but returns the final 401 response instead of marking the whole
// source as reauthenticated. Callers that receive this response must use a
// separate read-only request to decide whether the login itself is invalid.
func (s *ManagedSession) DoAllowUnauthorized(ctx context.Context, req *http.Request) (*http.Response, error) {
	return s.do(ctx, req, false)
}

// MarkReauthRequired records that a separate read-only probe proved the
// managed source's login is invalid.
func (s *ManagedSession) MarkReauthRequired(ctx context.Context) error {
	if s.store == nil {
		return errors.New("凭据存储不可用")
	}
	return s.store.MarkReauthRequired(ctx)
}

func (s *ManagedSession) do(
	ctx context.Context,
	req *http.Request,
	markFinal401Reauth bool,
) (*http.Response, error) {
	if req == nil {
		return nil, errors.New("WorkBuddy 请求不能为空")
	}
	body, hasBody, err := readRequestBody(req)
	if err != nil {
		return nil, err
	}
	credential, err := s.loadCredential(ctx)
	if err != nil {
		return nil, err
	}
	if needsManagedRefresh(credential, s.now()) {
		credential, err = s.refresh(ctx, credential)
		if err != nil {
			return nil, err
		}
	}
	resp, err := s.send(ctx, req, credential, body, hasBody)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		return resp, err
	}
	discardAndClose(resp.Body)

	renewed, err := s.refresh(ctx, credential)
	if err != nil {
		return nil, err
	}
	resp, err = s.send(ctx, req, renewed, body, hasBody)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		if !markFinal401Reauth {
			return resp, nil
		}
		discardAndClose(resp.Body)
		return nil, s.reauth(ctx, "刷新后的请求仍返回 401")
	}
	return resp, nil
}

func (s *ManagedSession) loadCredential(ctx context.Context) (PendingCredential, error) {
	if s.store == nil {
		return PendingCredential{}, s.reauth(ctx, "凭据存储不可用")
	}
	raw, err := s.store.Load(ctx)
	if err != nil {
		return PendingCredential{}, s.reauth(ctx, "无法读取受管凭据")
	}
	var credential PendingCredential
	if err := json.Unmarshal(raw, &credential); err != nil ||
		credential.Target != "workbuddy-cn" || credential.AccessToken == "" ||
		credential.UID == "" {
		return PendingCredential{}, s.reauth(ctx, "受管凭据不完整")
	}
	return credential, nil
}

func (s *ManagedSession) refresh(ctx context.Context, observed PendingCredential) (PendingCredential, error) {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()

	current, err := s.loadCredential(ctx)
	if err != nil {
		return PendingCredential{}, err
	}
	if current.AccessToken != observed.AccessToken ||
		current.RefreshToken != observed.RefreshToken {
		return current, nil
	}
	if !refreshableManaged(current, s.now()) {
		return PendingCredential{}, s.reauth(ctx, "refresh token 已失效")
	}

	next, err := s.requestRefresh(ctx, current)
	if err != nil {
		if errors.Is(err, ErrManagedReauthRequired) {
			return PendingCredential{}, s.reauth(ctx, "刷新凭据被拒绝")
		}
		return PendingCredential{}, err
	}
	raw, _ := json.Marshal(next)
	if err := s.store.Save(ctx, raw); err != nil {
		return PendingCredential{}, s.reauth(ctx, "无法安全保存刷新结果")
	}
	if err := s.store.MarkHealthy(ctx); err != nil {
		return PendingCredential{}, fmt.Errorf("刷新成功但来源健康状态更新失败: %w", err)
	}
	return next, nil
}

func (s *ManagedSession) reauth(ctx context.Context, reason string) error {
	if s.store != nil {
		if err := s.store.MarkReauthRequired(ctx); err != nil {
			return fmt.Errorf("%w: %s；来源状态更新失败: %v",
				ErrManagedReauthRequired, reason, err)
		}
	}
	return fmt.Errorf("%w: %s", ErrManagedReauthRequired, reason)
}

func (s *ManagedSession) requestRefresh(ctx context.Context, current PendingCredential) (PendingCredential, error) {
	domain, err := normalizeVerificationDomain(current.Domain, s.baseURL)
	if err != nil {
		return PendingCredential{}, fmt.Errorf("%w: 凭据 domain 无效",
			ErrManagedReauthRequired)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.baseURL+"/v2/plugin/auth/token/refresh", bytes.NewReader([]byte("{}")))
	if err != nil {
		return PendingCredential{}, fmt.Errorf("%w: 无法创建刷新请求",
			ErrManagedRetryable)
	}
	setBaseHeaders(req)
	req.Header.Set("X-Refresh-Token", current.RefreshToken)
	req.Header.Set("X-Auth-Refresh-Source", "plugin")
	req.Header.Set("X-Domain", domain)
	if current.EnterpriseID != "" {
		req.Header.Set("X-Enterprise-Id", current.EnterpriseID)
		req.Header.Set("X-Tenant-Id", current.EnterpriseID)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return PendingCredential{}, fmt.Errorf("%w: 网络失败", ErrManagedRetryable)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil || len(body) > maxResponseSize {
		return PendingCredential{}, fmt.Errorf("%w: 无法读取刷新响应",
			ErrManagedRetryable)
	}
	var env refreshEnvelope
	parseErr := json.Unmarshal(body, &env)
	code, hasCode := refreshBusinessCode(env.Code)
	errorText := strings.ToLower(string(body))
	if parseErr == nil {
		errorText += " " + strings.ToLower(env.errorText())
	}
	if resp.StatusCode == http.StatusUnauthorized ||
		resp.StatusCode == http.StatusForbidden ||
		(hasCode && code == 12153) ||
		strings.Contains(errorText, "invalid_grant") ||
		strings.Contains(errorText, "token format error") {
		return PendingCredential{}, fmt.Errorf("%w: 刷新凭据被拒绝 (HTTP %d)",
			ErrManagedReauthRequired, resp.StatusCode)
	}
	if resp.StatusCode == http.StatusRequestTimeout ||
		resp.StatusCode == http.StatusTooManyRequests ||
		resp.StatusCode >= 500 {
		return PendingCredential{}, fmt.Errorf("%w: 刷新暂时失败 (HTTP %d)",
			ErrManagedRetryable, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return PendingCredential{}, fmt.Errorf("%w: 刷新返回 HTTP %d",
			ErrManagedRetryable, resp.StatusCode)
	}
	if parseErr != nil {
		return PendingCredential{}, fmt.Errorf("%w: 暂时无法解析刷新响应",
			ErrManagedRetryable)
	}
	if hasCode && code != 0 {
		return PendingCredential{}, fmt.Errorf("%w: 刷新业务码 %d",
			ErrManagedRetryable, code)
	}
	var got tokenData
	if err := json.Unmarshal(env.Data, &got); err != nil || got.AccessToken == "" {
		return PendingCredential{}, fmt.Errorf("%w: 刷新响应缺少 access token",
			ErrManagedRetryable)
	}
	return mergeManagedToken(current, got, s.now()), nil
}

type refreshEnvelope struct {
	Code    json.RawMessage `json:"code"`
	Msg     string          `json:"msg"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
	Error   json.RawMessage `json:"error"`
}

func (e refreshEnvelope) errorText() string {
	parts := []string{e.Msg, e.Message}
	var text string
	if json.Unmarshal(e.Error, &text) == nil {
		parts = append(parts, text)
	}
	var nested struct {
		Message string          `json:"message"`
		Type    string          `json:"type"`
		Code    json.RawMessage `json:"code"`
	}
	if json.Unmarshal(e.Error, &nested) == nil {
		parts = append(parts, nested.Message, nested.Type, string(nested.Code))
	}
	return strings.Join(parts, " ")
}

func refreshBusinessCode(raw json.RawMessage) (int, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, false
	}
	var code int
	if json.Unmarshal(raw, &code) == nil {
		return code, true
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		code, err := strconv.Atoi(strings.TrimSpace(text))
		return code, err == nil
	}
	return 0, false
}

func mergeManagedToken(current PendingCredential, got tokenData, now time.Time) PendingCredential {
	current.AccessToken = got.AccessToken
	if got.RefreshToken != "" {
		current.RefreshToken = got.RefreshToken
	}
	if got.ExpiresAt > 0 {
		current.AccessExpiresAt = got.ExpiresAt
	} else if got.ExpiresIn > 0 {
		current.AccessExpiresAt = now.Add(time.Duration(got.ExpiresIn) * time.Second).UnixMilli()
	}
	if got.RefreshExpiresAt > 0 {
		current.RefreshExpiresAt = got.RefreshExpiresAt
	} else if got.RefreshExpiresIn > 0 {
		current.RefreshExpiresAt = now.Add(time.Duration(got.RefreshExpiresIn) * time.Second).UnixMilli()
	}
	if got.Domain != "" {
		current.Domain = got.Domain
	}
	if got.TokenType != "" {
		current.TokenType = got.TokenType
	}
	return current
}

func needsManagedRefresh(credential PendingCredential, now time.Time) bool {
	return credential.AccessExpiresAt > 0 &&
		now.UnixMilli() >= credential.AccessExpiresAt-managedRefreshLead.Milliseconds()
}

func refreshableManaged(credential PendingCredential, now time.Time) bool {
	return credential.RefreshToken != "" &&
		(credential.RefreshExpiresAt <= 0 || now.UnixMilli() < credential.RefreshExpiresAt)
}

func readRequestBody(req *http.Request) ([]byte, bool, error) {
	if req.Body == nil {
		return nil, false, nil
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, false, err
	}
	_ = req.Body.Close()
	return body, true, nil
}

func discardAndClose(body io.ReadCloser) {
	if body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(body, maxResponseSize+1))
	_ = body.Close()
}

func (s *ManagedSession) send(
	ctx context.Context,
	original *http.Request,
	credential PendingCredential,
	body []byte,
	hasBody bool,
) (*http.Response, error) {
	req := original.Clone(ctx)
	if original.Header == nil {
		req.Header = make(http.Header)
	} else {
		req.Header = original.Header.Clone()
	}
	if hasBody {
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.ContentLength = int64(len(body))
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(body)), nil
		}
	} else {
		req.Body = nil
		req.ContentLength = 0
		req.GetBody = nil
	}
	req.Header.Set("Authorization", "Bearer "+credential.AccessToken)
	domain, err := normalizeVerificationDomain(credential.Domain, s.baseURL)
	if err != nil {
		return nil, s.reauth(ctx, "凭据 domain 无效")
	}
	req.Header.Set("X-Domain", domain)
	req.Header.Set("X-User-Id", credential.UID)
	req.Header.Set("X-Product", "SaaS")
	req.Header.Set("X-IDE-Type", "WorkBuddy")
	if credential.EnterpriseID != "" {
		req.Header.Set("X-Enterprise-Id", credential.EnterpriseID)
		req.Header.Set("X-Tenant-Id", credential.EnterpriseID)
	}
	if req.URL != nil {
		req.URL = cloneURL(req.URL)
	}
	return s.client.Do(req)
}

func cloneURL(u *url.URL) *url.URL {
	copied := *u
	return &copied
}
