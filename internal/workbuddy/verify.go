package workbuddy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	// ErrPendingCredentialNotFound 表示待验证登录结果不存在或已清理。
	ErrPendingCredentialNotFound = errors.New("待验证登录结果不存在")

	errVerificationAuth   = errors.New("WorkBuddy CN 登录态已失效")
	errVerificationRetry  = errors.New("WorkBuddy CN 只读验证暂时不可用")
	errVerificationFailed = errors.New("WorkBuddy CN 只读验证失败")
)

// VerifiedAccount is the credential-free account projection accepted after a
// successful read-only verification.
type VerifiedAccount struct {
	Target         string
	AccountID      string
	SourceID       string
	DisplayName    string
	MaskedIdentity string
}

// VerificationSink persists the managed credential and idempotently upserts
// the verified account/source. It must not alter an existing account's enabled
// or pinned state.
//
// created 为 false 表示该账号此前已存在（本次只刷新了凭据）。上层用它把结论说准：
// 用户连点两次登录时，第二次应当看到「该账号已存在」，而不是又一句「已添加」。
type VerificationSink interface {
	AcceptVerifiedAccount(ctx context.Context, account VerifiedAccount, credential []byte) (created bool, err error)
}

// VerificationOutcome is a credential-free state suitable for the GUI.
type VerificationOutcome struct {
	Status  LoginStatus
	Message string
}

// VerificationConfig injects the read-only endpoints, secure pending store,
// and account/credential persistence seam.
type VerificationConfig struct {
	BaseURL      string
	HTTPClient   *http.Client
	PendingStore PendingStore
	Sink         VerificationSink
}

// VerificationService verifies a browser-login credential using only the
// accounts and meter-summary endpoints.
type VerificationService struct {
	baseURL string
	client  *http.Client
	store   PendingStore
	sink    VerificationSink
}

// NewVerificationService constructs the WorkBuddy CN read-only verifier.
func NewVerificationService(cfg VerificationConfig) *VerificationService {
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &VerificationService{
		baseURL: baseURL,
		client:  cfg.HTTPClient,
		store:   cfg.PendingStore,
		sink:    cfg.Sink,
	}
}

// Verify performs the read-only check and only hands the credential to the
// sink after the official login identity and balance reads succeed.
func (s *VerificationService) Verify(ctx context.Context, credential PendingCredential) VerificationOutcome {
	credential.UID = strings.TrimSpace(credential.UID)
	credential.EnterpriseID = strings.TrimSpace(credential.EnterpriseID)
	if credential.Target != "workbuddy-cn" || credential.SessionID == "" ||
		credential.AccessToken == "" || credential.UID == "" {
		return s.discardPending(credential.SessionID,
			failedVerification("登录结果不完整，请重新登录；未创建账号"))
	}

	domain, err := normalizeVerificationDomain(credential.Domain, s.baseURL)
	if err != nil {
		return s.discardPending(credential.SessionID,
			failedVerification("登录域名无效，请重新登录；未创建账号"))
	}

	// Magpie trusts the UID returned by /v2/plugin/login/account and uses the
	// account list only to fill an enterprise id the login response omitted.
	// Personal accounts need no account-list lookup at all.
	if credential.EnterpriseID == "" && needsEnterpriseLookup(credential) {
		if accounts, err := s.readAccounts(ctx, credential, domain); err == nil {
			credential.EnterpriseID = inferEnterpriseID(credential, accounts)
		}
	}
	if err := s.readBalance(ctx, credential, domain); err != nil {
		return s.failure(credential.SessionID, err)
	}

	account := verifiedAccount(credential)
	raw, _ := json.Marshal(credential)
	if s.sink == nil {
		return retryableVerification("账号存储暂不可用，请重试；未创建账号")
	}
	created, err := s.sink.AcceptVerifiedAccount(ctx, account, raw)
	if err != nil {
		return retryableVerification("无法保存已验证账号，请重试；未创建账号")
	}
	if err := s.deletePendingStrict(ctx, credential.SessionID); err != nil {
		return retryableVerification("账号已验证，但临时登录凭据清理失败，请重试")
	}
	message := "只读验证通过，已添加并启用账号"
	if !created {
		message = "只读验证通过，该账号此前已存在，已刷新登录凭据"
	}
	return VerificationOutcome{
		Status:  LoginVerified,
		Message: message,
	}
}

