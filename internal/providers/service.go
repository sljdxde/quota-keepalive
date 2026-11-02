// Package providers ports Magpie's OpenCode provider runtime into QKA and
// owns provider/account persistence, login, quota, check-in, and keepalive.
package providers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"qka/internal/accounts"
	"qka/internal/capabilities"
	"qka/internal/engine"
	"qka/internal/features"
	"qka/internal/privacy"
	"qka/internal/providerruntime"
	"qka/internal/requestguard"
)

// Runtime is the provider host surface used by the service.
type Runtime interface {
	Providers(context.Context) ([]providerruntime.Provider, error)
	Prompt(context.Context, string, int, map[string]string) (providerruntime.Prompt, error)
	Authorize(context.Context, string, int, map[string]string, string) (providerruntime.Authorization, error)
	Finish(context.Context, string, string) (providerruntime.Saved, error)
	APIKey(context.Context, string, int, map[string]string, string, string) (providerruntime.Saved, error)
	Usage(context.Context, string, string) (providerruntime.Usage, error)
	Checkin(context.Context, string, string) (providerruntime.Checkin, error)
	Load(context.Context, string, string) (providerruntime.Loader, error)
	Fetch(context.Context, providerruntime.FetchRequest) (providerruntime.FetchResponse, error)
	SignOut(context.Context, string, string) error
	ReadAuth() ([]byte, error)
	RemoveAuth() error
	Close() error
}

// RuntimeFactory starts one short-lived provider host.
type RuntimeFactory func(context.Context) (Runtime, error)

type runtimeDiagnostics interface {
	LoadedPlugins() []providerruntime.LoadedPlugin
}

// ErrLoginPromptRequired reports that sign-in needs more input.
var ErrLoginPromptRequired = errors.New("providers: login prompt required")

// ErrNotExecuted reports an idempotent skip or a cooldown without claiming
// that the requested provider action ran.
var ErrNotExecuted = errors.New("providers: operation not executed")

// MethodView is a generic sign-in method.
type MethodView struct {
	Type        string `json:"type"`
	Label       string `json:"label"`
	Placeholder string `json:"placeholder,omitempty"`
	Keyless     bool   `json:"keyless,omitempty"`
}

// ModelView is the stable, non-secret model metadata shown by the GUI.
type ModelView struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Free    bool    `json:"free"`
	Context int     `json:"context"`
	Rate    float64 `json:"rate"`
}

// AccountView is one provider account and today's operation state.
type AccountView struct {
	Key             string          `json:"key"`
	MaskedIdentity  string          `json:"masked_identity"`
	Type            string          `json:"type"`
	Enabled         bool            `json:"enabled"`
	Models          []string        `json:"models"`
	TaskPreferences map[string]bool `json:"task_preferences"`
	Operations      []OperationView `json:"operations"`
}

// OperationView is one automatic/manual operation for an account.
type OperationView struct {
	Operation   string   `json:"operation"`
	State       string   `json:"state"`
	TaskEnabled bool     `json:"task_enabled"`
	Credits     *float64 `json:"credits,omitempty"`
	Balance     *float64 `json:"balance,omitempty"`
	Message     string   `json:"message,omitempty"`
}

// ProviderView is one provider and its accounts.
type ProviderView struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Icon      string        `json:"icon,omitempty"`
	Enabled   bool          `json:"enabled"`
	SignedIn  bool          `json:"signed_in"`
	Usage     bool          `json:"usage"`
	Checkin   bool          `json:"checkin"`
	Keepalive bool          `json:"keepalive"`
	Methods   []MethodView  `json:"methods"`
	Models    []ModelView   `json:"models"`
	Accounts  []AccountView `json:"accounts"`
}

// View is the complete provider page projection.
type View struct {
	Providers    []ProviderView                 `json:"providers"`
	PluginErrors []providerruntime.LoadedPlugin `json:"plugin_errors,omitempty"`
	AccountCount int                            `json:"account_count"`
	EnabledCount int                            `json:"enabled_count"`
}

// LoginView is exposed to the generic provider login dialog.
type LoginView struct {
	Provider     string                  `json:"provider,omitempty"`
	MethodIndex  int                     `json:"method_index,omitempty"`
	Status       string                  `json:"status"`
	Session      string                  `json:"session,omitempty"`
	URL          string                  `json:"url,omitempty"`
	Instructions string                  `json:"instructions,omitempty"`
	Method       string                  `json:"method,omitempty"`
	Prompt       *providerruntime.Prompt `json:"prompt,omitempty"`
	Saved        *providerruntime.Saved  `json:"saved,omitempty"`
	Message      string                  `json:"message,omitempty"`
}

// ModelTestView is one bounded model connectivity probe. It never includes
// upstream response text, tokens, headers, or other credential-bearing data.
type ModelTestView struct {
	Provider string `json:"provider"`
	Account  string `json:"account"`
	Model    string `json:"model"`
	OK       bool   `json:"ok"`
	Status   int    `json:"status"`
	Latency  int64  `json:"latency_ms"`
	Message  string `json:"message"`
}

// RunItemView is one redacted provider/account result in run history.
type RunItemView struct {
	RunID      string   `json:"run_id"`
	Provider   string   `json:"provider"`
	Account    string   `json:"account"`
	Action     string   `json:"action"`
	State      string   `json:"state"`
	Message    string   `json:"message,omitempty"`
	Credits    *float64 `json:"credits,omitempty"`
	Balance    *float64 `json:"balance,omitempty"`
	Model      string   `json:"model,omitempty"`
	ObservedAt string   `json:"observed_at"`
}

// QuotaReadingView is one bounded quota observation for trend display.
type QuotaReadingView struct {
	ObservedAt string                        `json:"observed_at"`
	Plan       string                        `json:"plan,omitempty"`
	Until      string                        `json:"until,omitempty"`
	Balance    string                        `json:"balance,omitempty"`
	Windows    []providerruntime.UsageWindow `json:"windows"`
}

// RunSummary is the redacted aggregate of one provider execution pass.
type RunSummary struct {
	Attempted      int `json:"attempted"`
	Succeeded      int `json:"succeeded"`
	NeedsAttention int `json:"needs_attention"`
	Skipped        int `json:"skipped"`
}

type runResult struct {
	Provider string
	Account  string
	Action   string
	State    string
	Executed bool
	Message  string
	Credits  *float64
	Balance  *float64
	Model    string
	Err      error
}

// Service owns provider runtime lifecycle and SQLite projections.
type Service struct {
	db          *sql.DB
	accounts    *accounts.Service
	credentials *capabilities.CredentialService
	engine      *engine.Engine
	factory     RuntimeFactory
	now         func() time.Time

	mu      sync.Mutex
	host    Runtime
	authRef capabilities.CredentialRef
	runMu   sync.Mutex

	creditMu     sync.Mutex
	creditSynced map[string]bool

	riskGuard *requestguard.Guard
}

// NewService constructs the provider service.
func NewService(
	db *sql.DB,
	accountService *accounts.Service,
	credentials *capabilities.CredentialService,
	eng *engine.Engine,
	factory RuntimeFactory,
	now func() time.Time,
) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{
		db: db, accounts: accountService, credentials: credentials, engine: eng,
		factory: factory, now: now,
		authRef: capabilities.CredentialRef{
			Target: "qka-provider", AccountID: "runtime", SourceID: "auth",
			Kind: capabilities.Managed,
		},
	}
}

// SetRiskGuard installs the shared manual/scheduled request gate. A nil guard
// preserves the service's historical behavior for focused unit tests.
func (s *Service) SetRiskGuard(guard *requestguard.Guard) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.riskGuard = guard
}

func (s *Service) runtime(ctx context.Context) (Runtime, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.host != nil {
		return s.host, nil
	}
	if s.factory == nil {
		return nil, errors.New("provider runtime is unavailable")
	}
	host, err := s.factory(ctx)
	if err != nil {
		return nil, err
	}
	s.host = host
	return host, nil
}

func (s *Service) persistAuth(ctx context.Context, host Runtime) error {
	data, err := host.ReadAuth()
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var value map[string]any
	if len(data) > 0 {
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
	}
	if len(value) == 0 {
		if s.credentials != nil {
			if err := s.credentials.DeleteManaged(ctx, s.authRef.Target, s.authRef.AccountID, s.authRef.SourceID); err != nil &&
				!errors.Is(err, capabilities.ErrSecretNotFound) {
				return err
			}
		}
		return nil
	}
	if s.credentials == nil {
		return errors.New("credential service is unavailable")
	}
	if err := s.credentials.Persist(ctx, s.authRef, string(data)); err != nil {
		return err
	}
	// The host keeps using this process-private auth view. Removing it now
	// would make the next List/Usage/Checkin call in the same session look
	// signed out. Close removes it after the final snapshot is persisted.
	return nil
}

