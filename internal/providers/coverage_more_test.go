package providers

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"qka/internal/engine"
	"qka/internal/providerruntime"
)

func TestRuntimeProviderAndCapabilityBranches(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.service.runtimeProvider(ctx, f.runtime, "missing"); err == nil {
		t.Fatal("missing provider must fail")
	}
	f.runtime.providerErrs = errors.New("providers failed")
	if _, err := f.service.runtimeProvider(ctx, f.runtime, "fixture"); err == nil {
		t.Fatal("provider list failure must surface")
	}

	provider := providerFixture()
	provider.Checkin = false
	if err := checkOperationCapability(provider, "checkin"); err == nil {
		t.Fatal("unsupported checkin must fail")
	}
	provider = providerFixture()
	provider.Fetch = true
	provider.APIURL = ""
	provider.Models = nil
	if err := checkOperationCapability(provider, "keepalive"); err == nil {
		t.Fatal("unsupported keepalive must fail")
	}
	provider = providerFixture()
	provider.Usage = false
	if err := checkOperationCapability(provider, "refresh"); err == nil {
		t.Fatal("unsupported refresh must fail")
	}
	if err := checkOperationCapability(provider, "unknown"); err == nil {
		t.Fatal("unknown operation must fail")
	}
}

func TestProviderKeepaliveSupportedBranches(t *testing.T) {
	if providerKeepaliveSupported(providerruntime.Provider{}) {
		t.Fatal("fetch-disabled provider must not support keepalive")
	}
	if !providerKeepaliveSupported(providerruntime.Provider{
		Fetch: true, APIURL: "https://provider.example",
	}) {
		t.Fatal("provider API URL should enable keepalive")
	}
	if !providerKeepaliveSupported(providerruntime.Provider{
		Fetch:  true,
		Models: []providerruntime.Model{{APIURL: "https://model.example"}},
	}) {
		t.Fatal("model API URL should enable keepalive")
	}
	if providerKeepaliveSupported(providerruntime.Provider{
		Fetch:  true,
		Models: []providerruntime.Model{{}},
	}) {
		t.Fatal("empty model URLs must not enable keepalive")
	}
}

func TestVerifyLoginBranches(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	saved := providerruntime.Saved{Provider: "fixture", Account: "a1"}

	provider := providerFixture()
	provider.Usage = false
	f.runtime.providers = []providerruntime.Provider{provider}
	if err := f.service.verifyLogin(ctx, f.runtime, saved); err != nil {
		t.Fatal(err)
	}

	f.runtime.providers = []providerruntime.Provider{providerFixture()}
	f.runtime.usageErr = errors.New("usage unavailable")
	if err := f.service.verifyLogin(ctx, f.runtime, saved); err == nil {
		t.Fatal("usage error must fail login verification")
	}

	f.runtime.usageErr = nil
	f.runtime.usage = providerruntime.Usage{Error: "access token expired"}
	if err := f.service.verifyLogin(ctx, f.runtime, saved); err == nil ||
		!strings.Contains(err.Error(), "登录验证失败") {
		t.Fatalf("usage error text must fail login verification: %v", err)
	}
}

