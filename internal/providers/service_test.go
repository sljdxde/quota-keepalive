package providers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"qka/internal/accounts"
	"qka/internal/capabilities"
	"qka/internal/engine"
	"qka/internal/providerruntime"
	"qka/internal/store"
)

type fakeRuntime struct {
	providers    []providerruntime.Provider
	providerErrs error

	prompt       providerruntime.Prompt
	promptErr    error
	authorize    providerruntime.Authorization
	authorizeErr error
	finish       providerruntime.Saved
	finishErr    error
	apiKey       providerruntime.Saved
	apiKeyErr    error
	usage        providerruntime.Usage
	usageErr     error
	checkin      providerruntime.Checkin
	checkinErr   error
	loader       providerruntime.Loader
	loadErr      error
	fetch        providerruntime.FetchResponse
	fetchQueue   []providerruntime.FetchResponse
	fetchErr     error
	fetchRequest providerruntime.FetchRequest
	signOutErr   error
	auth         []byte
	authErr      error
	removeAuth   error
	closeErr     error
	diagnostics  []providerruntime.LoadedPlugin

	providerCalls int
	checkinCalls  int
	fetchCalls    int
	usageCalls    int
	readAuthCalls int
}

func (f *fakeRuntime) Providers(context.Context) ([]providerruntime.Provider, error) {
	f.providerCalls++
	return f.providers, f.providerErrs
}

func (f *fakeRuntime) Prompt(context.Context, string, int, map[string]string) (providerruntime.Prompt, error) {
	return f.prompt, f.promptErr
}

func (f *fakeRuntime) Authorize(context.Context, string, int, map[string]string, string) (providerruntime.Authorization, error) {
	return f.authorize, f.authorizeErr
}

func (f *fakeRuntime) Finish(context.Context, string, string) (providerruntime.Saved, error) {
	return f.finish, f.finishErr
}

func (f *fakeRuntime) APIKey(context.Context, string, int, map[string]string, string, string) (providerruntime.Saved, error) {
	return f.apiKey, f.apiKeyErr
}

func (f *fakeRuntime) Usage(context.Context, string, string) (providerruntime.Usage, error) {
	f.usageCalls++
	return f.usage, f.usageErr
}

func (f *fakeRuntime) Checkin(context.Context, string, string) (providerruntime.Checkin, error) {
	f.checkinCalls++
	return f.checkin, f.checkinErr
}

func (f *fakeRuntime) Load(context.Context, string, string) (providerruntime.Loader, error) {
	return f.loader, f.loadErr
}

func (f *fakeRuntime) Fetch(_ context.Context, req providerruntime.FetchRequest) (providerruntime.FetchResponse, error) {
	f.fetchCalls++
	f.fetchRequest = req
	if len(f.fetchQueue) > 0 {
		response := f.fetchQueue[0]
		f.fetchQueue = f.fetchQueue[1:]
		return response, nil
	}
	return f.fetch, f.fetchErr
}

func (f *fakeRuntime) SignOut(context.Context, string, string) error { return f.signOutErr }
func (f *fakeRuntime) ReadAuth() ([]byte, error) {
	f.readAuthCalls++
	return f.auth, f.authErr
}
func (f *fakeRuntime) RemoveAuth() error { return f.removeAuth }
func (f *fakeRuntime) Close() error      { return f.closeErr }
func (f *fakeRuntime) LoadedPlugins() []providerruntime.LoadedPlugin {
	return f.diagnostics
}

type memorySecretStore struct {
	values map[string]string
	err    error
}

func (m *memorySecretStore) key(service, account string) string {
	return service + "/" + account
}

func (m *memorySecretStore) Set(_ context.Context, service, account, secret string) error {
	if m.err != nil {
		return m.err
	}
	if m.values == nil {
		m.values = map[string]string{}
	}
	m.values[m.key(service, account)] = secret
	return nil
}

func (m *memorySecretStore) Get(_ context.Context, service, account string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	value, ok := m.values[m.key(service, account)]
	if !ok {
		return "", capabilities.ErrSecretNotFound
	}
	return value, nil
}

func (m *memorySecretStore) Delete(_ context.Context, service, account string) error {
	if m.err != nil {
		return m.err
	}
	delete(m.values, m.key(service, account))
	return nil
}

type fixture struct {
	service *Service
	runtime *fakeRuntime
	store   *store.Store
	account *accounts.Service
}

func providerFixture() providerruntime.Provider {
	return providerruntime.Provider{
		ID: "fixture", Spec: "@magpie-community/opencode-fixture-auth",
		Name: "Fixture", Icon: "data:image/png;base64,AA==", Usage: true,
		Checkin: true, Fetch: true, SignedIn: true, AuthType: "oauth",
		NPM: "@ai-sdk/openai-compatible", APIURL: "https://provider.example/v1",
		Methods: []providerruntime.Method{
			{Type: "oauth", Label: "浏览器"},
			{Type: "api", Label: "API Key"},
		},
		Models: []providerruntime.Model{{
			ID: "model-a", Name: "Model A", Free: true, Context: 128000, Rate: 0.5,
			NPM: "@ai-sdk/openai-compatible", APIURL: "https://provider.example/v1",
		}},
		Accounts: []providerruntime.Account{
			{Key: "a1", Type: "oauth", AccountID: "user@example.com", Hint: "first"},
			{Key: "a2", Type: "api", AccountID: "13800138000", Hint: "second"},
		},
	}
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	fake := &fakeRuntime{
		providers: []providerruntime.Provider{providerFixture()},
		auth:      []byte("{}"),
		usage: providerruntime.Usage{
			Plan: "Pro", Until: "2026-12-01", Balance: "12.5",
			Windows: []providerruntime.UsageWindow{{
				Name: "daily", Balance: floatPtr(12.5), Display: "12.5",
			}},
		},
		checkin: providerruntime.Checkin{
			Outcome: "claimed", Credit: 100, Message: "签到成功",
		},
		loader: providerruntime.Loader{
			BaseURL: "https://provider.example/v1", Fetch: true,
		},
		fetch: providerruntime.FetchResponse{
			Status: 200, Body: []byte(`{"choices":[{"message":{"content":"OK"}}]}`),
		},
	}
	acct := accounts.NewService(st.DB())
	eng := engine.New(st.DB(), func() time.Time {
		return time.Date(2026, 10, 30, 9, 0, 0, 0, engine.Beijing)
	}, "test-holder")
	svc := NewService(st.DB(), acct, nil, eng, func(context.Context) (Runtime, error) {
		return fake, nil
	}, func() time.Time {
		return time.Date(2026, 10, 30, 9, 0, 0, 0, engine.Beijing)
	})
	return &fixture{service: svc, runtime: fake, store: st, account: acct}
}