func (s *Service) runtimeProvider(
	ctx context.Context,
	host Runtime,
	provider string,
) (providerruntime.Provider, error) {
	items, err := host.Providers(ctx)
	if err != nil {
		return providerruntime.Provider{}, err
	}
	for _, item := range items {
		if item.ID == provider {
			return item, nil
		}
	}
	return providerruntime.Provider{}, fmt.Errorf("未知供应商: %s", provider)
}

func checkOperationCapability(provider providerruntime.Provider, operation string) error {
	supported := false
	switch operation {
	case "checkin":
		supported = provider.Checkin
	case "keepalive":
		supported = providerKeepaliveSupported(provider)
	case "refresh":
		supported = provider.Usage
	default:
		return fmt.Errorf("unsupported provider operation %q", operation)
	}
	if !supported {
		return fmt.Errorf("%s 不支持操作 %s", provider.Name, operation)
	}
	return nil
}

func (s *Service) verifyLogin(
	ctx context.Context,
	host Runtime,
	saved providerruntime.Saved,
) error {
	provider, err := s.runtimeProvider(ctx, host, saved.Provider)
	if err != nil {
		return fmt.Errorf("登录验证失败: %w", err)
	}
	if !provider.Usage {
		return nil
	}
	usage, err := host.Usage(ctx, saved.Provider, saved.Account)
	if err != nil {
		return fmt.Errorf("登录验证失败: %w", err)
	}
	if strings.TrimSpace(usage.Error) != "" {
		return fmt.Errorf("登录验证失败: %s", redactMessage(usage.Error))
	}
	return nil
}

// rollbackLogin removes a newly-created temporary sign-in and shuts down the
// host when read-only verification fails. Previously persisted accounts are
// preserved only after the host confirms the failed account was signed out.
func (s *Service) rollbackLogin(
	ctx context.Context,
	host Runtime,
	saved providerruntime.Saved,
) error {
	signOutErr := host.SignOut(ctx, saved.Provider, saved.Account)
	var persistErr error
	if signOutErr == nil {
		persistErr = s.persistAuth(ctx, host)
	}
	removeErr := host.RemoveAuth()
	closeErr := host.Close()
	s.mu.Lock()
	if s.host == host {
		s.host = nil
	}
	s.mu.Unlock()
	return errors.Join(signOutErr, persistErr, removeErr, closeErr)
}

// ensureTestableAccount 用于「模型测试」这类只读诊断：它**不要求**供应商或
// 账号处于启用状态——用户恰恰可能在停用之后用一次测试来确认登录态和模型
// 连通性。这里只校验账号确实存在，避免对不存在的账号发起外呼。
func (s *Service) ensureTestableAccount(ctx context.Context, provider, account string) error {
	account = strings.TrimSpace(account)
	if account == "" {
		return nil
	}
	var exists int
	err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM accounts WHERE target = ? AND id = ?`,
		provider, account).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		// 账号尚未落库时交给后续 runtime 校验（foundAccount），
		// 这里不额外拦截，保持与旧诊断路径一致。
		return nil
	}
	return err
}

func (s *Service) ensureEnabled(ctx context.Context, provider, account string) error {
	var providerEnabled int
	err := s.db.QueryRowContext(ctx,
		`SELECT enabled FROM providers WHERE id = ?`, provider).Scan(&providerEnabled)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && providerEnabled != 1 {
		return fmt.Errorf("供应商已停用: %s", provider)
	}
	if strings.TrimSpace(account) == "" {
		return nil
	}
	var accountEnabled int
	err = s.db.QueryRowContext(ctx,
		`SELECT enabled FROM accounts WHERE target = ? AND id = ?`,
		provider, account).Scan(&accountEnabled)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && accountEnabled != 1 {
		return fmt.Errorf("账号已停用: %s", account)
	}
	return nil
}

// Close persists the temporary sign-in view and stops the provider host.
func (s *Service) Close() error {
	s.mu.Lock()
	host := s.host
	s.host = nil
	s.mu.Unlock()
	if host == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	persistErr := s.persistAuth(ctx, host)
	removeErr := host.RemoveAuth()
	closeErr := host.Close()
	return errors.Join(persistErr, removeErr, closeErr)
}

// List synchronizes provider metadata and accounts, then returns the page view.
func (s *Service) List(ctx context.Context) (View, error) {
	host, err := s.runtime(ctx)
	if err != nil {
		return View{}, err
	}
	items, err := host.Providers(ctx)
	if err != nil {
		return View{}, err
	}
	for _, provider := range items {
		if err := s.upsertProvider(ctx, provider); err != nil {
			return View{}, err
		}
		if err := s.syncAccounts(ctx, provider); err != nil {
			return View{}, err
		}
	}
	view, err := s.view(ctx, items)
	if err != nil {
		return View{}, err
	}
	s.reconcileMissingCheckinGrowth(ctx, host, &view)
	if diagnostics, ok := host.(runtimeDiagnostics); ok {
		for _, plugin := range diagnostics.LoadedPlugins() {
			if plugin.Error != "" {
				view.PluginErrors = append(view.PluginErrors, plugin)
			}
		}
	}
	return view, nil
}

// reconcileMissingCheckinGrowth asks a check-in plugin for today's grant when
// an older row only knew the account balance. The plugin check-in is
// idempotent after the day is in; the in-memory cache keeps this to one read
// per account and Beijing day for each app process.
func (s *Service) reconcileMissingCheckinGrowth(
	ctx context.Context, host Runtime, view *View,
) {
	day := engine.BeijingDay(s.now())
	for pi := range view.Providers {
		provider := &view.Providers[pi]
		if !provider.Enabled || !provider.Checkin {
			continue
		}
		for ai := range provider.Accounts {
			account := &provider.Accounts[ai]
			if !account.Enabled {
				continue
			}
			for oi := range account.Operations {
				op := &account.Operations[oi]
				if !isCheckinCreditState(op.State) ||
					(op.Credits != nil && op.Balance != nil && *op.Credits != *op.Balance) {
					continue
				}
				cacheKey := day + "\x00" + provider.ID + "\x00" + account.Key
				s.creditMu.Lock()
				if s.creditSynced == nil {
					s.creditSynced = map[string]bool{}
				}
				if s.creditSynced[cacheKey] {
					s.creditMu.Unlock()
					continue
				}
				s.creditSynced[cacheKey] = true
				s.creditMu.Unlock()

				// 这里同样经过共享风控闸门：日界键保证同一天只共享一次结果，
				// 若上游返回 429/401/验证码，guard 会为该账号进入冷却，避免
				// 状态页反复刷新时持续打上游。成功结果按天缓存，不增加外呼量。
				upstream, err := requestguard.Do(ctx, s.riskGuard, provider.ID, account.Key,
					"checkin_reconcile:"+day, 5*time.Minute,
					func() (providerruntime.Checkin, error) {
						return host.Checkin(ctx, provider.ID, account.Key)
					})
				if err != nil || upstream.Outcome != "claimed" && upstream.Outcome != "done" ||
					upstream.Credit <= 0 {
					continue
				}
				credit := upstream.Credit
				key := engine.Key{
					Target: provider.ID, AccountID: account.Key, Operation: engine.Checkin,
				}
				if err := s.engine.RecordUsage(ctx, key, &credit, nil); err != nil {
					continue
				}
				message := redactMessage(upstream.Message)
				if err := s.engine.ReconcileState(
					ctx, key, engine.State(op.State), message,
				); err != nil {
					continue
				}
				op.Credits = &credit
				op.Message = message
			}
		}
	}
}

func isCheckinCreditState(state string) bool {
	switch state {
	case "signed_confirmed", "signed_provisional", "already":
		return true
	default:
		return false
	}
}

func (s *Service) upsertProvider(ctx context.Context, p providerruntime.Provider) error {
	models, _ := json.Marshal(p.Models)
	ts := s.now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO providers
  (id, name, plugin_spec, icon, enabled, usage, checkin, keepalive, models_json, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  name = excluded.name,
  plugin_spec = excluded.plugin_spec,
  icon = excluded.icon,
  usage = excluded.usage,
  checkin = excluded.checkin,
  keepalive = excluded.keepalive,
  models_json = excluded.models_json,
  updated_at = excluded.updated_at`,
		p.ID, p.Name, p.Spec, p.Icon, boolInt(defaultProviderEnabled(p.ID)),
		boolInt(p.Usage), boolInt(p.Checkin),
		boolInt(providerKeepaliveSupported(p)), string(models), ts, ts)
	return err
}

