package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"qka/internal/accounts"
	"qka/internal/capabilities"
	"qka/internal/engine"
	"qka/internal/providerruntime"
	"qka/internal/providers"
	"qka/internal/status"
	"qka/internal/store"
	"qka/internal/workbuddy"
)

func TestManageAccountsProviderDeleteBranches(t *testing.T) {
	t.Run("provider delete", func(t *testing.T) {
		a := coverageApp(t)
		ctx := context.Background()
		fake := &fakeProviderRuntime{providers: []providerruntime.Provider{{
			ID: "fixture", Name: "Fixture", Checkin: true,
			Accounts: []providerruntime.Account{{Key: "a1"}},
		}}}
		a.cfg.ProviderPluginsRoot = "plugins"
		a.providers = providers.NewService(
			a.store.DB(), a.accounts, a.credentials, a.engine,
			func(context.Context) (providers.Runtime, error) { return fake, nil },
			a.now,
		)
		if _, err := a.providers.List(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := a.manageAccounts(ctx, map[string]any{
			"action": "delete", "target": "fixture", "account_id": "a1",
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := a.accounts.Get(ctx, "fixture", "a1"); err == nil {
			t.Fatal("provider-owned account should be deleted")
		}
	})

	t.Run("provider lookup error", func(t *testing.T) {
		a := coverageApp(t)
		a.cfg.ProviderPluginsRoot = "plugins"
		a.providers = providers.NewService(
			a.store.DB(), a.accounts, a.credentials, a.engine, nil, a.now,
		)
		if _, err := a.store.DB().Exec(`DROP TABLE providers`); err != nil {
			t.Fatal(err)
		}
		if _, err := a.manageAccounts(context.Background(), map[string]any{
			"action": "delete", "target": "fixture", "account_id": "a1",
		}); err == nil {
			t.Fatal("provider ownership lookup failure must surface")
		}
	})
}

func TestProviderRuntimeFactoryRejectsStaleAuthReadError(t *testing.T) {
	oldDiscover, oldLaunch := discoverProviderPlugins, launchProviderRuntime
	t.Cleanup(func() {
		discoverProviderPlugins, launchProviderRuntime = oldDiscover, oldLaunch
	})
	discoverProviderPlugins = func(string) ([]providerruntime.Plugin, error) {
		return []providerruntime.Plugin{{Spec: "fixture", Target: "fixture"}}, nil
	}
	launchProviderRuntime = func(context.Context, providerruntime.LaunchConfig) (*providerruntime.Host, error) {
		t.Fatal("launch must not run after stale auth cleanup failure")
		return nil, nil
	}

	root := t.TempDir()
	configFile := filepath.Join(root, "config-file")
	if err := os.WriteFile(configFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := &App{cfg: Config{
		ConfigDir: configFile, ProviderPluginsRoot: filepath.Join(root, "plugins"),
	}}
	if _, err := a.providerRuntimeFactory()(context.Background()); err == nil {
		t.Fatal("stale auth cleanup failure must surface")
	}
}

func TestRunAllSkipsObservationThatDidNotExecute(t *testing.T) {
	a := coverageApp(t)
	ctx := context.Background()
	if err := a.accounts.Discover(ctx, "legacy", []accounts.DiscoveredAccount{{
		ID: "a1", SourceID: "s1", SourceKind: accounts.KindManaged,
		Health: accounts.HealthHealthy,
	}}); err != nil {
		t.Fatal(err)
	}
	a.status = status.NewService(a.store.DB(), a.now, func(string) []string {
		return []string{"checkin"}
	})
	old := executeRunOperation
	t.Cleanup(func() { executeRunOperation = old })
	executeRunOperation = func(
		*App, context.Context, string, string, string,
	) (any, error) {
		return engine.Observation{Executed: false, State: engine.Pending}, nil
	}
	summary, err := a.RunAllActionSummary(ctx, "checkin")
	if err != nil {
		t.Fatal(err)
	}
	if summary.Skipped != 1 || summary.Attempted != 0 {
		t.Fatalf("non-executed observation should be skipped: %+v", summary)
	}
}

func TestRunAllProviderRecordFailureIsReturned(t *testing.T) {
	a := coverageApp(t)
	ctx := context.Background()
	if err := a.accounts.Discover(ctx, "legacy", []accounts.DiscoveredAccount{{
		ID: "adapter", SourceID: "source", SourceKind: accounts.KindManaged,
		Health: accounts.HealthHealthy,
	}}); err != nil {
		t.Fatal(err)
	}
	a.status = status.NewService(a.store.DB(), a.now, func(string) []string {
		return []string{"checkin"}
	})
	fake := &fakeProviderRuntime{providers: []providerruntime.Provider{{
		ID: "provider", Name: "Provider", Checkin: true,
		Accounts: []providerruntime.Account{{Key: "provider-account"}},
	}}}
	a.cfg.ProviderPluginsRoot = "plugins"
	a.providers = providers.NewService(
		a.store.DB(), a.accounts, a.credentials, a.engine,
		func(context.Context) (providers.Runtime, error) { return fake, nil },
		a.now,
	)
	old := executeRunOperation
	t.Cleanup(func() { executeRunOperation = old })
	executeRunOperation = func(
		*App, context.Context, string, string, string,
	) (any, error) {
		if _, err := a.store.DB().Exec(`
CREATE TRIGGER fail_provider_run_insert BEFORE INSERT ON runs
BEGIN SELECT RAISE(ABORT, 'run insert failed'); END`); err != nil {
			t.Fatal(err)
		}
		return engine.Observation{Executed: true, State: engine.Already}, nil
	}
	summary, err := a.RunAllActionSummary(ctx, "checkin")
	if err == nil {
		t.Fatal("provider record failure must surface")
	}
	if summary.Succeeded == 0 {
		t.Fatalf("adapter result should still be counted: %+v", summary)
	}
}

func TestWorkBuddyQuotaReadingWriteFailureIsReturned(t *testing.T) {
	now := workBuddyCoverageNow()
	target := &workBuddyCheckinTarget{
		active: true, claimCredit: 100,
		balancePackages: []map[string]any{{
			"CycleRemainCapacity": "1200",
		}},
	}
	srv := target.serve(t)
	defer srv.Close()
	a, _, accountID := newWorkBuddyCheckinApp(t, srv.URL, now)
	execCoverageSQL(t, a, `
CREATE TRIGGER fail_workbuddy_quota_reading BEFORE INSERT ON quota_readings
BEGIN SELECT RAISE(ABORT, 'quota reading failed'); END`)
	if _, err := a.runWorkBuddyCheckin(context.Background(), accountID); err == nil {
		t.Fatal("quota reading write failure must surface")
	}
	if err := a.refreshWorkBuddyUsage(context.Background(), accountID); err == nil {
		t.Fatal("read-only quota refresh write failure must surface")
	}
}

func TestWorkBuddyVerificationManagedSourceFailure(t *testing.T) {
	a, acct, _ := workBuddyCoverageApp(t)
	ctx := context.Background()
	verified := workbuddyVerifiedAccount()
	creds := newWorkBuddyCredentialService(t)
	execCoverageSQL(t, a, `
CREATE TRIGGER fail_managed_source_update BEFORE UPDATE ON sources
BEGIN SELECT RAISE(ABORT, 'source update failed'); END`)
	if _, err := newWorkBuddyVerificationSink(acct, creds).AcceptVerifiedAccount(
		ctx, verified, []byte("secret"),
	); err == nil {
		t.Fatal("managed source establishment failure must surface")
	}
}

func workBuddyCoverageNow() time.Time {
	return time.Date(2026, 10, 29, 14, 0, 0, 0, engine.Beijing)
}

func workBuddyCoverageApp(t *testing.T) (*App, *accounts.Service, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	acct := accounts.NewService(st.DB())
	now := workBuddyCoverageNow()
	return &App{
		cfg: Config{}, now: func() time.Time { return now },
		store: st, accounts: acct,
		engine: engine.New(st.DB(), func() time.Time { return now }, "test"),
	}, acct, st
}

func workbuddyVerifiedAccount() workbuddy.VerifiedAccount {
	return workbuddy.VerifiedAccount{
		Target: "workbuddy-cn", AccountID: "wb", SourceID: "source",
		DisplayName: "WB", MaskedIdentity: "****",
	}
}

func newWorkBuddyCredentialService(t *testing.T) *capabilities.CredentialService {
	t.Helper()
	return capabilities.NewCredentialService(
		failingAppSecretStore{}, newAppSecretStore(),
		capabilities.Grant{
			"credentials.managed": true,
			"credentials.resolve": true,
		}, nil,
	)
}