func TestEnsureEnabledBranches(t *testing.T) {
	ctx := context.Background()

	t.Run("provider query error", func(t *testing.T) {
		f := newFixture(t)
		mustExec(t, f.store.DB(), `DROP TABLE providers`)
		if err := f.service.ensureEnabled(ctx, "fixture", "a1"); err == nil {
			t.Fatal("provider query error must surface")
		}
	})

	t.Run("provider disabled", func(t *testing.T) {
		f := newFixture(t)
		if _, err := f.service.List(ctx); err != nil {
			t.Fatal(err)
		}
		if err := f.service.SetEnabled(ctx, "fixture", false); err != nil {
			t.Fatal(err)
		}
		if err := f.service.ensureEnabled(ctx, "fixture", "a1"); err == nil {
			t.Fatal("disabled provider must be rejected")
		}
	})

	t.Run("empty account is allowed", func(t *testing.T) {
		f := newFixture(t)
		if _, err := listFixtureEnabled(t, f); err != nil {
			t.Fatal(err)
		}
		if err := f.service.ensureEnabled(ctx, "fixture", ""); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("account query error", func(t *testing.T) {
		f := newFixture(t)
		if _, err := f.service.List(ctx); err != nil {
			t.Fatal(err)
		}
		mustExec(t, f.store.DB(), `DROP TABLE accounts`)
		if err := f.service.ensureEnabled(ctx, "fixture", "a1"); err == nil {
			t.Fatal("account query error must surface")
		}
	})

	t.Run("account disabled", func(t *testing.T) {
		f := newFixture(t)
		if _, err := f.service.List(ctx); err != nil {
			t.Fatal(err)
		}
		if err := f.service.SetAccountEnabled(ctx, "fixture", "a1", false); err != nil {
			t.Fatal(err)
		}
		if err := f.service.ensureEnabled(ctx, "fixture", "a1"); err == nil {
			t.Fatal("disabled account must be rejected")
		}
	})
}

func TestListViewAndProviderPreferencesErrors(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.runtime.providers = nil
	mustExec(t, f.store.DB(), `DROP TABLE providers`)
	if _, err := f.service.List(ctx); err == nil {
		t.Fatal("view provider preference error must surface")
	}

	f = newFixture(t)
	mustExec(t, f.store.DB(), `DROP TABLE providers`)
	if _, err := f.service.providerPrefs(ctx); err == nil {
		t.Fatal("provider preference query error must surface")
	}
}

func TestSyncAndIdentityIsolationBranches(t *testing.T) {
	ctx := context.Background()

	t.Run("sync query error", func(t *testing.T) {
		queryErr := errors.New("identity lookup failed")
		db := openCoverageDB(t, &coverageDBConfig{
			query: func(string) (driver.Rows, error) { return nil, queryErr },
		})
		svc := NewService(db, nil, nil, nil, nil, fixedNow)
		err := svc.syncAccounts(ctx, providerruntime.Provider{
			ID: "p",
			Accounts: []providerruntime.Account{{
				Key: "slot", AccountID: "identity",
			}},
		})
		if !errors.Is(err, queryErr) {
			t.Fatalf("sync identity error = %v", err)
		}
	})

	t.Run("begin transaction error", func(t *testing.T) {
		beginErr := errors.New("begin failed")
		db := openCoverageDB(t, &coverageDBConfig{
			beginErr: beginErr,
			query: func(string) (driver.Rows, error) {
				return &coverageRows{
					columns: []string{"identity_hash"},
					values:  [][]driver.Value{{"old"}},
				}, nil
			},
		})
		svc := NewService(db, nil, nil, nil, nil, fixedNow)
		if err := svc.reconcileAccountIdentity(ctx, "p",
			providerruntime.Account{Key: "slot", AccountID: "new"}); !errors.Is(err, beginErr) {
			t.Fatalf("identity begin error = %v", err)
		}
	})

	for _, tc := range []struct {
		name   string
		failAt int
	}{
		{"clear logs", 1},
		{"remove empty runs", 5},
		{"delete account", 6},
		{"update identity", 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			execErr := errors.New(tc.name)
			calls := 0
			db := openCoverageDB(t, &coverageDBConfig{
				query: func(string) (driver.Rows, error) {
					return &coverageRows{
						columns: []string{"identity_hash"},
						values:  [][]driver.Value{{"old"}},
					}, nil
				},
				exec: func(string) (driver.Result, error) {
					calls++
					if calls == tc.failAt {
						return nil, execErr
					}
					return coverageResult{affected: 1}, nil
				},
			})
			svc := NewService(db, nil, nil, nil, nil, fixedNow)
			if err := svc.reconcileAccountIdentity(ctx, "p",
				providerruntime.Account{Key: "slot", AccountID: "new"}); !errors.Is(err, execErr) {
				t.Fatalf("%s error = %v", tc.name, err)
			}
		})
	}
}

func TestPruneMissingPluginSourceError(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.service.List(ctx); err != nil {
		t.Fatal(err)
	}
	mustExec(t, f.store.DB(), `
CREATE TRIGGER fail_prune_source_delete BEFORE DELETE ON sources
BEGIN SELECT RAISE(ABORT, 'source delete failed'); END`)
	if err := f.service.pruneMissingPluginSources(ctx, "fixture", map[string]bool{}); err == nil {
		t.Fatal("source removal error must surface")
	}
}

func TestDeleteRefreshRunAndProviderLookupBranches(t *testing.T) {
	ctx := context.Background()

	t.Run("delete runtime unavailable", func(t *testing.T) {
		f := newFixture(t)
		if err := f.service.Close(); err != nil {
			t.Fatal(err)
		}
		f.service.factory = nil
		if err := f.service.DeleteAccount(ctx, "fixture", "a1"); err == nil {
			t.Fatal("delete runtime failure must surface")
		}
	})

	t.Run("refresh disabled provider", func(t *testing.T) {
		f := newFixture(t)
		if _, err := f.service.List(ctx); err != nil {
			t.Fatal(err)
		}
		if err := f.service.SetEnabled(ctx, "fixture", false); err != nil {
			t.Fatal(err)
		}
		if _, err := f.service.RefreshUsage(ctx, "fixture", "a1"); err == nil {
			t.Fatal("refresh on disabled provider must fail")
		}
	})

	t.Run("refresh missing runtime provider", func(t *testing.T) {
		f := newFixture(t)
		f.runtime.providers = []providerruntime.Provider{{ID: "other", Name: "Other"}}
		if _, err := f.service.RefreshUsage(ctx, "fixture", "a1"); err == nil {
			t.Fatal("refresh with unknown provider must fail")
		}
	})

	t.Run("refresh usage unsupported", func(t *testing.T) {
		f := newFixture(t)
		f.runtime.providers[0].Usage = false
		if _, err := f.service.RefreshUsage(ctx, "fixture", "a1"); err == nil {
			t.Fatal("refresh without usage must fail")
		}
	})

	t.Run("refresh usage response error", func(t *testing.T) {
		f := newFixture(t)
		f.runtime.usage = providerruntime.Usage{Error: "quota denied"}
		if _, err := f.service.RefreshUsage(ctx, "fixture", "a1"); err == nil {
			t.Fatal("usage response error must fail")
		}
	})

	t.Run("has provider", func(t *testing.T) {
		f := newFixture(t)
		if found, err := f.service.HasProvider(ctx, "fixture"); err != nil || found {
			t.Fatalf("unpersisted provider = %v, %v", found, err)
		}
		if _, err := f.service.List(ctx); err != nil {
			t.Fatal(err)
		}
		if found, err := f.service.HasProvider(ctx, "fixture"); err != nil || !found {
			t.Fatalf("persisted provider = %v, %v", found, err)
		}
		mustExec(t, f.store.DB(), `DROP TABLE providers`)
		if _, err := f.service.HasProvider(ctx, "fixture"); err == nil {
			t.Fatal("provider lookup error must surface")
		}
	})
}

func TestRunStateAndSummaryBranches(t *testing.T) {
	ctx := context.Background()

	t.Run("run disabled provider", func(t *testing.T) {
		f := newFixture(t)
		if _, err := f.service.List(ctx); err != nil {
			t.Fatal(err)
		}
		if err := f.service.SetEnabled(ctx, "fixture", false); err != nil {
			t.Fatal(err)
		}
		if err := f.service.Run(ctx, "fixture", "a1", "checkin"); err == nil {
			t.Fatal("disabled provider run must fail")
		}
	})

	t.Run("run missing runtime provider", func(t *testing.T) {
		f := newFixture(t)
		f.runtime.providers = []providerruntime.Provider{{ID: "other", Name: "Other"}}
		if err := f.service.Run(ctx, "fixture", "a1", "checkin"); err == nil {
			t.Fatal("unknown provider run must fail")
		}
	})

	t.Run("run operation error", func(t *testing.T) {
		f := newFixture(t)
		mustExec(t, f.store.DB(), `
CREATE TRIGGER fail_more_daily_update BEFORE UPDATE ON daily_ops
BEGIN SELECT RAISE(ABORT, 'daily update failed'); END`)
		if err := f.service.Run(ctx, "fixture", "a1", "checkin"); err == nil {
			t.Fatal("run operation error must surface")
		}
	})

	t.Run("run terminal state not executed", func(t *testing.T) {
		f := newFixture(t)
		if err := f.service.Run(ctx, "fixture", "a1", "checkin"); err != nil {
			t.Fatal(err)
		}
		if err := f.service.Run(ctx, "fixture", "a1", "checkin"); !errors.Is(err, ErrNotExecuted) {
			t.Fatalf("second run should be not executed: %v", err)
		}
	})

	t.Run("run one unsupported operation", func(t *testing.T) {
		f := newFixture(t)
		result := f.service.runOne(ctx, f.runtime, "fixture", "a1", "unknown")
		if result.Err == nil {
			t.Fatal("unsupported runOne operation must fail")
		}
	})

	t.Run("run all view action validation", func(t *testing.T) {
		f := newFixture(t)
		if _, err := f.service.RunAllView(ctx, View{}, "bad", nil); err == nil {
			t.Fatal("invalid RunAllView action must fail")
		}
		if _, err := f.service.RunAllView(ctx, View{}, "checkin", nil); err != nil {
			t.Fatal(err)
		}
	})
}

func TestUsageValuesLimitBranches(t *testing.T) {
	credits, balance := usageValues(providerruntime.Usage{
		Windows: []providerruntime.UsageWindow{
			{Limit: 100, Amount: 30},
			{Limit: 100, Amount: 130},
		},
	})
	if credits == nil || balance == nil || *credits != 70 || *balance != 70 {
		t.Fatalf("limit-derived quota = %v %v", credits, balance)
	}
}

func TestKeepaliveRuntimeAndProtocolBranches(t *testing.T) {
	ctx := context.Background()

	t.Run("runtime provider error", func(t *testing.T) {
		f := newFixture(t)
		result, err := f.service.keepalive(ctx, f.runtime, "missing", "a1")
		if err != nil || result.Kind != engine.RRetryable {
			t.Fatalf("keepalive runtime provider error = %+v %v", result, err)
		}
	})

	t.Run("anthropic protocol", func(t *testing.T) {
		f := newFixture(t)
		f.runtime.providers[0].NPM = "@ai-sdk/anthropic"
		f.runtime.providers[0].Models[0].NPM = "@ai-sdk/anthropic"
		if err := f.service.Run(ctx, "fixture", "a1", "keepalive"); err != nil {
			t.Fatal(err)
		}
		request := f.runtime.fetchRequest
		if !strings.HasSuffix(request.URL, "/messages") ||
			request.Headers["anthropic-version"] != "2023-06-01" ||
			!strings.Contains(request.Body, `"system"`) {
			t.Fatalf("anthropic request = %+v", request)
		}
	})
}

func TestKeepaliveModelSelectionBranches(t *testing.T) {
	provider := providerruntime.Provider{
		Models: []providerruntime.Model{
			{ID: "blocked", APIURL: "https://blocked.example/v1"},
			{ID: "allowed", NPM: "@ai-sdk/anthropic", APIURL: "https://allowed.example/v1/"},
		},
		Accounts: []providerruntime.Account{
			{Key: "other", Models: []string{"ignored"}},
			{Key: "a1", Models: []string{"allowed"}},
		},
	}
	model, url := keepaliveModel(provider, "a1", "")
	if model.ID != "allowed" || url != "https://allowed.example/v1/messages" {
		t.Fatalf("selected model = %+v %q", model, url)
	}

	provider = providerruntime.Provider{
		Models: []providerruntime.Model{{ID: "model"}},
	}
	if model, url := keepaliveModel(provider, "a1", ""); model.ID != "" || url != "" {
		t.Fatalf("model without base URL = %+v %q", model, url)
	}

	model, url = keepaliveModel(provider, "a1", "https://fallback.example/v1")
	if model.ID != "model" || url != "https://fallback.example/v1/chat/completions" {
		t.Fatalf("fallback model = %+v %q", model, url)
	}

	provider = providerruntime.Provider{
		Models: []providerruntime.Model{{ID: "blocked"}},
		Accounts: []providerruntime.Account{{
			Key: "a1", Models: []string{"different"},
		}},
	}
	if model, url := keepaliveModel(provider, "a1", "https://fallback.example/v1"); model.ID != "" || url != "" {
		t.Fatalf("restricted fallback model = %+v %q", model, url)
	}
}

func TestCompletedKeepaliveRecordsUsage(t *testing.T) {
	f := newFixture(t)
	result := f.service.runOne(context.Background(), f.runtime, "fixture", "a1", "keepalive")
	if result.Err != nil || result.State != string(engine.Completed) ||
		result.Balance == nil || *result.Balance != 12.5 {
		t.Fatalf("completed keepalive usage = %+v", result)
	}
}

func TestLoginPersistAuthErrors(t *testing.T) {
	ctx := context.Background()
	finish := newFixture(t)
	finish.runtime.finish = providerruntime.Saved{Provider: "fixture", Account: "a1"}
	finish.runtime.auth = []byte("not-json")
	if _, err := finish.service.FinishLogin(ctx, "session", "code"); err == nil {
		t.Fatal("FinishLogin persist auth error must surface")
	}

	apiKey := newFixture(t)
	apiKey.runtime.apiKey = providerruntime.Saved{Provider: "fixture", Account: "a1"}
	apiKey.runtime.auth = []byte("not-json")
	if _, err := apiKey.service.LoginAPIKey(ctx, "fixture", 1, nil, "key"); err == nil {
		t.Fatal("LoginAPIKey persist auth error must surface")
	}
}