// defaultProviderEnabled keeps bundled providers available for opt-in while
// making WorkBuddy the only provider enabled on a fresh installation.
func defaultProviderEnabled(providerID string) bool {
	return providerID == "workbuddy"
}

func providerKeepaliveSupported(p providerruntime.Provider) bool {
	if !p.Fetch {
		return false
	}
	if strings.TrimSpace(p.APIURL) != "" {
		return true
	}
	for _, model := range p.Models {
		if strings.TrimSpace(model.APIURL) != "" {
			return true
		}
	}
	return false
}

func (s *Service) syncAccounts(ctx context.Context, p providerruntime.Provider) error {
	found := make([]accounts.DiscoveredAccount, 0, len(p.Accounts))
	runtimeKeys := make(map[string]bool, len(p.Accounts))
	for _, account := range p.Accounts {
		if err := s.reconcileAccountIdentity(ctx, p.ID, account); err != nil {
			return err
		}
		runtimeKeys[account.Key] = true
		found = append(found, accounts.DiscoveredAccount{
			ID:             account.Key,
			DisplayName:    p.Name,
			MaskedIdentity: maskIdentity(firstNonEmpty(account.AccountID, account.Hint, account.Key)),
			SourceID:       "plugin:" + account.Key,
			SourceKind:     accounts.KindManaged,
			Health:         accounts.HealthHealthy,
		})
	}
	if len(found) > 0 {
		if err := s.accounts.Discover(ctx, p.ID, found); err != nil {
			return err
		}
	}
	return s.pruneMissingPluginSources(ctx, p.ID, runtimeKeys)
}

func (s *Service) reconcileAccountIdentity(
	ctx context.Context,
	provider string,
	account providerruntime.Account,
) error {
	identity := strings.TrimSpace(account.AccountID)
	if identity == "" || strings.TrimSpace(account.Key) == "" {
		return nil
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(provider+"\x00"+identity)))
	var previous string
	err := s.db.QueryRowContext(ctx, `
SELECT identity_hash FROM provider_account_identities
WHERE provider = ? AND slot_key = ?`,
		provider, account.Key).Scan(&previous)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = s.db.ExecContext(ctx, `
INSERT INTO provider_account_identities
  (provider, slot_key, identity_hash, updated_at)
VALUES (?, ?, ?, ?)`,
			provider, account.Key, hash, s.now().UTC().Format(time.RFC3339))
		return err
	}
	if err != nil || previous == hash {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, stmt := range []string{
		`DELETE FROM logs WHERE target = ? AND account_id = ?`,
		`DELETE FROM quota_readings WHERE provider = ? AND account_id = ?`,
		`DELETE FROM run_items WHERE provider = ? AND account_id = ?`,
		`DELETE FROM daily_ops WHERE target = ? AND account_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, stmt, provider, account.Key); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
DELETE FROM runs
WHERE NOT EXISTS (SELECT 1 FROM run_items WHERE run_items.run_id = runs.id)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
DELETE FROM accounts WHERE target = ? AND id = ?`,
		provider, account.Key); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE provider_account_identities
SET identity_hash = ?, updated_at = ?
WHERE provider = ? AND slot_key = ?`,
		hash, s.now().UTC().Format(time.RFC3339), provider, account.Key); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) pruneMissingPluginSources(
	ctx context.Context,
	provider string,
	runtimeKeys map[string]bool,
) error {
	stored, err := s.accounts.List(ctx, provider)
	if err != nil {
		return err
	}
	const prefix = "plugin:"
	for _, account := range stored {
		for _, source := range account.Sources {
			if !strings.HasPrefix(source.ID, prefix) || runtimeKeys[strings.TrimPrefix(source.ID, prefix)] {
				continue
			}
			if err := s.accounts.RemoveSource(ctx, provider, account.ID, source.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) view(ctx context.Context, runtimeItems []providerruntime.Provider) (View, error) {
	states, err := s.providerPrefs(ctx)
	if err != nil {
		return View{}, err
	}
	view := View{Providers: []ProviderView{}}
	day := engine.BeijingDay(s.now())
	for _, p := range runtimeItems {
		enabled := true
		if value, ok := states[p.ID]; ok {
			enabled = value
		}
		pv := ProviderView{
			ID: p.ID, Name: p.Name, Icon: p.Icon, Enabled: enabled,
			SignedIn: p.SignedIn, Usage: p.Usage, Checkin: p.Checkin,
			Keepalive: false,
			Methods:   []MethodView{}, Models: []ModelView{},
			Accounts: []AccountView{},
		}
		for _, method := range p.Methods {
			pv.Methods = append(pv.Methods, MethodView{
				Type: method.Type, Label: method.Label,
				Placeholder: method.Placeholder, Keyless: method.Keyless,
			})
		}
		for _, model := range p.Models {
			name := strings.TrimSpace(model.Name)
			if name == "" {
				name = model.ID
			}
			pv.Models = append(pv.Models, ModelView{
				ID: model.ID, Name: name, Free: model.Free,
				Context: model.Context, Rate: model.Rate,
			})
		}
		for _, account := range p.Accounts {
			stored, err := s.accounts.Get(ctx, p.ID, account.Key)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return View{}, err
			}
			av := AccountView{
				Key:            account.Key,
				MaskedIdentity: maskIdentity(firstNonEmpty(account.AccountID, account.Hint, account.Key)),
				Type:           account.Type, Enabled: stored.Enabled,
				Models:          append([]string(nil), account.Models...),
				TaskPreferences: map[string]bool{},
				Operations:      []OperationView{},
			}
			if len(av.Models) == 0 {
				for _, model := range pv.Models {
					av.Models = append(av.Models, model.ID)
				}
			}
			prefs, err := s.accounts.TaskPreferences(ctx, p.ID, account.Key)
			if err != nil {
				return View{}, err
			}
			for _, operation := range []string{"checkin"} {
				on, ok := prefs[operation]
				av.TaskPreferences[operation] = !ok || on
			}
			ops, err := s.operationViews(ctx, p.ID, account.Key, day, av.TaskPreferences)
			if err != nil {
				return View{}, err
			}
			av.Operations = ops
			pv.Accounts = append(pv.Accounts, av)
			view.AccountCount++
		}
		if enabled {
			view.EnabledCount++
		}
		view.Providers = append(view.Providers, pv)
	}
	sort.Slice(view.Providers, func(i, j int) bool {
		if view.Providers[i].SignedIn != view.Providers[j].SignedIn {
			return view.Providers[i].SignedIn
		}
		return view.Providers[i].Name < view.Providers[j].Name
	})
	return view, nil
}

func (s *Service) operationViews(
	ctx context.Context, provider, account, day string, prefs map[string]bool,
) ([]OperationView, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT operation, state, credits, balance, credits_known, balance_known, message
FROM daily_ops WHERE target = ? AND account_id = ? AND day_key = ?`,
		provider, account, day)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byOp := map[string]OperationView{}
	for rows.Next() {
		var op OperationView
		var credits, balance float64
		var creditsKnown, balanceKnown int
		if err := rows.Scan(&op.Operation, &op.State, &credits, &balance,
			&creditsKnown, &balanceKnown, &op.Message); err != nil {
			return nil, err
		}
		if !features.OperationAvailable(op.Operation) {
			continue
		}
		op.TaskEnabled = prefs[op.Operation]
		if creditsKnown == 1 {
			value := credits
			op.Credits = &value
		}
		if balanceKnown == 1 {
			value := balance
			op.Balance = &value
		}
		byOp[op.Operation] = op
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]OperationView, 0, 1)
	for _, operation := range []string{"checkin"} {
		op, ok := byOp[operation]
		if !ok {
			op = OperationView{Operation: operation, State: "", TaskEnabled: prefs[operation]}
		}
		out = append(out, op)
	}
	return out, nil
}

func (s *Service) providerPrefs(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, enabled FROM providers`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		var enabled int
		if err := rows.Scan(&id, &enabled); err != nil {
			return nil, err
		}
		out[id] = enabled == 1
	}
	return out, rows.Err()
}

// SetEnabled toggles one provider.
func (s *Service) SetEnabled(ctx context.Context, provider string, enabled bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE providers SET enabled = ?, updated_at = ? WHERE id = ?`,
		boolInt(enabled), s.now().UTC().Format(time.RFC3339), provider)
	if err != nil {
		s.writeAuditFailure(ctx, provider, "", "provider", err)
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		s.writeAuditFailure(ctx, provider, "", "provider", err)
		return err
	}
	if n == 0 {
		s.writeAuditFailure(ctx, provider, "", "provider", sql.ErrNoRows)
		return sql.ErrNoRows
	}
	s.writeAuditLog(ctx, provider, "", "provider", "completed", "info",
		"供应商已"+enabledLabel(enabled))
	return nil
}