func listFixtureEnabled(t *testing.T, f *fixture) (View, error) {
	t.Helper()
	ctx := context.Background()
	if _, err := f.service.List(ctx); err != nil {
		return View{}, err
	}
	if err := f.service.SetEnabled(ctx, "fixture", true); err != nil {
		return View{}, err
	}
	view, err := f.service.List(ctx)
	closeErr := f.service.Close()
	if err != nil {
		return View{}, err
	}
	return view, closeErr
}

func floatPtr(value float64) *float64 { return &value }

func TestListSyncsProvidersAndMasksAccounts(t *testing.T) {
	f := newFixture(t)
	f.runtime.diagnostics = []providerruntime.LoadedPlugin{{
		Spec: "broken-auth", Error: "load failed",
	}}
	view, err := listFixtureEnabled(t, f)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Providers) != 1 || view.AccountCount != 2 ||
		view.EnabledCount != 1 || view.Providers[0].Keepalive {
		t.Fatalf("view: %+v", view)
	}
	if view.Providers[0].Accounts[0].MaskedIdentity != "us***@example.com" {
		t.Errorf("email mask mismatch: %s", view.Providers[0].Accounts[0].MaskedIdentity)
	}
	if view.Providers[0].Accounts[1].MaskedIdentity != "****8000" {
		t.Errorf("phone mask mismatch: %s", view.Providers[0].Accounts[1].MaskedIdentity)
	}
	if len(view.PluginErrors) != 1 || view.PluginErrors[0].Spec != "broken-auth" {
		t.Errorf("plugin diagnostics: %+v", view.PluginErrors)
	}
	if len(view.Providers[0].Models) != 1 {
		t.Fatalf("model metadata: %+v", view.Providers[0].Models)
	}
	model := view.Providers[0].Models[0]
	if model.ID != "model-a" || model.Name != "Model A" || !model.Free ||
		model.Context != 128000 || model.Rate != 0.5 {
		t.Errorf("model metadata mismatch: %+v", model)
	}
	for _, account := range view.Providers[0].Accounts {
		if !account.Enabled || !account.TaskPreferences["checkin"] {
			t.Errorf("new healthy accounts should default enabled: %+v", account)
		}
		if _, exposed := account.TaskPreferences["keepalive"]; exposed {
			t.Errorf("keepalive must remain hidden: %+v", account)
		}
	}
}

func TestModelProbeUsesSignedAccountAndReturnsOnlySafeMetadata(t *testing.T) {
	f := newFixture(t)
	if _, err := listFixtureEnabled(t, f); err != nil {
		t.Fatal(err)
	}
	view, err := f.service.TestModel(context.Background(), "fixture", "a1", "model-a")
	if err != nil {
		t.Fatal(err)
	}
	if !view.OK || view.Status != 200 || view.Model != "model-a" ||
		view.Account == "a1" || view.Message != "模型可用" {
		t.Fatalf("model test view: %+v", view)
	}
	request := f.runtime.fetchRequest
	if !strings.HasSuffix(request.URL, "/chat/completions") ||
		!strings.Contains(request.Body, `"model":"model-a"`) {
		t.Fatalf("model test request: %+v", request)
	}
	data, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "choices") ||
		strings.Contains(string(data), "access_token") {
		t.Fatalf("model test leaked response detail: %s", data)
	}

	f.runtime.fetchQueue = []providerruntime.FetchResponse{{
		Status: 401, Body: []byte(`{"error":"invalid token"}`),
	}}
	view, err = f.service.TestModel(context.Background(), "fixture", "", "model-a")
	if err != nil {
		t.Fatal(err)
	}
	if view.OK || view.Status != 401 || !strings.Contains(view.Message, "鉴权失败") {
		t.Fatalf("auth failure view: %+v", view)
	}
}