func (s *VerificationService) readAccounts(ctx context.Context, credential PendingCredential, domain string) ([]accountData, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+"/v2/plugin/accounts", nil)
	if err != nil {
		return nil, errVerificationRetry
	}
	setBaseHeaders(req)
	req.Header.Set("Authorization", "Bearer "+credential.AccessToken)
	req.Header.Set("X-Domain", domain)

	env, err := s.readEnvelope(req)
	if err != nil {
		return nil, err
	}
	var data struct {
		Accounts []accountData `json:"accounts"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		return nil, errVerificationRetry
	}
	return data.Accounts, nil
}

func (s *VerificationService) readBalance(ctx context.Context, credential PendingCredential, domain string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.baseURL+"/billing/meter/get-user-resource-summary", bytes.NewReader([]byte("{}")))
	if err != nil {
		return errVerificationRetry
	}
	setBaseHeaders(req)
	req.Header.Set("Authorization", "Bearer "+credential.AccessToken)
	req.Header.Set("X-User-Id", credential.UID)
	req.Header.Set("X-Domain", domain)
	req.Header.Set("X-Product", "SaaS")
	req.Header.Set("X-IDE-Type", "WorkBuddy")
	if credential.EnterpriseID != "" {
		req.Header.Set("X-Enterprise-Id", credential.EnterpriseID)
		req.Header.Set("X-Tenant-Id", credential.EnterpriseID)
	}
	_, err = s.readEnvelope(req)
	return err
}

func (s *VerificationService) readEnvelope(req *http.Request) (envelope, error) {
	resp, err := s.client.Do(req)
	if err != nil {
		return envelope{}, errVerificationRetry
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return envelope{}, errVerificationAuth
	}
	if resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusTooManyRequests ||
		resp.StatusCode >= 500 {
		return envelope{}, errVerificationRetry
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return envelope{}, errVerificationFailed
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil || len(body) > maxResponseSize {
		return envelope{}, errVerificationRetry
	}
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return envelope{}, errVerificationRetry
	}
	if env.Code != 0 {
		return envelope{}, errVerificationFailed
	}
	return env, nil
}

func (s *VerificationService) failure(sessionID string, err error) VerificationOutcome {
	switch {
	case errors.Is(err, errVerificationAuth):
		return s.discardPending(sessionID, VerificationOutcome{
			Status:  LoginReauthRequired,
			Message: "登录态已失效，请重新登录；未创建账号",
		})
	case errors.Is(err, errVerificationRetry):
		return retryableVerification("无法完成只读验证，请检查网络后重试；未创建账号")
	default:
		return s.discardPending(sessionID,
			failedVerification("只读验证失败，请重新登录；未创建账号"))
	}
}

func failedVerification(message string) VerificationOutcome {
	return VerificationOutcome{Status: LoginFailed, Message: message}
}

func retryableVerification(message string) VerificationOutcome {
	return VerificationOutcome{Status: LoginRetryable, Message: message}
}

func (s *VerificationService) discardPending(
	sessionID string,
	outcome VerificationOutcome,
) VerificationOutcome {
	if err := s.deletePending(sessionID); err != nil {
		outcome.Status = LoginRetryable
		outcome.Message = strings.TrimSpace(outcome.Message) +
			"；临时登录凭据清理失败，请重试"
	}
	return outcome
}

func (s *VerificationService) deletePending(sessionID string) error {
	if s.store == nil || sessionID == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return ignoreMissingPendingCredential(s.store.Delete(ctx, sessionID))
}

func (s *VerificationService) deletePendingStrict(ctx context.Context, sessionID string) error {
	if s.store == nil {
		return ErrPendingCredentialNotFound
	}
	return s.store.Delete(ctx, sessionID)
}

func normalizeVerificationDomain(raw, baseURL string) (string, error) {
	candidate := strings.TrimSpace(raw)
	if candidate == "" {
		u, err := url.Parse(baseURL)
		if err != nil || u.Host == "" {
			return "", errVerificationFailed
		}
		candidate = u.Host
	}
	if strings.ContainsAny(candidate, " \t\r\n/\\?#@") {
		return "", errVerificationFailed
	}
	u, err := url.Parse("//" + candidate)
	if err != nil || u.Host == "" || u.Hostname() == "" || u.Path != "" ||
		u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", errVerificationFailed
	}
	return strings.TrimSuffix(strings.ToLower(u.Host), "."), nil
}

func needsEnterpriseLookup(credential PendingCredential) bool {
	identityType := strings.ToLower(strings.TrimSpace(credential.IdentityType))
	return identityType != "" && identityType != "personal"
}

func inferEnterpriseID(credential PendingCredential, accounts []accountData) string {
	var mine []accountData
	for _, account := range accounts {
		account.UID = strings.TrimSpace(account.UID)
		account.EnterpriseID = strings.TrimSpace(account.EnterpriseID)
		if account.UID != credential.UID || account.EnterpriseID == "" {
			continue
		}
		mine = append(mine, account)
	}

	identityType := strings.ToLower(strings.TrimSpace(credential.IdentityType))
	var sameType, lastLogin []accountData
	for _, account := range mine {
		if strings.ToLower(strings.TrimSpace(account.Type)) == identityType {
			sameType = append(sameType, account)
		}
		if jsonTruthy(account.LastLogin) {
			lastLogin = append(lastLogin, account)
		}
	}
	switch {
	case len(sameType) == 1:
		return sameType[0].EnterpriseID
	case len(lastLogin) == 1:
		return lastLogin[0].EnterpriseID
	case len(mine) == 1:
		return mine[0].EnterpriseID
	default:
		return ""
	}
}

func jsonTruthy(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.TrimSpace(typed) != ""
	case float64:
		return typed != 0
	case []any:
		return len(typed) != 0
	case map[string]any:
		return len(typed) != 0
	default:
		return false
	}
}

func verifiedAccount(credential PendingCredential) VerifiedAccount {
	sum := sha256.Sum256([]byte("workbuddy-cn\x00" + credential.UID + "\x00" + credential.EnterpriseID))
	suffix := hex.EncodeToString(sum[:16])
	masked := maskIdentity(credential.UID, credential.PhoneNumber)
	return VerifiedAccount{
		Target:         credential.Target,
		AccountID:      "wb_" + suffix,
		SourceID:       "wbm_" + suffix,
		DisplayName:    masked,
		MaskedIdentity: masked,
	}
}