// SetAccountEnabled toggles one account.
func (s *Service) SetAccountEnabled(ctx context.Context, provider, account string, enabled bool) error {
	if err := s.accounts.SetEnabled(ctx, provider, account, enabled); err != nil {
		s.writeAuditFailure(ctx, provider, account, "account", err)
		return err
	}
	s.writeAuditLog(ctx, provider, account, "account", "completed", "info",
		"账号已"+enabledLabel(enabled))
	return nil
}

// SetTaskEnabled toggles one account operation preference.
func (s *Service) SetTaskEnabled(ctx context.Context, provider, account, operation string, enabled bool) error {
	if err := s.accounts.SetTaskEnabled(ctx, provider, account, operation, enabled); err != nil {
		s.writeAuditFailure(ctx, provider, account, "automation", err)
		return err
	}
	s.writeAuditLog(ctx, provider, account, "automation", "completed", "info",
		auditOperationLabel(operation)+"自动执行已"+enabledLabel(enabled))
	return nil
}

// BeginLogin asks the plugin for its next prompt or starts OAuth.
func (s *Service) BeginLogin(
	ctx context.Context, provider string, method int, inputs map[string]string,
) (LoginView, error) {
	host, err := s.runtime(ctx)
	if err != nil {
		s.writeAuditFailure(ctx, provider, "", "login", err)
		return LoginView{}, err
	}
	prompt, err := host.Prompt(ctx, provider, method, inputs)
	if err != nil {
		s.writeAuditFailure(ctx, provider, "", "login", err)
		return LoginView{}, err
	}
	if prompt.Key != "" {
		return LoginView{Provider: provider, MethodIndex: method, Status: "prompt", Prompt: &prompt}, nil
	}
	providers, err := host.Providers(ctx)
	if err != nil {
		s.writeAuditFailure(ctx, provider, "", "login", err)
		return LoginView{}, err
	}
	methodType := ""
	for _, item := range providers {
		if item.ID != provider {
			continue
		}
		if method >= 0 && method < len(item.Methods) {
			methodType = item.Methods[method].Type
		}
		break
	}
	if methodType == "api" {
		return LoginView{
			Provider: provider, MethodIndex: method, Status: "api_key",
			Method: "api", Message: "请输入 API Key 完成登录",
		}, nil
	}
	a, err := host.Authorize(ctx, provider, method, inputs, providerruntime.NewAccount)
	if err != nil {
		s.writeAuditFailure(ctx, provider, "", "login", err)
		return LoginView{}, err
	}
	return LoginView{
		Provider: provider, MethodIndex: method, Status: "authorized", Session: a.Session, URL: a.URL,
		Instructions: a.Instructions, Method: a.Method,
	}, nil
}

// FinishLogin completes the OAuth callback and persists the sign-in.
func (s *Service) FinishLogin(ctx context.Context, session, code string) (LoginView, error) {
	host, err := s.runtime(ctx)
	if err != nil {
		s.writeAuditFailure(ctx, "", "", "login", err)
		return LoginView{}, err
	}
	saved, err := host.Finish(ctx, session, code)
	if err != nil {
		s.writeAuditFailure(ctx, saved.Provider, saved.Account, "login", err)
		return LoginView{}, err
	}
	if err := s.verifyLogin(ctx, host, saved); err != nil {
		s.writeAuditFailure(ctx, saved.Provider, saved.Account, "login", err)
		return LoginView{}, errors.Join(err, s.rollbackLogin(ctx, host, saved))
	}
	if err := s.persistAuth(ctx, host); err != nil {
		s.writeAuditFailure(ctx, saved.Provider, saved.Account, "login", err)
		return LoginView{}, err
	}
	s.writeAuditLog(ctx, saved.Provider, saved.Account, "login", "completed", "info", "登录完成")
	return LoginView{Provider: saved.Provider, Status: "verified", Saved: &saved, Message: "登录完成"}, nil
}

// LoginAPIKey signs in with an API key and persists the sign-in.
func (s *Service) LoginAPIKey(
	ctx context.Context, provider string, method int, inputs map[string]string, key string,
) (LoginView, error) {
	host, err := s.runtime(ctx)
	if err != nil {
		s.writeAuditFailure(ctx, provider, "", "login", err)
		return LoginView{}, err
	}
	saved, err := host.APIKey(ctx, provider, method, inputs, key, providerruntime.NewAccount)
	if err != nil {
		s.writeAuditFailure(ctx, provider, saved.Account, "login", err)
		return LoginView{}, err
	}
	if err := s.verifyLogin(ctx, host, saved); err != nil {
		s.writeAuditFailure(ctx, saved.Provider, saved.Account, "login", err)
		return LoginView{}, errors.Join(err, s.rollbackLogin(ctx, host, saved))
	}
	if err := s.persistAuth(ctx, host); err != nil {
		s.writeAuditFailure(ctx, saved.Provider, saved.Account, "login", err)
		return LoginView{}, err
	}
	s.writeAuditLog(ctx, saved.Provider, saved.Account, "login", "completed", "info", "登录完成")
	return LoginView{Provider: saved.Provider, Status: "verified", Saved: &saved, Message: "登录完成"}, nil
}

// DeleteAccount signs out and removes only QKA's account projection.
func (s *Service) DeleteAccount(ctx context.Context, provider, account string) error {
	host, err := s.runtime(ctx)
	if err != nil {
		s.writeAuditFailure(ctx, provider, account, "account", err)
		return err
	}
	if err := host.SignOut(ctx, provider, account); err != nil {
		s.writeAuditFailure(ctx, provider, account, "account", err)
		return err
	}
	if err := s.persistAuth(ctx, host); err != nil {
		s.writeAuditFailure(ctx, provider, account, "account", err)
		return err
	}
	if err := s.accounts.Delete(ctx, provider, account); err != nil {
		s.writeAuditFailure(ctx, provider, account, "account", err)
		return err
	}
	s.writeAuditLog(ctx, provider, account, "account", "completed", "info", "账号已删除")
	return nil
}

// RefreshUsage reads and stores one account's usage through the shared safety
// gate. Repeated manual clicks share the recent result instead of multiplying
// upstream traffic.
func (s *Service) RefreshUsage(
	ctx context.Context, provider, account string,
) (providerruntime.Usage, error) {
	return requestguard.Do(ctx, s.riskGuard, provider, account, "refresh",
		15*time.Second, func() (providerruntime.Usage, error) {
			return s.refreshUsage(ctx, provider, account)
		})
}

func (s *Service) refreshUsage(ctx context.Context, provider, account string) (providerruntime.Usage, error) {
	if err := s.ensureEnabled(ctx, provider, account); err != nil {
		return providerruntime.Usage{}, err
	}
	host, err := s.runtime(ctx)
	if err != nil {
		return providerruntime.Usage{}, err
	}
	runtimeProvider, err := s.runtimeProvider(ctx, host, provider)
	if err != nil {
		return providerruntime.Usage{}, err
	}
	if !runtimeProvider.Usage {
		return providerruntime.Usage{}, fmt.Errorf("该供应商不支持额度查询")
	}
	usage, err := host.Usage(ctx, provider, account)
	persistErr := s.persistAuth(ctx, host)
	if err != nil {
		return providerruntime.Usage{}, errors.Join(err, persistErr)
	}
	if strings.TrimSpace(usage.Error) != "" {
		return providerruntime.Usage{}, errors.Join(
			fmt.Errorf("额度查询失败: %s", redactMessage(usage.Error)),
			persistErr,
		)
	}
	if _, _, err := s.recordUsage(ctx, provider, account, usage); err != nil {
		return providerruntime.Usage{}, errors.Join(err, persistErr)
	}
	return usage, persistErr
}

// TestModel sends the smallest supported chat request to one provider model.
// It reports only connectivity metadata so the GUI never receives upstream
// response bodies, headers, credentials, or prompt echoes.
func (s *Service) TestModel(
	ctx context.Context, provider, account, modelID string,
) (view ModelTestView, err error) {
	modelID = strings.TrimSpace(modelID)
	defer func() {
		s.writeModelTestAuditLog(context.WithoutCancel(ctx), provider, account, modelID, view, err)
	}()
	// 去重键必须包含模型 ID：同一账号 30 秒内换一个模型测试是两个不同的
	// 上游请求；若只按 provider+account+operation 去重，会把上一个模型的
	// 成功结果当成新模型的结论返回（GUI 会显示"模型可用"但实际没测）。
	operation := "model_test"
	if modelID != "" {
		operation += ":" + modelID
	}
	view, err = requestguard.Do(ctx, s.riskGuard, provider, account, operation,
		30*time.Second, func() (ModelTestView, error) {
			return s.testModel(ctx, provider, account, modelID)
		})
	if err == nil && s.riskGuard != nil {
		s.riskGuard.ObserveStatus(provider, account, view.Status)
	}
	return view, err
}