func TestModelProbeRejectsUnknownModulesAndMissingAccounts(t *testing.T) {
	f := newFixture(t)
	if _, err := f.service.List(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.TestModel(context.Background(), "fixture", "a1", "missing"); err == nil {
		t.Fatal("unknown model must fail")
	}
	f.runtime.providers[0].Accounts = nil
	if _, err := f.service.TestModel(context.Background(), "fixture", "", "model-a"); err == nil {
		t.Fatal("provider without accounts must fail")
	}
}

func TestModelProbeRetriesWithStreamingWhenRequired(t *testing.T) {
	f := newFixture(t)
	f.runtime.fetchQueue = []providerruntime.FetchResponse{
		{Status: 400, Body: []byte(`{"msg":"Non-stream chat request is currently not supported"}`)},
		{Status: 200, Body: []byte("data: {\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n\ndata: [DONE]\n")},
	}
	view, err := f.service.TestModel(context.Background(), "fixture", "a1", "model-a")
	if err != nil {
		t.Fatal(err)
	}
	if !view.OK || view.Status != 200 {
		t.Fatalf("streaming model test view: %+v", view)
	}
	if f.runtime.fetchCalls != 2 {
		t.Fatalf("stream fallback should issue two requests, got %d", f.runtime.fetchCalls)
	}
	if !strings.Contains(f.runtime.fetchRequest.Body, `"stream":true`) {
		t.Fatalf("stream fallback body = %s", f.runtime.fetchRequest.Body)
	}
}

func TestModelTestWritesAuditLog(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := listFixtureEnabled(t, f); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.TestModel(ctx, "fixture", "a1", "model-a"); err != nil {
		t.Fatal(err)
	}
	f.runtime.fetch = providerruntime.FetchResponse{
		Status: 401, Body: []byte(`{"error":"invalid token SECRET-BODY"}`),
	}
	if _, err := f.service.TestModel(ctx, "fixture", "a1", "model-a"); err != nil {
		t.Fatal(err)
	}

	rows, err := f.store.DB().QueryContext(ctx, `
SELECT state, level, message FROM logs
WHERE target = 'fixture' AND account_id = 'a1' AND operation = 'model_test'
ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var states, levels, messages []string
	for rows.Next() {
		var state, level, message string
		if err := rows.Scan(&state, &level, &message); err != nil {
			t.Fatal(err)
		}
		states = append(states, state)
		levels = append(levels, level)
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || states[0] != "completed" || states[1] != "failed_final" ||
		levels[0] != "info" || levels[1] != "error" {
		t.Fatalf("model test logs: states=%v levels=%v messages=%v", states, levels, messages)
	}
	if !strings.Contains(messages[0], "HTTP 200") ||
		!strings.Contains(messages[0], "模型可用") ||
		!strings.Contains(messages[1], "HTTP 401") ||
		strings.Contains(strings.Join(messages, " "), "SECRET-BODY") {
		t.Fatalf("model test audit messages: %v", messages)
	}
}

func TestListBackfillsMissingCheckinGrowth(t *testing.T) {
	f := newFixture(t)
	mustExec(t, f.store.DB(), `
INSERT INTO daily_ops
  (target, account_id, operation, day_key, state, credits, balance,
   credits_known, balance_known, message, updated_at)
VALUES
  ('fixture', 'a1', 'checkin', '2026-10-30', 'already', 12.5, 12.5,
   1, 1, '今天已领取', 'ts')`)
	f.runtime.checkin = providerruntime.Checkin{
		Outcome: "done", Credit: 100, Message: "今天已领取 +100 积分",
	}
	view, err := listFixtureEnabled(t, f)
	if err != nil {
		t.Fatal(err)
	}
	op := view.Providers[0].Accounts[0].Operations[0]
	if op.Credits == nil || *op.Credits != 100 ||
		!strings.Contains(op.Message, "+100") {
		t.Fatalf("check-in growth was not restored: %+v", op)
	}
	if f.runtime.checkinCalls != 1 {
		t.Fatalf("growth backfill calls = %d", f.runtime.checkinCalls)
	}
	if _, err := f.service.List(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.runtime.checkinCalls != 1 {
		t.Fatalf("growth backfill should run once per day, got %d calls", f.runtime.checkinCalls)
	}
}

func TestDefaultProviderEnablementIsWorkBuddyFirst(t *testing.T) {
	if !defaultProviderEnabled("workbuddy") {
		t.Fatal("workbuddy should be enabled by default")
	}
	for _, provider := range []string{
		"codearts", "qoder", "qoder-cn", "qwenwork", "trae-cn", "zcode",
		"minimax-code", "opencode-zen-free", "workbuddy-ai", "community-test",
	} {
		if defaultProviderEnabled(provider) {
			t.Errorf("%s should remain disabled by default", provider)
		}
	}
}

func TestListAndRuntimeErrors(t *testing.T) {
	f := newFixture(t)
	f.runtime.providerErrs = errors.New("providers failed")
	if _, err := f.service.List(context.Background()); err == nil {
		t.Fatal("providers error should surface")
	}

	svc := NewService(f.store.DB(), f.account, nil, nil, nil, time.Now)
	if _, err := svc.List(context.Background()); err == nil {
		t.Fatal("missing factory should fail")
	}
}

func TestSetEnabledAndTaskPreferences(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := listFixtureEnabled(t, f); err != nil {
		t.Fatal(err)
	}
	if err := f.service.SetEnabled(ctx, "fixture", false); err != nil {
		t.Fatal(err)
	}
	if err := f.service.SetAccountEnabled(ctx, "fixture", "a1", false); err != nil {
		t.Fatal(err)
	}
	if err := f.service.SetTaskEnabled(ctx, "fixture", "a1", "keepalive", false); err != nil {
		t.Fatal(err)
	}
	calls := f.runtime.providerCalls
	view, err := f.service.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if view.Providers[0].Enabled || view.Providers[0].Accounts[0].Enabled ||
		view.Providers[0].Accounts[0].TaskPreferences["keepalive"] {
		t.Fatalf("preferences not persisted: %+v", view.Providers[0])
	}
	if f.runtime.providerCalls <= calls {
		t.Fatal("List should resync runtime")
	}
	if err := f.service.SetEnabled(ctx, "missing", true); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing provider: %v", err)
	}
}

func TestHistoryHidesKeepaliveRunItems(t *testing.T) {
	f := newFixture(t)
	mustExec(t, f.store.DB(), `
INSERT INTO runs
  (id, started_at, finished_at, action, state, attempted, succeeded, needs_attention, skipped)
VALUES ('history-run', 'ts', 'ts', 'all', 'completed', 2, 2, 0, 0);
INSERT INTO run_items
  (run_id, provider, account_id, action, state, message, created_at)
VALUES ('history-run', 'fixture', 'a1', 'checkin', 'signed_confirmed', 'ok', 'ts');
INSERT INTO run_items
  (run_id, provider, account_id, action, state, message, created_at)
VALUES ('history-run', 'fixture', 'a1', 'keepalive', 'completed', 'hidden', 'ts');`)

	items, _, err := f.service.History(context.Background(), "fixture", "a1", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Action != "checkin" {
		t.Fatalf("keepalive history must stay hidden, got %+v", items)
	}
}

func TestLoginFlows(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.runtime.prompt = providerruntime.Prompt{Key: "phone", Message: "手机号"}
	view, err := f.service.BeginLogin(ctx, "fixture", 0, nil)
	if err != nil || view.Status != "prompt" || view.Prompt.Key != "phone" {
		t.Fatalf("prompt login: %+v %v", view, err)
	}

	f.runtime.prompt = providerruntime.Prompt{}
	view, err = f.service.BeginLogin(ctx, "fixture", 1, nil)
	if err != nil || view.Status != "api_key" || view.Method != "api" {
		t.Fatalf("api login: %+v %v", view, err)
	}

	f.runtime.authorize = providerruntime.Authorization{
		Session: "s1", URL: "https://login.example", Instructions: "完成授权",
	}
	view, err = f.service.BeginLogin(ctx, "fixture", 0, nil)
	if err != nil || view.Status != "authorized" || view.Session != "s1" {
		t.Fatalf("authorize login: %+v %v", view, err)
	}

	f.runtime.finish = providerruntime.Saved{Provider: "fixture", Account: "a1"}
	view, err = f.service.FinishLogin(ctx, "s1", "code")
	if err != nil || view.Status != "verified" {
		t.Fatalf("finish login: %+v %v", view, err)
	}
	f.runtime.apiKey = providerruntime.Saved{Provider: "fixture", Account: "a1"}
	view, err = f.service.LoginAPIKey(ctx, "fixture", 1, nil, "secret")
	if err != nil || view.Status != "verified" {
		t.Fatalf("api key login: %+v %v", view, err)
	}
}

func TestLoginErrors(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.runtime.promptErr = errors.New("prompt")
	if _, err := f.service.BeginLogin(ctx, "fixture", 0, nil); err == nil {
		t.Fatal("prompt error should surface")
	}
	f.runtime.promptErr = nil
	f.runtime.providerErrs = errors.New("providers")
	if _, err := f.service.BeginLogin(ctx, "fixture", 0, nil); err == nil {
		t.Fatal("provider discovery error should surface")
	}
	f.runtime.providerErrs = nil
	f.runtime.authorizeErr = errors.New("authorize")
	if _, err := f.service.BeginLogin(ctx, "fixture", 0, nil); err == nil {
		t.Fatal("authorize error should surface")
	}
	f.runtime.finishErr = errors.New("finish")
	if _, err := f.service.FinishLogin(ctx, "s", "c"); err == nil {
		t.Fatal("finish error should surface")
	}
	f.runtime.apiKeyErr = errors.New("api key")
	if _, err := f.service.LoginAPIKey(ctx, "fixture", 1, nil, "k"); err == nil {
		t.Fatal("api key error should surface")
	}
}

func TestRefreshUsageRecordsCreditsAndBalance(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	beforeAuthReads := f.runtime.readAuthCalls
	usage, err := f.service.RefreshUsage(ctx, "fixture", "a1")
	if err != nil || usage.Plan != "Pro" {
		t.Fatalf("usage: %+v %v", usage, err)
	}
	if f.runtime.readAuthCalls <= beforeAuthReads {
		t.Fatal("refresh must persist auth renewals from the plugin")
	}
	var credits, balance float64
	var creditsKnown, balanceKnown int
	err = f.store.DB().QueryRowContext(ctx, `
SELECT credits, balance, credits_known, balance_known
FROM daily_ops WHERE target = 'fixture' AND account_id = 'a1' AND operation = 'checkin'`,
	).Scan(&credits, &balance, &creditsKnown, &balanceKnown)
	if err != nil {
		t.Fatal(err)
	}
	if creditsKnown != 1 || balanceKnown != 1 || credits != 12.5 || balance != 12.5 {
		t.Fatalf("usage not recorded: %v %v %d %d", credits, balance, creditsKnown, balanceKnown)
	}
	var readings int
	if err := f.store.DB().QueryRowContext(ctx,
		`SELECT count(*) FROM quota_readings`).Scan(&readings); err != nil {
		t.Fatal(err)
	}
	if readings != 1 {
		t.Fatalf("quota readings = %d", readings)
	}

	f.runtime.usageErr = errors.New("usage")
	if _, err := f.service.RefreshUsage(ctx, "fixture", "a1"); err == nil {
		t.Fatal("usage error should surface")
	}
}

func TestRefreshUsagePersistsRenewedAuthSnapshot(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	keychain := &memorySecretStore{}
	file := &memorySecretStore{}
	f.service.credentials = capabilities.NewCredentialService(keychain, file, capabilities.Grant{
		"credentials.managed": true,
		"credentials.resolve": true,
	}, time.Now)

	f.runtime.auth = []byte(`{"fixture":{"a1":"renewed-success-token"}}`)
	if _, err := f.service.RefreshUsage(ctx, "fixture", "a1"); err != nil {
		t.Fatal(err)
	}
	if !storedSecretContains(keychain, "renewed-success-token") {
		t.Fatalf("额度刷新成功后未持久化插件续签认证: %+v", keychain.values)
	}

	keychain.values = map[string]string{}
	f.runtime.auth = []byte(`{"fixture":{"a1":"renewed-error-token"}}`)
	f.runtime.usageErr = errors.New("usage failed")
	if _, err := f.service.RefreshUsage(ctx, "fixture", "a1"); err == nil {
		t.Fatal("usage error should surface")
	}
	if !storedSecretContains(keychain, "renewed-error-token") {
		t.Fatalf("额度刷新失败后未保留插件续签认证: %+v", keychain.values)
	}
}

func storedSecretContains(store *memorySecretStore, want string) bool {
	for _, secret := range store.values {
		if strings.Contains(secret, want) {
			return true
		}
	}
	return false
}

func TestRunCheckinAndRefresh(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.service.Run(ctx, "fixture", "a1", "checkin"); err != nil {
		t.Fatal(err)
	}
	if f.runtime.checkinCalls != 1 {
		t.Fatalf("checkin calls = %d", f.runtime.checkinCalls)
	}
	if err := f.service.Run(ctx, "fixture", "a1", "refresh"); err != nil {
		t.Fatal(err)
	}
	if err := f.service.Run(ctx, "fixture", "a1", "unknown"); err == nil {
		t.Fatal("unknown operation should fail")
	}
	history, quota, err := f.service.History(ctx, "fixture", "a1", 10)
	if err != nil || len(history) != 2 || len(quota) == 0 {
		t.Fatalf("history: %d %d %v", len(history), len(quota), err)
	}
	for _, item := range history {
		if item.Account != "****a1" {
			t.Fatalf("history account was not masked: %+v", item)
		}
	}
}

func TestRunWritesPerAccountAuditLogs(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.service.Run(ctx, "fixture", "a1", "checkin"); err != nil {
		t.Fatal(err)
	}
	if err := f.service.Run(ctx, "fixture", "a1", "refresh"); err != nil {
		t.Fatal(err)
	}

	rows, err := f.store.DB().QueryContext(ctx, `
SELECT operation, state, level, message FROM logs
WHERE target = 'fixture' AND account_id = 'a1'
ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var operations, states, levels, messages []string
	for rows.Next() {
		var operation, state, level, message string
		if err := rows.Scan(&operation, &state, &level, &message); err != nil {
			t.Fatal(err)
		}
		operations = append(operations, operation)
		states = append(states, state)
		levels = append(levels, level)
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(operations) != 2 || operations[0] != "checkin" || operations[1] != "refresh" ||
		states[0] != "signed_provisional" || states[1] != "completed" ||
		levels[0] != "info" || levels[1] != "info" {
		t.Fatalf("run audit rows: operations=%v states=%v levels=%v", operations, states, levels)
	}
	if !strings.Contains(messages[0], "今日已领取 +100") ||
		!strings.Contains(messages[0], "当前额度 12.5") ||
		!strings.Contains(messages[1], "额度刷新") ||
		!strings.Contains(messages[1], "当前额度 12.5") {
		t.Fatalf("run audit messages: %v", messages)
	}
}

func TestRunAuditDoesNotMisleadWhenCreditsEqualBalance(t *testing.T) {
	value := 2365.22000153
	_, _, message := runResultAudit(runResult{
		Action: "checkin", State: "already", Executed: true,
		Credits: &value, Balance: &value, Message: "今日已领取",
	})
	if strings.Contains(message, "今日已领取 +") ||
		!strings.Contains(message, "当前额度 2365.22000153") {
		t.Fatalf("增长值与余额相同时不应误标增长值: %s", message)
	}
}

func TestFailedRunWritesRedactedAuditLog(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := listFixtureEnabled(t, f); err != nil {
		t.Fatal(err)
	}
	f.runtime.checkinErr = errors.New("upstream failed: token=SECRET-TOKEN")
	if err := f.service.Run(ctx, "fixture", "a1", "checkin"); err != nil {
		t.Fatal(err)
	}

	var state, level, message string
	if err := f.store.DB().QueryRowContext(ctx, `
SELECT state, level, message FROM logs
WHERE target = 'fixture' AND account_id = 'a1' AND operation = 'checkin'
ORDER BY id DESC LIMIT 1`).Scan(&state, &level, &message); err != nil {
		t.Fatal(err)
	}
	if state != "failed_retryable" || level != "error" ||
		!strings.Contains(message, "网络或上游暂时失败") ||
		strings.Contains(message, "SECRET-TOKEN") {
		t.Fatalf("失败审计日志 state=%s level=%s message=%s", state, level, message)
	}
}

func TestRunSetupFailureWritesAuditLog(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.service.List(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.service.SetEnabled(ctx, "fixture", false); err != nil {
		t.Fatal(err)
	}
	if err := f.service.Run(ctx, "fixture", "a1", "checkin"); err == nil {
		t.Fatal("disabled provider should reject the run")
	}

	var state, level, message string
	var account string
	if err := f.store.DB().QueryRowContext(ctx, `
SELECT account_id, state, level, message FROM logs
WHERE target = 'fixture' AND operation = 'checkin'
ORDER BY id DESC LIMIT 1`).Scan(&account, &state, &level, &message); err != nil {
		t.Fatal(err)
	}
	if account != "a1" || state != "failed_final" || level != "error" ||
		!strings.Contains(message, "签到失败") ||
		!strings.Contains(message, "供应商已停用") {
		t.Fatalf("运行前置异常日志 account=%s state=%s level=%s message=%s",
			account, state, level, message)
	}
}

func TestRunFailureLogsAfterContextCancellation(t *testing.T) {
	f := newFixture(t)
	if _, err := f.service.List(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := f.service.Run(ctx, "fixture", "a1", "checkin"); err == nil {
		t.Fatal("canceled context should reject the run")
	}

	var count int
	if err := f.store.DB().QueryRow(`
SELECT COUNT(*) FROM logs
WHERE target = 'fixture' AND account_id = 'a1' AND operation = 'checkin'
  AND state = 'failed_final' AND level = 'error'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("上下文取消后仍应写入失败审计日志，实际 %d 条", count)
	}
}

func TestLoginFailuresWriteRedactedAuditLog(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.runtime.promptErr = errors.New("prompt failed: token=SECRET-PROMPT")
	if _, err := f.service.BeginLogin(ctx, "fixture", 0, nil); err == nil {
		t.Fatal("prompt failure should surface")
	}
	f.runtime.finishErr = errors.New("oauth failed: access_token=SECRET-OAUTH")
	if _, err := f.service.FinishLogin(ctx, "session", "code"); err == nil {
		t.Fatal("finish login failure should surface")
	}
	f.runtime.apiKeyErr = errors.New("api key failed: api_key=SECRET-KEY")
	if _, err := f.service.LoginAPIKey(ctx, "fixture", 1, nil, "input"); err == nil {
		t.Fatal("api key login failure should surface")
	}

	rows, err := f.store.DB().QueryContext(ctx, `
SELECT level, message FROM logs WHERE operation = 'login' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var messages []string
	for rows.Next() {
		var level, message string
		if err := rows.Scan(&level, &message); err != nil {
			t.Fatal(err)
		}
		if level != "error" {
			t.Fatalf("login failure level = %s", level)
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(messages, " ")
	if len(messages) != 3 ||
		!strings.Contains(joined, "登录失败") ||
		strings.Contains(joined, "SECRET-PROMPT") ||
		strings.Contains(joined, "SECRET-OAUTH") ||
		strings.Contains(joined, "SECRET-KEY") {
		t.Fatalf("login audit logs: %v", messages)
	}
}

func TestRunCheckinRecordsClaimedCreditAndCurrentBalance(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.service.Run(ctx, "fixture", "a1", "checkin"); err != nil {
		t.Fatal(err)
	}
	var credits, balance float64
	var creditsKnown, balanceKnown int
	if err := f.store.DB().QueryRowContext(ctx, `
SELECT credits, balance, credits_known, balance_known
FROM daily_ops WHERE target = 'fixture' AND account_id = 'a1' AND operation = 'checkin'`,
	).Scan(&credits, &balance, &creditsKnown, &balanceKnown); err != nil {
		t.Fatal(err)
	}
	if creditsKnown != 1 || balanceKnown != 1 || credits != 100 || balance != 12.5 {
		t.Fatalf("check-in quota = %v/%d, balance = %v/%d",
			credits, creditsKnown, balance, balanceKnown)
	}
}

func TestRunCheckinKeepsClaimedCreditWhenUsageFails(t *testing.T) {
	f := newFixture(t)
	f.runtime.usageErr = errors.New("usage unavailable")
	ctx := context.Background()
	if err := f.service.Run(ctx, "fixture", "a1", "checkin"); err != nil {
		t.Fatal(err)
	}
	var credits float64
	var creditsKnown, balanceKnown int
	var message string
	if err := f.store.DB().QueryRowContext(ctx, `
SELECT credits, credits_known, balance_known, message
FROM daily_ops WHERE target = 'fixture' AND account_id = 'a1' AND operation = 'checkin'`,
	).Scan(&credits, &creditsKnown, &balanceKnown, &message); err != nil {
		t.Fatal(err)
	}
	if creditsKnown != 1 || balanceKnown != 0 || credits != 100 {
		t.Fatalf("claimed credit fallback = %v/%d, balance known = %d",
			credits, creditsKnown, balanceKnown)
	}
	if !strings.Contains(message, "刷新额度") {
		t.Fatalf("额度不可读时应给出刷新提示，实际 %q", message)
	}
}

func TestRunCheckinWithoutQuotaDoesNotFabricateUsage(t *testing.T) {
	f := newFixture(t)
	f.runtime.checkin.Credit = 0
	f.runtime.usageErr = errors.New("usage unavailable")
	ctx := context.Background()
	if err := f.service.Run(ctx, "fixture", "a1", "checkin"); err != nil {
		t.Fatal(err)
	}
	var creditsKnown, balanceKnown int
	var message string
	if err := f.store.DB().QueryRowContext(ctx, `
SELECT credits_known, balance_known, message
FROM daily_ops WHERE target = 'fixture' AND account_id = 'a1' AND operation = 'checkin'`,
	).Scan(&creditsKnown, &balanceKnown, &message); err != nil {
		t.Fatal(err)
	}
	if creditsKnown != 0 || balanceKnown != 0 {
		t.Fatalf("unknown quota should stay unknown: credits=%d balance=%d",
			creditsKnown, balanceKnown)
	}
	if !strings.Contains(message, "刷新额度") {
		t.Fatalf("额度不可读时应给出刷新提示，实际 %q", message)
	}
}

func TestRunAllFiltersAndIsolatesFailures(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := listFixtureEnabled(t, f); err != nil {
		t.Fatal(err)
	}
	if err := f.service.SetAccountEnabled(ctx, "fixture", "a2", false); err != nil {
		t.Fatal(err)
	}
	summary, err := f.service.RunAll(ctx, "checkin", nil)
	if err != nil || summary.Attempted != 1 || summary.Succeeded != 1 {
		t.Fatalf("checkin summary: %+v %v", summary, err)
	}

	if _, err := f.service.RunAll(ctx, "bad", nil); err == nil {
		t.Fatal("invalid action should fail")
	}

	f2 := newFixture(t)
	if _, err := listFixtureEnabled(t, f2); err != nil {
		t.Fatal(err)
	}
	f2.runtime.checkinErr = errors.New("only a1 fails")
	// Both accounts use the same fake; disable a2 so one retryable failure still
	// proves the aggregate path records attention without panicking.
	if err := f2.service.SetAccountEnabled(ctx, "fixture", "a2", false); err != nil {
		t.Fatal(err)
	}
	summary, err = f2.service.RunAll(ctx, "checkin", nil)
	if err != nil || summary.NeedsAttention != 1 || summary.Attempted != 1 {
		t.Fatalf("failure summary: %+v %v", summary, err)
	}
}

func TestRunAllTaskPreferencesAndExclusion(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := listFixtureEnabled(t, f); err != nil {
		t.Fatal(err)
	}
	if err := f.service.SetTaskEnabled(ctx, "fixture", "a1", "checkin", false); err != nil {
		t.Fatal(err)
	}
	summary, err := f.service.RunAll(ctx, "checkin", nil)
	if err != nil || summary.Skipped != 1 || summary.Attempted != 1 {
		t.Fatalf("task preference summary: %+v %v", summary, err)
	}

	f2 := newFixture(t)
	if _, err := listFixtureEnabled(t, f2); err != nil {
		t.Fatal(err)
	}
	summary, err = f2.service.RunAll(ctx, "all", map[string]bool{"fixture": true})
	if err != nil || summary.Attempted != 0 {
		t.Fatalf("excluded summary: %+v %v", summary, err)
	}
}

func TestKeepalivePaths(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name      string
		configure func(*fakeRuntime)
		wantState string
	}{
		{"load error", func(f *fakeRuntime) { f.loadErr = errors.New("load") }, "failed_retryable"},
		{"no fetch", func(f *fakeRuntime) { f.loader.Fetch = false }, "failed_final"},
		{"no model", func(f *fakeRuntime) {
			f.providers[0].Models = nil
		}, "failed_final"},
		{"http 401", func(f *fakeRuntime) {
			f.fetch.Status = 401
		}, "failed_final"},
		{"http 429", func(f *fakeRuntime) {
			f.fetch.Status = 429
		}, "failed_final"},
		{"http 500", func(f *fakeRuntime) {
			f.fetch.Status = 500
		}, "failed_retryable"},
		{"empty reply", func(f *fakeRuntime) {
			f.fetch.Body = []byte(`{}`)
		}, "failed_retryable"},
		{"success", func(*fakeRuntime) {}, "completed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			tc.configure(f.runtime)
			if err := f.service.Run(ctx, "fixture", "a1", "keepalive"); err != nil {
				t.Fatal(err)
			}
			var state string
			if err := f.store.DB().QueryRowContext(ctx, `
SELECT state FROM daily_ops
WHERE target='fixture' AND account_id='a1' AND operation='keepalive'`).Scan(&state); err != nil {
				t.Fatal(err)
			}
			if state != tc.wantState {
				t.Fatalf("state=%s want=%s", state, tc.wantState)
			}
		})
	}
}

func TestKeepaliveAnthropicProtocolAndFetchError(t *testing.T) {
	f := newFixture(t)
	f.runtime.providers[0].ID = "minimax-code"
	f.runtime.providers[0].Name = "MiniMax"
	if err := f.service.Run(context.Background(), "minimax-code", "a1", "keepalive"); err != nil {
		t.Fatal(err)
	}
	if f.runtime.fetchCalls != 1 {
		t.Fatal("fetch should be called")
	}

	f2 := newFixture(t)
	f2.runtime.fetchErr = errors.New("network")
	if err := f2.service.Run(context.Background(), "fixture", "a1", "keepalive"); err != nil {
		t.Fatal(err)
	}
}

func TestKeepaliveRetriesWithStreamingWhenRequired(t *testing.T) {
	f := newFixture(t)
	f.runtime.fetchQueue = []providerruntime.FetchResponse{
		{Status: 400, Body: []byte(`{"msg":"Non-stream chat request is currently not supported"}`)},
		{Status: 200, Body: []byte("data: {\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n\ndata: [DONE]\n")},
	}
	if err := f.service.Run(context.Background(), "fixture", "a1", "keepalive"); err != nil {
		t.Fatal(err)
	}
	if f.runtime.fetchCalls != 2 {
		t.Fatalf("stream fallback should issue two requests, got %d", f.runtime.fetchCalls)
	}
	if !strings.Contains(f.runtime.fetchRequest.Body, `"stream":true`) {
		t.Fatalf("stream fallback body = %s", f.runtime.fetchRequest.Body)
	}
}

func TestDeleteAccountAndRuntimeErrors(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.service.List(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.service.DeleteAccount(ctx, "fixture", "a1"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.account.Get(ctx, "fixture", "a1"); err == nil {
		t.Fatal("account should be deleted")
	}

	f.runtime.signOutErr = errors.New("signout")
	if err := f.service.DeleteAccount(ctx, "fixture", "a2"); err == nil {
		t.Fatal("sign out error should surface")
	}
}

func TestHistoryLimitsAndEmptyFilters(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.service.Run(ctx, "fixture", "a1", "refresh"); err != nil {
		t.Fatal(err)
	}
	items, readings, err := f.service.History(ctx, "", "", 0)
	if err != nil || len(items) != 1 || len(readings) != 1 {
		t.Fatalf("history all: %d %d %v", len(items), len(readings), err)
	}
	items, readings, err = f.service.History(ctx, "fixture", "missing", 500)
	if err != nil || len(items) != 0 || len(readings) != 0 {
		t.Fatalf("history missing: %d %d %v", len(items), len(readings), err)
	}
}

func TestRecordRunStateAndRedaction(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	err := f.service.recordRun(ctx, "run-1", "2026-10-30T00:00:00Z", "all", []runResult{
		{Provider: "p", Account: "a", Action: "checkin", State: "failed_final", Executed: true},
		{Provider: "p", Account: "a", Action: "keepalive", State: "completed", Executed: false},
		{Provider: "p", Account: "a", Action: "checkin", State: "failed_retryable", Err: errors.New("x")},
	})
	if err != nil {
		t.Fatal(err)
	}
	var state string
	var attention, skipped, attempted int
	if err := f.store.DB().QueryRowContext(ctx, `
SELECT state, needs_attention, skipped, attempted FROM runs WHERE id='run-1'`,
	).Scan(&state, &attention, &skipped, &attempted); err != nil {
		t.Fatal(err)
	}
	if state != "failed_retryable" || attention != 2 || skipped != 2 || attempted != 1 {
		t.Fatalf("run aggregate: %s %d %d %d", state, attention, skipped, attempted)
	}

	if got := redactMessage("Authorization: Bearer abc123 token=secret " + strings.Repeat("x", 600)); strings.Contains(got, "abc123") ||
		strings.Contains(got, "secret") || len([]rune(got)) > 501 {
		t.Fatalf("redaction failed: %s", got)
	}
	if got := redactMessage("  "); got != "" {
		t.Fatal("whitespace should redact to empty")
	}
}

func TestPersistAuthAndClose(t *testing.T) {
	f := newFixture(t)
	keychain := &memorySecretStore{}
	file := &memorySecretStore{}
	creds := capabilities.NewCredentialService(keychain, file, capabilities.Grant{
		"credentials.managed": true,
		"credentials.resolve": true,
	}, time.Now)
	f.service.credentials = creds
	f.runtime.auth = []byte(`{"fixture":{"a1":"secret"}}`)
	if err := f.service.persistAuth(context.Background(), f.runtime); err != nil {
		t.Fatal(err)
	}
	if len(keychain.values) != 1 {
		t.Fatalf("auth not persisted: %+v", keychain.values)
	}
	if err := f.service.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.service.Close(); err != nil {
		t.Fatal(err)
	}

	f.runtime.auth = []byte(`{}`)
	f.service.host = f.runtime
	f.runtime.removeAuth = errors.New("remove")
	f.runtime.closeErr = errors.New("close")
	if err := f.service.persistAuth(context.Background(), f.runtime); err != nil {
		t.Fatal(err)
	}
	if err := f.service.Close(); err == nil {
		t.Fatal("close errors should be joined")
	}
}

func TestPersistAuthErrors(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.runtime.auth = nil
	f.runtime.authErr = nil
	if err := f.service.persistAuth(ctx, f.runtime); err != nil {
		t.Fatal(err)
	}
	f.runtime.authErr = errors.New("read")
	if err := f.service.persistAuth(ctx, f.runtime); err == nil {
		t.Fatal("read error should surface")
	}
	f.runtime.authErr = nil
	f.runtime.auth = []byte(`not-json`)
	if err := f.service.persistAuth(ctx, f.runtime); err == nil {
		t.Fatal("invalid auth JSON should surface")
	}
	f.runtime.auth = []byte(`{"x":1}`)
	if err := f.service.persistAuth(ctx, f.runtime); err == nil {
		t.Fatal("missing credential service should surface")
	}
	f.service.credentials = capabilities.NewCredentialService(
		&memorySecretStore{err: errors.New("keychain")},
		&memorySecretStore{err: errors.New("file")},
		capabilities.Grant{"credentials.managed": true}, time.Now)
	if err := f.service.persistAuth(ctx, f.runtime); err == nil {
		t.Fatal("credential persist failure should surface")
	}
}

func TestHelpers(t *testing.T) {
	if !hasReply([]byte("plain text")) || !hasReply([]byte(`{"output_text":"ok"}`)) ||
		hasReply([]byte(`{}`)) {
		t.Fatal("hasReply mismatch")
	}
	if !hasReply([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n\ndata: [DONE]\n")) {
		t.Fatal("SSE reply should count as a keepalive response")
	}
	if !keepaliveNeedsStream(400, []byte(`{"msg":"Non-stream chat request is currently not supported"}`)) ||
		keepaliveNeedsStream(200, []byte(`{"choices":[]}`)) {
		t.Fatal("stream fallback detection mismatch")
	}
	if kindForCheckin("done") != engine.RAlready ||
		kindForCheckin("claimed") != engine.RSuccess ||
		kindForCheckin("ineligible") != engine.RIneligible ||
		kindForCheckin("inactive") != engine.RInactive ||
		kindForCheckin("captcha") != engine.RManualRequired ||
		kindForCheckin("failed") != engine.RFinal ||
		kindForCheckin("other") != engine.RRetryable {
		t.Fatal("checkin kind mapping mismatch")
	}
	value := 7.0
	if credits, balance := usageValues(providerruntime.Usage{Windows: []providerruntime.UsageWindow{
		{Balance: &value},
	}}); credits == nil || balance == nil || *credits != 7 || *balance != 7 {
		t.Fatal("usageValues mismatch")
	}
	emptyCredits, emptyBalance := usageValues(providerruntime.Usage{})
	if emptyCredits != nil || emptyBalance != nil {
		t.Fatal("empty usageValues should be nil")
	}
	if once := usageCredits(providerruntime.Usage{Windows: []providerruntime.UsageWindow{{Balance: &value}}}); once == nil || *once != 7 {
		t.Fatal("usageCredits mismatch")
	}
	if fallbackCredits, fallbackBalance := usageValues(providerruntime.Usage{
		Balance: "1,234.5 credits",
	}); fallbackCredits == nil || fallbackBalance == nil ||
		*fallbackCredits != 1234.5 || *fallbackBalance != 1234.5 {
		t.Fatal("top-level usage balance should be parsed")
	}
	if _, ok := parseUsageBalance("not reported"); ok {
		t.Fatal("non-numeric balance must stay unknown")
	}
	if zero, ok := parseUsageBalance("-2 credits"); !ok || zero != 0 {
		t.Fatal("negative balance should be clamped to zero")
	}
	if _, ok := parseUsageBalance(strings.Repeat("9", 1000)); ok {
		t.Fatal("overflow balance must stay unknown")
	}
	if appendMessage("", "提示") != "提示" ||
		appendMessage("完成", "") != "完成" ||
		appendMessage("完成", "提示") != "完成；提示" {
		t.Fatal("message suffix mismatch")
	}
	for input, want := range map[string]string{
		"":                "****",
		"abc":             "****abc",
		"13800138000":     "****8000",
		"张三丰":             "****张三丰",
		"研发用户甲乙丙丁":        "****甲乙丙丁",
		"开发者🙂账号":          "****者🙂账号",
		"王小明@example.com": "王小***@example.com",
	} {
		if got := maskIdentity(input); got != want {
			t.Errorf("maskIdentity(%q) = %q, want %q", input, got, want)
		}
	}
	if boolInt(true) != 1 || boolInt(false) != 0 || firstNonEmpty("", "x") != "x" ||
		firstNonEmpty("", "") != "" {
		t.Fatal("small helper mismatch")
	}
	actual, ok := nullableFloat(&value).(float64)
	if nullableFloat(nil) != nil || !ok || actual != 7 {
		t.Fatal("nullableFloat mismatch")
	}
	if !strings.HasSuffix(StaleAuthPath("/tmp/qka"), "provider-auth.tmp.json") {
		t.Fatal("stale auth path mismatch")
	}
}

func TestProviderOperationsHelpers(t *testing.T) {
	v := ProviderView{Checkin: true, Keepalive: false}
	a := AccountView{TaskPreferences: map[string]bool{"keepalive": false}}
	if ops := providerOperations(v, a, "all"); len(ops) != 1 || ops[0] != "checkin" {
		t.Fatalf("ops: %+v", ops)
	}
	v.Keepalive = true
	a.TaskPreferences["keepalive"] = true
	if ops := providerOperations(v, a, "keepalive"); len(ops) != 0 {
		t.Fatalf("keepalive ops: %+v", ops)
	}
	if !enabledPref(nil, "checkin") || enabledPref(map[string]bool{"checkin": false}, "checkin") {
		t.Fatal("enabledPref mismatch")
	}
	if operationLabel("checkin", v) != "checkin" ||
		operationLabel("all", v) != "checkin" ||
		operationLabel("all", ProviderView{Checkin: false}) != "checkin" {
		t.Fatal("operationLabel mismatch")
	}
	if id := newRunID(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)); !strings.HasPrefix(id, "20260101T000000-") {
		t.Fatalf("run id: %s", id)
	}
}

func TestAccountSlotIdentityChangeIsolatesHistory(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.service.List(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.service.SetTaskEnabled(ctx, "fixture", "a1", "keepalive", false); err != nil {
		t.Fatal(err)
	}
	mustExec(t, f.store.DB(), `
INSERT INTO daily_ops
  (target, account_id, operation, day_key, state, message, updated_at)
VALUES ('fixture', 'a1', 'checkin', '2026-10-30', 'signed_confirmed', 'old identity', 'ts');
INSERT INTO logs (ts, target, account_id, operation, state, level, message)
VALUES ('ts', 'fixture', 'a1', 'checkin', 'signed_confirmed', 'info', 'old identity');
INSERT INTO runs
  (id, started_at, finished_at, action, state, attempted, succeeded, needs_attention, skipped)
VALUES ('old-run', 'ts', 'ts', 'all', 'completed', 1, 1, 0, 0);
INSERT INTO run_items
  (run_id, provider, account_id, action, state, message, created_at)
VALUES ('old-run', 'fixture', 'a1', 'checkin', 'signed_confirmed', 'old identity', 'ts');
INSERT INTO quota_readings
  (provider, account_id, observed_at, balance, windows_json)
VALUES ('fixture', 'a1', 'ts', '99', '[]');`)
	var oldHash string
	if err := f.store.DB().QueryRowContext(ctx, `
SELECT identity_hash FROM provider_account_identities
WHERE provider = 'fixture' AND slot_key = 'a1'`).Scan(&oldHash); err != nil {
		t.Fatal(err)
	}

	f.runtime.providers[0].Accounts[0].AccountID = "new@example.com"
	view, err := f.service.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"daily_ops", "logs", "quota_readings", "run_items"} {
		var count int
		column := "account_id"
		target := "target"
		if table == "quota_readings" || table == "run_items" {
			target = "provider"
		}
		if err := f.store.DB().QueryRowContext(ctx,
			"SELECT count(*) FROM "+table+" WHERE "+target+" = 'fixture' AND "+column+" = 'a1'",
		).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s retained old identity rows: %d", table, count)
		}
	}
	var orphanRuns int
	if err := f.store.DB().QueryRowContext(ctx,
		`SELECT count(*) FROM runs WHERE id = 'old-run'`).Scan(&orphanRuns); err != nil {
		t.Fatal(err)
	}
	if orphanRuns != 0 {
		t.Fatal("orphan run aggregate should be removed")
	}
	var newHash string
	if err := f.store.DB().QueryRowContext(ctx, `
SELECT identity_hash FROM provider_account_identities
WHERE provider = 'fixture' AND slot_key = 'a1'`).Scan(&newHash); err != nil {
		t.Fatal(err)
	}
	if newHash == oldHash {
		t.Fatal("slot identity hash should change")
	}
	found := false
	for _, account := range view.Providers[0].Accounts {
		if account.Key != "a1" {
			continue
		}
		found = true
		if !account.Enabled || account.MaskedIdentity != "ne***@example.com" ||
			!account.TaskPreferences["checkin"] {
			t.Fatalf("new identity should start enabled with default preferences: %+v", account)
		}
	}
	if !found {
		t.Fatal("new identity account missing")
	}
}

func TestAccountSlotWithoutUpstreamIdentityKeepsHistory(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.service.List(ctx); err != nil {
		t.Fatal(err)
	}
	mustExec(t, f.store.DB(), `
INSERT INTO daily_ops
  (target, account_id, operation, day_key, state, updated_at)
VALUES ('fixture', 'a1', 'checkin', '2026-10-30', 'already', 'ts');`)
	f.runtime.providers[0].Accounts[0].AccountID = ""
	if _, err := f.service.List(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := f.store.DB().QueryRowContext(ctx, `
SELECT count(*) FROM daily_ops
WHERE target = 'fixture' AND account_id = 'a1'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("missing upstream identity must not reset history")
	}
}

func TestSyncRemovesMissingPluginSourcesOnly(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.service.List(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.account.ImportSource(ctx, "fixture", "a2", "external"); err != nil {
		t.Fatal(err)
	}
	f.runtime.providers[0].Accounts = f.runtime.providers[0].Accounts[:1]
	view, err := f.service.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Providers[0].Accounts) != 1 || view.Providers[0].Accounts[0].Key != "a1" {
		t.Fatalf("runtime account view mismatch: %+v", view.Providers[0].Accounts)
	}
	account, err := f.account.Get(ctx, "fixture", "a2")
	if err != nil {
		t.Fatal(err)
	}
	if len(account.Sources) != 1 || account.Sources[0].ID != "external" {
		t.Fatalf("non-plugin source must survive: %+v", account.Sources)
	}

	f.runtime.providers[0].Accounts = nil
	if _, err := f.service.List(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.account.Get(ctx, "fixture", "a1"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing sole plugin source should remove account: %v", err)
	}
	if _, err := f.account.Get(ctx, "fixture", "a2"); err != nil {
		t.Fatalf("imported account must survive empty runtime: %v", err)
	}
}

// TestRedactMessageCoversIdentityLeaks 覆盖 task-3 目标：provider 侧写日志/历史
// 前必须把 uid、手机号、邮箱一并掩码，同时保留中文与可读数字。
func TestRedactMessageCoversIdentityLeaks(t *testing.T) {
	got := redactMessage("签到失败 uid:1234567890 手机 13812345678 邮箱 alice@example.com，" +
		"Authorization: Bearer abcdefghijklmnop，api_key=sk-1234567890abcdef，当前额度 2351.71")
	for _, forbidden := range []string{
		"1234567890", "13812345678", "alice@example.com",
		"abcdefghijklmnop", "sk-1234567890abcdef",
	} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("redacted message still contains %q: %s", forbidden, got)
		}
	}
	for _, want := range []string{
		"签到失败", "uid:12***90", "138****5678", "a***@example.com",
		"[redacted]", "当前额度 2351.71",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("redacted message missing %q: %s", want, got)
		}
	}
}