func (s *Service) testModel(
	ctx context.Context, provider, account, modelID string,
) (view ModelTestView, err error) {
	account = strings.TrimSpace(account)
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return ModelTestView{}, errors.New("请选择要测试的模型")
	}
	if err := s.ensureTestableAccount(ctx, provider, account); err != nil {
		return ModelTestView{}, err
	}
	s.runMu.Lock()
	defer s.runMu.Unlock()

	host, err := s.runtime(ctx)
	if err != nil {
		return ModelTestView{}, err
	}
	runtimeProvider, err := s.runtimeProvider(ctx, host, provider)
	if err != nil {
		return ModelTestView{}, err
	}
	if strings.TrimSpace(account) == "" {
		if len(runtimeProvider.Accounts) == 0 {
			return ModelTestView{}, errors.New("该供应商尚未添加账号")
		}
		account = runtimeProvider.Accounts[0].Key
		if err := s.ensureTestableAccount(ctx, provider, account); err != nil {
			return ModelTestView{}, err
		}
	}
	foundAccount := false
	for _, item := range runtimeProvider.Accounts {
		if item.Key == account {
			foundAccount = true
			break
		}
	}
	if !foundAccount {
		return ModelTestView{}, errors.New("该供应商账号不存在，请刷新后重试")
	}
	loader, err := host.Load(ctx, provider, account)
	if err != nil {
		return ModelTestView{}, fmt.Errorf("读取供应商登录态失败: %w", err)
	}
	model, endpoint, err := modelTestTarget(runtimeProvider, account, modelID, loader.BaseURL)
	if err != nil {
		return ModelTestView{}, err
	}

	npm := strings.ToLower(firstNonEmpty(model.NPM, runtimeProvider.NPM))
	protocol := "openai"
	if strings.Contains(npm, "anthropic") {
		protocol = "anthropic"
	}
	body := map[string]any{
		"model":      firstNonEmpty(model.ID, modelID),
		"messages":   []map[string]string{{"role": "user", "content": "Reply OK."}},
		"max_tokens": 4,
		"stream":     false,
	}
	headers := map[string]string{"content-type": "application/json"}
	if protocol == "anthropic" {
		body["system"] = "You are a connectivity check."
		headers["anthropic-version"] = "2023-06-01"
	}
	// body 只包含字符串/整数/字符串切片，json.Marshal 不会失败；原来不可能
	// 走到的错误分支已删除，避免为了覆盖率硬造测试。
	encoded, _ := json.Marshal(body)

	started := time.Now()
	response, fetchErr := host.Fetch(ctx, providerruntime.FetchRequest{
		Provider: provider, Account: account, URL: endpoint, Method: "POST",
		Headers: headers, Body: string(encoded),
	})
	if fetchErr == nil && keepaliveNeedsStream(response.Status, response.Body) {
		body["stream"] = true
		encoded, _ = json.Marshal(body)
		response, fetchErr = host.Fetch(ctx, providerruntime.FetchRequest{
			Provider: provider, Account: account, URL: endpoint, Method: "POST",
			Headers: headers, Body: string(encoded),
		})
	}
	// time.Since 使用单调时钟，不可能为负；原来的负数夹紧分支是死代码，已删除。
	latency := time.Since(started).Milliseconds()
	persistErr := s.persistAuth(ctx, host)
	view = ModelTestView{
		Provider: provider, Account: maskIdentity(account), Model: modelID,
		Status: response.Status, Latency: latency,
	}
	if fetchErr != nil {
		view.Message = "模型请求失败，请检查网络或重新登录后重试"
		return view, persistErr
	}
	if response.Status < 200 || response.Status >= 300 {
		view.Message = modelTestStatusMessage(response.Status)
		return view, persistErr
	}
	if !hasReply(response.Body) {
		view.Message = "供应商返回成功，但没有模型回复"
		return view, persistErr
	}
	view.OK = true
	view.Message = "模型可用"
	return view, persistErr
}

func modelTestStatusMessage(status int) string {
	switch status {
	case 400:
		return "请求被供应商拒绝，请检查模型参数"
	case 401, 403:
		return "鉴权失败，请重新登录该账号"
	case 404:
		return "模型或接口不存在"
	case 408:
		return "供应商响应超时"
	case 429:
		return "请求被限流，请稍后再试"
	}
	if status >= 500 {
		return "供应商服务暂时异常"
	}
	return fmt.Sprintf("模型测试返回 HTTP %d", status)
}

func modelTestTarget(
	provider providerruntime.Provider,
	account, modelID, fallbackBaseURL string,
) (providerruntime.Model, string, error) {
	var selected providerruntime.Model
	for _, model := range provider.Models {
		if model.ID == modelID {
			selected = model
			break
		}
	}
	if selected.ID == "" {
		return providerruntime.Model{}, "", errors.New("该供应商没有此模型")
	}
	for _, item := range provider.Accounts {
		if item.Key != account {
			continue
		}
		if len(item.Models) > 0 {
			allowed := false
			for _, id := range item.Models {
				if id == modelID {
					allowed = true
					break
				}
			}
			if !allowed {
				return providerruntime.Model{}, "", errors.New("当前账号不能使用该模型")
			}
		}
		break
	}
	base := strings.TrimRight(firstNonEmpty(selected.APIURL, provider.APIURL, fallbackBaseURL), "/")
	if base == "" {
		return providerruntime.Model{}, "", errors.New("该模型没有可用的接口地址")
	}
	npm := strings.ToLower(firstNonEmpty(selected.NPM, provider.NPM))
	suffix := "/chat/completions"
	if strings.Contains(npm, "anthropic") {
		suffix = "/messages"
	}
	if !strings.HasSuffix(base, suffix) {
		base += suffix
	}
	return selected, base, nil
}

func (s *Service) recordUsage(
	ctx context.Context,
	provider, account string,
	usage providerruntime.Usage,
) (*float64, *float64, error) {
	if strings.TrimSpace(usage.Error) != "" {
		return nil, nil, fmt.Errorf("额度查询失败: %s", redactMessage(usage.Error))
	}
	windows, _ := json.Marshal(usage.Windows)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO quota_readings
  (provider, account_id, observed_at, plan, until, balance, windows_json)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		provider, account, s.now().UTC().Format(time.RFC3339),
		usage.Plan, usage.Until, usage.Balance, string(windows))
	if err != nil {
		return nil, nil, err
	}
	credits, balance := usageValues(usage)
	key := engine.Key{Target: provider, AccountID: account, Operation: engine.Checkin}
	if err := s.engine.RecordUsage(ctx, key, credits, balance); err != nil {
		return nil, nil, err
	}
	return credits, balance, nil
}

// HasProvider reports whether target belongs to the provider runtime. It is
// used by legacy management entry points so provider credentials cannot be
// orphaned by falling back to a metadata-only delete.
func (s *Service) HasProvider(ctx context.Context, target string) (bool, error) {
	var found int
	err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM providers WHERE id = ?`, target).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// Run executes checkin or keepalive through the shared daily state machine.
func (s *Service) Run(
	ctx context.Context, provider, account, operation string,
) (runErr error) {
	recorded := false
	defer func() {
		if runErr != nil && !recorded {
			s.writeAuditFailure(ctx, provider, account, operation, runErr)
		}
	}()
	s.runMu.Lock()
	defer s.runMu.Unlock()

	if err := s.ensureEnabled(ctx, provider, account); err != nil {
		return err
	}
	host, err := s.runtime(ctx)
	if err != nil {
		return err
	}
	runtimeProvider, err := s.runtimeProvider(ctx, host, provider)
	if err != nil {
		return err
	}
	if err := checkOperationCapability(runtimeProvider, operation); err != nil {
		return err
	}
	started := s.now().UTC().Format(time.RFC3339)
	runID := newRunID(s.now())
	result := s.runOne(ctx, host, provider, account, operation)
	if recordErr := s.recordRun(ctx, runID, started, operation, []runResult{result}); recordErr != nil {
		return recordErr
	}
	recorded = true
	if result.Err != nil {
		return result.Err
	}
	if !result.Executed {
		return fmt.Errorf("%w（当前状态：%s）", ErrNotExecuted, result.State)
	}
	return nil
}

// RunAll executes the provider-backed accounts selected by action. The action
// values are all/checkin/keepalive. One account failure never aborts the rest.
func (s *Service) RunAll(ctx context.Context, action string, exclude map[string]bool) (RunSummary, error) {
	if action != "all" && action != "checkin" && action != "keepalive" {
		return RunSummary{}, fmt.Errorf("unsupported provider action %q", action)
	}
	if !features.OperationAvailable("keepalive") && action == "keepalive" {
		return RunSummary{}, features.ErrKeepaliveDisabled
	}
	action = features.NormalizeAction(action)
	s.runMu.Lock()
	defer s.runMu.Unlock()

	view, err := s.List(ctx)
	if err != nil {
		return RunSummary{}, err
	}
	return s.runAllViewLocked(ctx, view, action, exclude)
}

// RunAllView executes an already synchronized provider view. App uses this to
// separate adapter and provider execution domains without reading plugins
// twice in one scheduled run.
func (s *Service) RunAllView(
	ctx context.Context,
	view View,
	action string,
	exclude map[string]bool,
) (RunSummary, error) {
	if action != "all" && action != "checkin" && action != "keepalive" {
		return RunSummary{}, fmt.Errorf("unsupported provider action %q", action)
	}
	if !features.OperationAvailable("keepalive") && action == "keepalive" {
		return RunSummary{}, features.ErrKeepaliveDisabled
	}
	action = features.NormalizeAction(action)
	s.runMu.Lock()
	defer s.runMu.Unlock()
	return s.runAllViewLocked(ctx, view, action, exclude)
}

func (s *Service) runAllViewLocked(
	ctx context.Context,
	view View,
	action string,
	exclude map[string]bool,
) (RunSummary, error) {
	host, err := s.runtime(ctx)
	if err != nil {
		return RunSummary{}, err
	}

	started := s.now().UTC().Format(time.RFC3339)
	runID := newRunID(s.now())
	summary := RunSummary{}
	results := []runResult{}
	var failures []error
	for _, provider := range view.Providers {
		if exclude[provider.ID] || !provider.Enabled {
			continue
		}
		for _, account := range provider.Accounts {
			if !account.Enabled {
				continue
			}
			operations := providerOperations(provider, account, action)
			if len(operations) == 0 {
				summary.Skipped++
				results = append(results, runResult{
					Provider: provider.ID, Account: account.Key, Action: operationLabel(action, provider),
					State: string(engine.None), Message: "没有启用的自动任务",
				})
				continue
			}
			for _, operation := range operations {
				result := s.runOne(ctx, host, provider.ID, account.Key, operation)
				results = append(results, result)
				if result.Err != nil {
					failures = append(failures, fmt.Errorf("%s/%s/%s: %w",
						provider.ID, account.Key, operation, result.Err))
					summary.NeedsAttention++
					continue
				}
				if !result.Executed || result.State == string(engine.FailedRetryable) ||
					result.State == string(engine.FailedFinal) ||
					result.State == string(engine.ManualRequired) {
					summary.Attempted++
					if !result.Executed {
						summary.Skipped++
						summary.Attempted--
					}
					if result.State == string(engine.FailedRetryable) ||
						result.State == string(engine.FailedFinal) ||
						result.State == string(engine.ManualRequired) {
						summary.NeedsAttention++
					}
					continue
				}
				summary.Attempted++
				summary.Succeeded++
			}
		}
	}
	if err := s.recordRun(ctx, runID, started, action, results); err != nil {
		return summary, err
	}
	return summary, errors.Join(failures...)
}

func providerOperations(provider ProviderView, account AccountView, action string) []string {
	if !features.OperationAvailable(action) {
		return []string{}
	}
	action = features.NormalizeAction(action)
	out := []string{}
	want := func(operation string) bool {
		return action == operation
	}
	if provider.Checkin && want("checkin") && enabledPref(account.TaskPreferences, "checkin") {
		out = append(out, "checkin")
	}
	return out
}

func enabledPref(prefs map[string]bool, operation string) bool {
	value, ok := prefs[operation]
	return !ok || value
}

func operationLabel(action string, provider ProviderView) string {
	return features.NormalizeAction(action)
}

func (s *Service) runOne(
	ctx context.Context, host Runtime, provider, account, operation string,
) runResult {
	result := runResult{Provider: provider, Account: account, Action: operation}
	switch operation {
	case "checkin":
		var obs engine.Observation
		var claimedCredit float64
		obs, err := s.engine.Run(ctx,
			engine.Key{Target: provider, AccountID: account, Operation: engine.Checkin},
			func(ctx context.Context, _ engine.Key) (engine.ExternalResult, error) {
				upstream, guardErr := requestguard.Do(ctx, s.riskGuard,
					provider, account, "checkin", time.Minute,
					func() (providerruntime.Checkin, error) {
						return host.Checkin(ctx, provider, account)
					})
				if guardErr != nil {
					return engine.ExternalResult{Kind: engine.RRetryable, Message: guardErr.Error()}, nil
				}
				if s.riskGuard != nil {
					s.riskGuard.ObserveText(provider, account, upstream.Message)
					if upstream.Outcome == "captcha" {
						s.riskGuard.Record(provider, account, requestguard.ReasonChallenge)
					}
				}
				claimedCredit = upstream.Credit
				kind := kindForCheckin(upstream.Outcome)
				return engine.ExternalResult{Kind: kind, Message: upstream.Message}, nil
			})
		if err != nil {
			result.Err = err
			return result
		}
		result.State = string(obs.State)
		result.Executed = obs.Executed
		result.Message = redactMessage(obs.Message)
		// A successful check-in should immediately show the account's current
		// quota when the plugin exposes usage. Failure here never rewrites the
		// check-in result.
		if obs.State == engine.SignedConfirmed || obs.State == engine.SignedProvisional ||
			obs.State == engine.Already {
			usage, usageErr := requestguard.Do(ctx, s.riskGuard,
				provider, account, "refresh", 15*time.Second,
				func() (providerruntime.Usage, error) {
					return host.Usage(ctx, provider, account)
				})
			if usageErr == nil && strings.TrimSpace(usage.Error) == "" {
				result.Credits, result.Balance, usageErr = s.recordUsage(ctx, provider, account, usage)
			}
			if usageErr == nil {
				if claimedCredit > 0 {
					value := claimedCredit
					result.Credits = &value
				}
				_ = s.engine.RecordUsage(ctx,
					engine.Key{Target: provider, AccountID: account, Operation: engine.Checkin},
					result.Credits, result.Balance)
			} else if claimedCredit > 0 {
				value := claimedCredit
				result.Credits = &value
				_ = s.engine.RecordUsage(ctx,
					engine.Key{Target: provider, AccountID: account, Operation: engine.Checkin},
					result.Credits, nil)
			}
			if result.Balance == nil {
				message := appendMessage(result.Message,
					"当前额度暂不可读，可点击「刷新额度」重试")
				result.Message = message
				obs.Message = message
				_ = s.engine.ReconcileState(ctx, engine.Key{
					Target: provider, AccountID: account, Operation: engine.Checkin,
				}, obs.State, message)
			}
		}
	case "keepalive":
		// 保活当前由 features.KeepaliveAvailable=false 全局停用：
		// providerOperations/NormalizeAction 不会产出该操作，GUI/调度都不会
		// 走到这里。保留分支只为实现与测试内部路径，恢复功能前不得绕过
		// features.OperationAvailable 直接调用 Run(..., "keepalive")。
		var obs engine.Observation
		obs, err := s.engine.Run(ctx,
			engine.Key{Target: provider, AccountID: account, Operation: engine.Keepalive},
			func(ctx context.Context, _ engine.Key) (engine.ExternalResult, error) {
				return s.keepalive(ctx, host, provider, account)
			})
		if err != nil {
			result.Err = err
			return result
		}
		result.State = string(obs.State)
		result.Executed = obs.Executed
		result.Message = redactMessage(obs.Message)
		if obs.Executed && obs.State == engine.Completed {
			usage, usageErr := requestguard.Do(ctx, s.riskGuard,
				provider, account, "refresh", 15*time.Second,
				func() (providerruntime.Usage, error) {
					return host.Usage(ctx, provider, account)
				})
			if usageErr == nil && strings.TrimSpace(usage.Error) == "" {
				result.Credits, result.Balance, _ = s.recordUsage(ctx, provider, account, usage)
			}
		}
	case "refresh":
		usage, err := s.RefreshUsage(ctx, provider, account)
		if err != nil {
			result.Err = err
			return result
		}
		result.State = "completed"
		result.Executed = true
		result.Credits, result.Balance = usageValues(usage)
	default:
		result.Err = fmt.Errorf("unsupported provider operation %q", operation)
	}
	return result
}

func usageValues(usage providerruntime.Usage) (*float64, *float64) {
	var credits, balance *float64
	for _, window := range usage.Windows {
		remaining := window.Balance
		if remaining == nil && window.Limit > 0 {
			value := window.Limit - window.Amount
			if value < 0 {
				value = 0
			}
			remaining = &value
		}
		if remaining == nil {
			continue
		}
		value := *remaining
		if balance == nil {
			balance = &value
		}
		if credits == nil {
			credits = &value
		}
	}
	if balance == nil {
		if value, ok := parseUsageBalance(usage.Balance); ok {
			balance = &value
			credits = &value
		}
	}
	return credits, balance
}

var usageBalancePattern = regexp.MustCompile(`[-+]?[0-9][0-9,]*(?:\.[0-9]+)?`)

func parseUsageBalance(value string) (float64, bool) {
	match := usageBalancePattern.FindString(strings.TrimSpace(value))
	if match == "" {
		return 0, false
	}
	number, err := strconv.ParseFloat(strings.ReplaceAll(match, ",", ""), 64)
	if err != nil {
		return 0, false
	}
	if number < 0 {
		number = 0
	}
	return number, true
}

func appendMessage(message, suffix string) string {
	message = strings.TrimSpace(message)
	suffix = strings.TrimSpace(suffix)
	if suffix == "" {
		return message
	}
	if message == "" {
		return suffix
	}
	return message + "；" + suffix
}

func newRunID(now time.Time) string {
	var suffix [6]byte
	_, _ = rand.Read(suffix[:])
	return fmt.Sprintf("%s-%x", now.UTC().Format("20060102T150405"), suffix)
}

func (s *Service) recordRun(
	ctx context.Context, runID, started, action string, results []runResult,
) error {
	if s.db == nil {
		return nil
	}
	attempted := 0
	succeeded := 0
	needsAttention := 0
	skipped := 0
	state := "completed"
	for _, result := range results {
		if result.Err != nil || result.State == string(engine.FailedRetryable) ||
			result.State == string(engine.FailedFinal) ||
			result.State == string(engine.ManualRequired) {
			needsAttention++
			state = "failed_retryable"
		}
		if result.Executed {
			attempted++
			if result.Err == nil && result.State != string(engine.FailedRetryable) &&
				result.State != string(engine.FailedFinal) &&
				result.State != string(engine.ManualRequired) {
				succeeded++
			}
		} else if result.State != "" {
			skipped++
		}
	}
	if needsAttention > 0 && attempted == 0 {
		state = "failed_final"
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	finished := s.now().UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx, `
INSERT INTO runs
  (id, started_at, finished_at, action, state, attempted, succeeded, needs_attention, skipped, message)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		runID, started, finished, action, state, attempted, succeeded,
		needsAttention, skipped, ""); err != nil {
		return err
	}
	for _, result := range results {
		message := redactMessage(result.Message)
		if result.Err != nil {
			message = "执行失败，详情请查看脱敏日志"
		}
		_, err := tx.ExecContext(ctx, `
INSERT INTO run_items
  (run_id, provider, account_id, action, state, message, credits, balance, model, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			runID, result.Provider, result.Account, result.Action, result.State,
			message, nullableFloat(result.Credits), nullableFloat(result.Balance),
			result.Model, finished)
		if err != nil {
			return err
		}
		logState, level, logMessage := runResultAudit(result)
		if err := s.insertLog(ctx, tx, finished, result.Provider, result.Account,
			result.Action, logState, level, logMessage); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type sqlExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func (s *Service) writeAuditLog(
	ctx context.Context,
	target, account, operation, state, level, message string,
) {
	if s == nil || s.db == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	} else {
		ctx = context.WithoutCancel(ctx)
	}
	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_ = s.insertLog(writeCtx, s.db, s.now().UTC().Format(time.RFC3339),
		target, account, operation, state, level, message)
}

func (s *Service) writeAuditFailure(
	ctx context.Context,
	target, account, operation string,
	operationErr error,
) {
	if operationErr == nil {
		return
	}
	message := auditOperationLabel(operation) + "失败"
	if detail := redactMessage(operationErr.Error()); detail != "" {
		message += " · " + detail
	}
	s.writeAuditLog(ctx, target, account, operation, "failed_final", "error", message)
}

func (s *Service) insertLog(
	ctx context.Context,
	exec sqlExecer,
	ts, target, account, operation, state, level, message string,
) error {
	if exec == nil {
		return nil
	}
	_, err := exec.ExecContext(ctx, `
INSERT INTO logs (ts, target, account_id, operation, state, level, message)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		ts, target, account, operation, state, level, redactMessage(message))
	return err
}

func (s *Service) writeModelTestAuditLog(
	ctx context.Context,
	provider, account, modelID string,
	view ModelTestView,
	testErr error,
) {
	if s == nil || s.db == nil {
		return
	}
	state := "completed"
	level := "info"
	message := "模型测试"
	if modelID != "" {
		message += " · " + modelID
	}
	switch {
	case testErr != nil:
		state = "failed_final"
		level = "error"
		message += " · " + firstNonEmpty(redactMessage(testErr.Error()), "执行失败")
	case !view.OK:
		state = modelTestFailureState(view.Status)
		level = "error"
		if view.Status > 0 {
			message += fmt.Sprintf(" · HTTP %d", view.Status)
		}
		if view.Latency > 0 {
			message += fmt.Sprintf(" · %d ms", view.Latency)
		}
		message += " · " + firstNonEmpty(redactMessage(view.Message), "模型不可用")
	default:
		message += fmt.Sprintf(" · HTTP %d", view.Status)
		if view.Latency > 0 {
			message += fmt.Sprintf(" · %d ms", view.Latency)
		}
		message += " · 模型可用"
	}
	s.writeAuditLog(ctx, provider, account, "model_test", state, level, message)
}

func modelTestFailureState(status int) string {
	if status == 0 || status == 408 || status == 429 || status >= 500 {
		return "failed_retryable"
	}
	return "failed_final"
}

func runResultAudit(result runResult) (state, level, message string) {
	state = strings.TrimSpace(result.State)
	failed := result.Err != nil || state == string(engine.FailedRetryable) ||
		state == string(engine.FailedFinal)
	if state == "" {
		if result.Err != nil {
			state = string(engine.FailedRetryable)
		} else {
			state = "completed"
		}
	}
	level = "info"
	if failed {
		level = "error"
	} else if state == string(engine.ManualRequired) {
		level = "warn"
	}

	parts := []string{auditOperationLabel(result.Action)}
	if result.Credits != nil &&
		(result.Balance == nil || *result.Credits != *result.Balance) {
		parts = append(parts, "今日已领取 +"+formatAuditNumber(*result.Credits))
	}
	if result.Balance != nil {
		parts = append(parts, "当前额度 "+formatAuditNumber(*result.Balance))
	}
	if result.Model != "" {
		parts = append(parts, "模型 "+result.Model)
	}
	if failed || state == string(engine.ManualRequired) || !result.Executed {
		parts = append(parts, auditStateLabel(state))
	}
	if result.Message != "" {
		parts = append(parts, redactMessage(result.Message))
	} else if result.Err != nil {
		parts = append(parts, firstNonEmpty(redactMessage(result.Err.Error()), "执行失败"))
	}
	return state, level, redactMessage(strings.Join(parts, " · "))
}

func auditOperationLabel(operation string) string {
	switch operation {
	case "checkin":
		return "签到"
	case "refresh":
		return "额度刷新"
	case "keepalive":
		return "保活"
	case "model_test":
		return "模型测试"
	case "login":
		return "登录"
	case "account":
		return "账号设置"
	case "provider":
		return "供应商设置"
	case "automation":
		return "自动执行"
	default:
		return firstNonEmpty(operation, "操作")
	}
}

func auditStateLabel(state string) string {
	switch state {
	case string(engine.SignedConfirmed):
		return "已确认到账"
	case string(engine.SignedProvisional):
		return "已领取，待确认"
	case string(engine.Already):
		return "今日已领取"
	case string(engine.ManualRequired):
		return "需要人工处理"
	case string(engine.FailedRetryable):
		return "网络或上游暂时失败"
	case string(engine.FailedFinal):
		return "执行失败"
	case "completed":
		return "已完成"
	default:
		return state
	}
}

func formatAuditNumber(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func enabledLabel(enabled bool) string {
	if enabled {
		return "启用"
	}
	return "停用"
}

func nullableFloat(value *float64) any {
	if value == nil {
		return nil
	}
	return *value
}

// redactMessage 使用 internal/privacy 的统一规则（bearer/密钥/uid/手机号/邮箱/
// 本机用户名/长 token），保证 provider 写日志与 adapter 写日志是同一套口径。
func redactMessage(value string) string {
	return privacy.SanitizeText(value, 500)
}

// History returns recent provider run items and quota readings. Account IDs
// are masked before leaving the service.
func (s *Service) History(
	ctx context.Context, provider, account string, limit int,
) ([]RunItemView, []QuotaReadingView, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	query := `
SELECT r.id, i.provider, i.account_id, i.action, i.state, i.message,
       i.credits, i.balance, i.model, i.created_at
FROM run_items i
JOIN runs r ON r.id = i.run_id
WHERE (? = '' OR i.provider = ?) AND (? = '' OR i.account_id = ?)
`
	args := []any{provider, provider, account, account}
	if !features.KeepaliveAvailable {
		query += ` AND i.action <> ?`
		args = append(args, "keepalive")
	}
	query += ` ORDER BY i.created_at DESC, i.id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	items := []RunItemView{}
	for rows.Next() {
		var item RunItemView
		var credits, balance sql.NullFloat64
		if err := rows.Scan(&item.RunID, &item.Provider, &item.Account, &item.Action,
			&item.State, &item.Message, &credits, &balance, &item.Model,
			&item.ObservedAt); err != nil {
			return nil, nil, err
		}
		item.Account = maskIdentity(item.Account)
		if credits.Valid {
			value := credits.Float64
			item.Credits = &value
		}
		if balance.Valid {
			value := balance.Float64
			item.Balance = &value
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	rows, err = s.db.QueryContext(ctx, `
SELECT observed_at, plan, until, balance, windows_json
FROM quota_readings
WHERE (? = '' OR provider = ?) AND (? = '' OR account_id = ?)
ORDER BY observed_at DESC, id DESC
LIMIT ?`, provider, provider, account, account, limit)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	readings := []QuotaReadingView{}
	for rows.Next() {
		var item QuotaReadingView
		var windows string
		if err := rows.Scan(&item.ObservedAt, &item.Plan, &item.Until,
			&item.Balance, &windows); err != nil {
			return nil, nil, err
		}
		if json.Unmarshal([]byte(windows), &item.Windows) != nil || item.Windows == nil {
			item.Windows = []providerruntime.UsageWindow{}
		}
		readings = append(readings, item)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return items, readings, nil
}

func (s *Service) keepalive(
	ctx context.Context, host Runtime, provider, account string,
) (engine.ExternalResult, error) {
	loader, err := host.Load(ctx, provider, account)
	if err != nil {
		return engine.ExternalResult{Kind: engine.RRetryable, Message: "读取供应商登录态失败"}, nil
	}
	if !loader.Fetch {
		return engine.ExternalResult{Kind: engine.RFinal, Message: "该供应商不支持保活请求"}, nil
	}
	runtimeProvider, err := s.runtimeProvider(ctx, host, provider)
	if err != nil {
		return engine.ExternalResult{Kind: engine.RRetryable, Message: "读取供应商模型失败"}, nil
	}
	model, url := keepaliveModel(runtimeProvider, account, loader.BaseURL)
	if model.ID == "" || url == "" {
		return engine.ExternalResult{Kind: engine.RFinal, Message: "没有可用的保活模型"}, nil
	}
	npm := firstNonEmpty(model.NPM, runtimeProvider.NPM)
	protocol := "openai"
	if strings.Contains(strings.ToLower(npm), "anthropic") {
		protocol = "anthropic"
	}
	body := map[string]any{
		"model":      model.ID,
		"messages":   []map[string]string{{"role": "user", "content": "Reply OK."}},
		"max_tokens": 4,
		"stream":     false,
	}
	headers := map[string]string{"content-type": "application/json"}
	if protocol == "anthropic" {
		body["system"] = "You are a connectivity check."
		headers["anthropic-version"] = "2023-06-01"
	}
	encoded, _ := json.Marshal(body)
	response, err := host.Fetch(ctx, providerruntime.FetchRequest{
		Provider: provider, Account: account, URL: url, Method: "POST",
		Headers: headers, Body: string(encoded),
	})
	if err != nil {
		return engine.ExternalResult{Kind: engine.RRetryable, Message: "供应商保活请求失败"}, nil
	}
	if keepaliveNeedsStream(response.Status, response.Body) {
		body["stream"] = true
		encoded, _ = json.Marshal(body)
		response, err = host.Fetch(ctx, providerruntime.FetchRequest{
			Provider: provider, Account: account, URL: url, Method: "POST",
			Headers: headers, Body: string(encoded),
		})
		if err != nil {
			return engine.ExternalResult{Kind: engine.RRetryable, Message: "供应商流式保活请求失败"}, nil
		}
	}
	if response.Status < 200 || response.Status >= 300 {
		kind := engine.RRetryable
		if response.Status == 401 || response.Status == 403 || response.Status == 429 {
			kind = engine.RFinal
		}
		return engine.ExternalResult{Kind: kind, Message: fmt.Sprintf("供应商保活返回 HTTP %d", response.Status)}, nil
	}
	if !hasReply(response.Body) {
		return engine.ExternalResult{Kind: engine.RRetryable, Message: "供应商返回成功但没有模型回复"}, nil
	}
	return engine.ExternalResult{Kind: engine.RSuccess, Message: "保活完成"}, nil
}

func keepaliveModel(
	provider providerruntime.Provider,
	account, fallbackBaseURL string,
) (providerruntime.Model, string) {
	allowed := map[string]bool{}
	for _, item := range provider.Accounts {
		if item.Key != account {
			continue
		}
		for _, id := range item.Models {
			allowed[id] = true
		}
		break
	}
	try := func(model providerruntime.Model) (providerruntime.Model, string) {
		if len(allowed) > 0 && !allowed[model.ID] {
			return providerruntime.Model{}, ""
		}
		base := strings.TrimRight(firstNonEmpty(model.APIURL, provider.APIURL, fallbackBaseURL), "/")
		if base == "" {
			return providerruntime.Model{}, ""
		}
		npm := strings.ToLower(firstNonEmpty(model.NPM, provider.NPM))
		suffix := "/chat/completions"
		if strings.Contains(npm, "anthropic") {
			suffix = "/messages"
		}
		if !strings.HasSuffix(base, suffix) {
			base += suffix
		}
		return model, base
	}
	for _, model := range provider.Models {
		if candidate, url := try(model); url != "" {
			return candidate, url
		}
	}
	if fallbackBaseURL != "" && len(provider.Models) > 0 {
		return try(provider.Models[0])
	}
	return providerruntime.Model{}, ""
}

func hasReply(data []byte) bool {
	var value map[string]any
	if json.Unmarshal(data, &value) != nil {
		text := string(data)
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload == "" || payload == "[DONE]" {
				continue
			}
			var event map[string]any
			if json.Unmarshal([]byte(payload), &event) == nil && jsonHasReply(event) {
				return true
			}
		}
		return len(strings.TrimSpace(text)) > 0
	}
	return jsonHasReply(value)
}

func jsonHasReply(value map[string]any) bool {
	if choices, ok := value["choices"].([]any); ok && len(choices) > 0 {
		return true
	}
	if content, ok := value["content"].([]any); ok && len(content) > 0 {
		return true
	}
	if text, ok := value["output_text"].(string); ok && strings.TrimSpace(text) != "" {
		return true
	}
	return false
}

func keepaliveNeedsStream(status int, body []byte) bool {
	if status != 400 && status != 415 && status != 422 {
		return false
	}
	message := strings.ToLower(string(body))
	return strings.Contains(message, "non-stream") ||
		strings.Contains(message, "stream request") ||
		strings.Contains(message, "streaming is required") ||
		strings.Contains(message, "stream must")
}

func kindForCheckin(outcome string) engine.ResultKind {
	switch outcome {
	case "claimed":
		return engine.RSuccess
	case "done":
		return engine.RAlready
	case "ineligible":
		return engine.RIneligible
	case "inactive":
		return engine.RInactive
	case "captcha":
		return engine.RManualRequired
	case "failed":
		return engine.RFinal
	default:
		return engine.RRetryable
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func maskIdentity(value string) string {
	value = strings.ToValidUTF8(strings.TrimSpace(value), "�")
	chars := []rune(value)
	if len(chars) == 0 {
		return "****"
	}
	at := -1
	for index, char := range chars {
		if char == '@' {
			at = index
			break
		}
	}
	if at > 0 {
		prefix := chars[:at]
		if len(prefix) > 2 {
			prefix = prefix[:2]
		}
		return string(prefix) + "***" + string(chars[at:])
	}
	if len(chars) <= 4 {
		return "****" + string(chars)
	}
	return "****" + string(chars[len(chars)-4:])
}

func usageCredits(usage providerruntime.Usage) *float64 {
	for _, window := range usage.Windows {
		if window.Balance != nil {
			value := *window.Balance
			return &value
		}
	}
	return nil
}

// StaleAuthPath returns the temporary auth file for cleanup/test diagnostics.
func StaleAuthPath(configDir string) string {
	return filepath.Join(configDir, "provider-auth.tmp.json")
}
