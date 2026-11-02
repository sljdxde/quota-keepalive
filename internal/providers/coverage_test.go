package providers

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"qka/internal/accounts"
	"qka/internal/capabilities"
	"qka/internal/engine"
	"qka/internal/providerruntime"
)

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func fixedNow() time.Time {
	return time.Date(2026, 10, 30, 9, 0, 0, 0, engine.Beijing)
}

func TestCoverageServiceDefaultsAndAuthErrors(t *testing.T) {
	svc := NewService(nil, nil, nil, nil, nil, nil)
	if svc.now().IsZero() {
		t.Fatal("nil clock should fall back to time.Now")
	}

	factoryErr := errors.New("factory failed")
	svc = NewService(nil, nil, nil, nil, func(context.Context) (Runtime, error) {
		return nil, factoryErr
	}, fixedNow)
	if _, err := svc.runtime(context.Background()); !errors.Is(err, factoryErr) {
		t.Fatalf("runtime error = %v", err)
	}

	f := newFixture(t)
	f.runtime.authErr = os.ErrNotExist
	if err := f.service.persistAuth(context.Background(), f.runtime); err != nil {
		t.Fatalf("missing auth file should be ignored: %v", err)
	}

	f.runtime.authErr = nil
	f.runtime.auth = []byte(`{}`)
	f.service.credentials = capabilities.NewCredentialService(
		&memorySecretStore{}, &memorySecretStore{}, capabilities.Grant{}, fixedNow,
	)
	if err := f.service.persistAuth(context.Background(), f.runtime); err == nil {
		t.Fatal("managed credential deletion failure should surface")
	}
}

func TestCoverageListWriteAndViewFailures(t *testing.T) {
	t.Run("provider write", func(t *testing.T) {
		f := newFixture(t)
		mustExec(t, f.store.DB(), `
CREATE TRIGGER fail_provider_insert BEFORE INSERT ON providers
BEGIN SELECT RAISE(ABORT, 'provider write failed'); END`)
		if _, err := f.service.List(context.Background()); err == nil {
			t.Fatal("provider write failure should surface")
		}
	})

	t.Run("account write", func(t *testing.T) {
		f := newFixture(t)
		mustExec(t, f.store.DB(), `
CREATE TRIGGER fail_account_insert BEFORE INSERT ON accounts
BEGIN SELECT RAISE(ABORT, 'account write failed'); END`)
		if _, err := f.service.List(context.Background()); err == nil {
			t.Fatal("account write failure should surface")
		}
	})

	t.Run("signed out with no accounts", func(t *testing.T) {
		f := newFixture(t)
		f.runtime.providers[0].SignedIn = false
		f.runtime.providers[0].Accounts = nil
		view, err := f.service.List(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(view.Providers) != 1 || len(view.Providers[0].Accounts) != 0 {
			t.Fatalf("unexpected view: %+v", view)
		}
	})

	t.Run("view read", func(t *testing.T) {
		queryErr := errors.New("provider prefs failed")
		db := openCoverageDB(t, &coverageDBConfig{
			exec: func(string) (driver.Result, error) {
				return coverageResult{affected: 1}, nil
			},
			query: func(string) (driver.Rows, error) {
				return nil, queryErr
			},
		})
		host := &fakeRuntime{providers: []providerruntime.Provider{{ID: "empty", Name: "Empty"}}}
		svc := NewService(db, accounts.NewService(db), nil, nil,
			func(context.Context) (Runtime, error) { return host, nil }, fixedNow)
		if _, err := svc.List(context.Background()); !errors.Is(err, queryErr) {
			t.Fatalf("view failure = %v", err)
		}
	})
}

func TestCoverageViewSQLiteErrors(t *testing.T) {
	t.Run("sources", func(t *testing.T) {
		f := newFixture(t)
		if _, err := f.service.List(context.Background()); err != nil {
			t.Fatal(err)
		}
		mustExec(t, f.store.DB(), `DROP TABLE sources`)
		if _, err := f.service.view(context.Background(), []providerruntime.Provider{providerFixture()}); err == nil {
			t.Fatal("source read failure should surface")
		}
	})

	t.Run("task preferences", func(t *testing.T) {
		f := newFixture(t)
		if _, err := f.service.List(context.Background()); err != nil {
			t.Fatal(err)
		}
		mustExec(t, f.store.DB(), `DROP TABLE account_task_preferences`)
		if _, err := f.service.view(context.Background(), []providerruntime.Provider{providerFixture()}); err == nil {
			t.Fatal("task preference read failure should surface")
		}
	})

	t.Run("operation rows", func(t *testing.T) {
		f := newFixture(t)
		if _, err := f.service.List(context.Background()); err != nil {
			t.Fatal(err)
		}
		mustExec(t, f.store.DB(), `DROP TABLE daily_ops`)
		if _, err := f.service.view(context.Background(), []providerruntime.Provider{providerFixture()}); err == nil {
			t.Fatal("daily operation read failure should surface")
		}
	})
}

func TestCoverageViewSortingAndKnownQuota(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	view, err := f.service.view(ctx, []providerruntime.Provider{
		{ID: "signed-out", Name: "A"},
		{ID: "signed-in", Name: "Z", SignedIn: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if view.Providers[0].ID != "signed-in" {
		t.Fatalf("signed-in provider should sort first: %+v", view.Providers)
	}
	view, err = f.service.view(ctx, []providerruntime.Provider{
		{ID: "provider-b", Name: "B"},
		{ID: "provider-a", Name: "A"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if view.Providers[0].ID != "provider-a" {
		t.Fatalf("same sign-in state should sort by name: %+v", view.Providers)
	}

	if _, err := f.service.RefreshUsage(ctx, "fixture", "a1"); err != nil {
		t.Fatal(err)
	}
	view, err = f.service.view(ctx, []providerruntime.Provider{providerFixture()})
	if err != nil {
		t.Fatal(err)
	}
	checkin := view.Providers[0].Accounts[0].Operations[0]
	if checkin.Credits == nil || checkin.Balance == nil ||
		*checkin.Credits != 12.5 || *checkin.Balance != 12.5 {
		t.Fatalf("known quota fields missing: %+v", checkin)
	}
}

func TestCoverageSetEnabledErrors(t *testing.T) {
	t.Run("exec", func(t *testing.T) {
		f := newFixture(t)
		if _, err := f.service.List(context.Background()); err != nil {
			t.Fatal(err)
		}
		mustExec(t, f.store.DB(), `
CREATE TRIGGER fail_provider_update BEFORE UPDATE ON providers
BEGIN SELECT RAISE(ABORT, 'provider update failed'); END`)
		if err := f.service.SetEnabled(context.Background(), "fixture", false); err == nil {
			t.Fatal("provider update failure should surface")
		}
	})

	t.Run("rows affected", func(t *testing.T) {
		rowsErr := errors.New("rows affected failed")
		db := openCoverageDB(t, &coverageDBConfig{
			exec: func(string) (driver.Result, error) {
				return coverageResult{affected: 1, affectedErr: rowsErr}, nil
			},
		})
		svc := NewService(db, nil, nil, nil, nil, fixedNow)
		if err := svc.SetEnabled(context.Background(), "fixture", false); !errors.Is(err, rowsErr) {
			t.Fatalf("rows affected error = %v", err)
		}
	})
}

func TestCoverageLoginAndRuntimeErrors(t *testing.T) {
	f := newFixture(t)
	f.service.host = nil
	f.service.factory = nil
	ctx := context.Background()
	if _, err := f.service.BeginLogin(ctx, "fixture", 0, nil); err == nil {
		t.Fatal("begin login runtime error should surface")
	}
	if _, err := f.service.FinishLogin(ctx, "session", "code"); err == nil {
		t.Fatal("finish login runtime error should surface")
	}
	if _, err := f.service.LoginAPIKey(ctx, "fixture", 1, nil, "key"); err == nil {
		t.Fatal("api key login runtime error should surface")
	}
	if _, err := f.service.RefreshUsage(ctx, "fixture", "a1"); err == nil {
		t.Fatal("usage runtime error should surface")
	}
	if err := f.service.Run(ctx, "fixture", "a1", "checkin"); err == nil {
		t.Fatal("run runtime error should surface")
	}

	f.runtime.providers = []providerruntime.Provider{
		{ID: "other", Name: "Other"},
		providerFixture(),
	}
	f.runtime.authorize = providerruntime.Authorization{Session: "s1"}
	f.service.factory = func(context.Context) (Runtime, error) { return f.runtime, nil }
	if _, err := f.service.BeginLogin(ctx, "fixture", 0, nil); err != nil {
		t.Fatalf("non-matching provider should be skipped: %v", err)
	}
}

func TestCoverageLoginPersistErrors(t *testing.T) {
	ctx := context.Background()

	finish := newFixture(t)
	finish.runtime.auth = []byte(`not-json`)
	if _, err := finish.service.FinishLogin(ctx, "session", "code"); err == nil {
		t.Fatal("finish login persist error should surface")
	}

	apiKey := newFixture(t)
	apiKey.runtime.auth = []byte(`not-json`)
	if _, err := apiKey.service.LoginAPIKey(ctx, "fixture", 1, nil, "key"); err == nil {
		t.Fatal("api key persist error should surface")
	}

	del := newFixture(t)
	del.runtime.auth = []byte(`not-json`)
	if err := del.service.DeleteAccount(ctx, "fixture", "a1"); err == nil {
		t.Fatal("delete account persist error should surface")
	}
}

func TestCoverageRefreshUsageWriteErrors(t *testing.T) {
	t.Run("quota insert", func(t *testing.T) {
		f := newFixture(t)
		mustExec(t, f.store.DB(), `
CREATE TRIGGER fail_quota_insert BEFORE INSERT ON quota_readings
BEGIN SELECT RAISE(ABORT, 'quota insert failed'); END`)
		if _, err := f.service.RefreshUsage(context.Background(), "fixture", "a1"); err == nil {
			t.Fatal("quota insert failure should surface")
		}
	})

	t.Run("daily operation update", func(t *testing.T) {
		f := newFixture(t)
		mustExec(t, f.store.DB(), `
CREATE TRIGGER fail_daily_update BEFORE UPDATE ON daily_ops
BEGIN SELECT RAISE(ABORT, 'daily update failed'); END`)
		if _, err := f.service.RefreshUsage(context.Background(), "fixture", "a1"); err == nil {
			t.Fatal("daily operation update failure should surface")
		}
	})
}

func TestCoverageRunRecordFailure(t *testing.T) {
	f := newFixture(t)
	mustExec(t, f.store.DB(), `
CREATE TRIGGER fail_run_insert BEFORE INSERT ON runs
BEGIN SELECT RAISE(ABORT, 'run insert failed'); END`)
	if err := f.service.Run(context.Background(), "fixture", "a1", "checkin"); err == nil {
		t.Fatal("run record failure should surface")
	}
}

type providersHookRuntime struct {
	*fakeRuntime
	hook func()
}

func (r *providersHookRuntime) Providers(ctx context.Context) ([]providerruntime.Provider, error) {
	if r.hook != nil {
		r.hook()
	}
	return r.fakeRuntime.Providers(ctx)
}

func TestCoverageRunAllFailurePaths(t *testing.T) {
	t.Run("list", func(t *testing.T) {
		f := newFixture(t)
		mustExec(t, f.store.DB(), `
CREATE TRIGGER fail_provider_insert BEFORE INSERT ON providers
BEGIN SELECT RAISE(ABORT, 'provider write failed'); END`)
		if _, err := f.service.RunAll(context.Background(), "checkin", nil); err == nil {
			t.Fatal("run-all list failure should surface")
		}
	})

	t.Run("runtime after list", func(t *testing.T) {
		f := newFixture(t)
		if _, err := listFixtureEnabled(t, f); err != nil {
			t.Fatal(err)
		}
		hook := &providersHookRuntime{fakeRuntime: f.runtime}
		var calls int
		f.service.factory = func(context.Context) (Runtime, error) {
			calls++
			if calls == 1 {
				return hook, nil
			}
			return nil, errors.New("second runtime failed")
		}
		hook.hook = func() {
			if calls == 1 {
				_ = f.service.Close()
			}
		}
		if _, err := f.service.RunAll(context.Background(), "checkin", nil); err == nil {
			t.Fatal("second runtime failure should surface")
		}
	})

	t.Run("operation error", func(t *testing.T) {
		f := newFixture(t)
		if _, err := listFixtureEnabled(t, f); err != nil {
			t.Fatal(err)
		}
		hook := &providersHookRuntime{fakeRuntime: f.runtime}
		hook.hook = func() {
			_, _ = f.store.DB().Exec(`
CREATE TRIGGER fail_daily_update BEFORE UPDATE ON daily_ops
BEGIN SELECT RAISE(ABORT, 'daily update failed'); END`)
		}
		f.service.factory = func(context.Context) (Runtime, error) { return hook, nil }
		summary, err := f.service.RunAll(context.Background(), "checkin", nil)
		if err == nil || summary.NeedsAttention != 2 || summary.Attempted != 0 {
			t.Fatalf("operation error summary=%+v err=%v", summary, err)
		}
	})

	t.Run("record error", func(t *testing.T) {
		f := newFixture(t)
		if _, err := listFixtureEnabled(t, f); err != nil {
			t.Fatal(err)
		}
		hook := &providersHookRuntime{fakeRuntime: f.runtime}
		hook.hook = func() {
			_, _ = f.store.DB().Exec(`
CREATE TRIGGER fail_run_insert BEFORE INSERT ON runs
BEGIN SELECT RAISE(ABORT, 'run insert failed'); END`)
		}
		f.service.factory = func(context.Context) (Runtime, error) { return hook, nil }
		if _, err := f.service.RunAll(context.Background(), "checkin", nil); err == nil {
			t.Fatal("run-all record failure should surface")
		}
	})
}

func TestCoverageRunAllSkipsTerminalState(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := listFixtureEnabled(t, f); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.RunAll(ctx, "checkin", nil); err != nil {
		t.Fatal(err)
	}
	summary, err := f.service.RunAll(ctx, "checkin", nil)
	if err != nil || summary.Skipped != 2 || summary.Attempted != 0 {
		t.Fatalf("terminal state summary=%+v err=%v", summary, err)
	}
}

func TestCoverageRunOneErrors(t *testing.T) {
	t.Run("checkin engine", func(t *testing.T) {
		f := newFixture(t)
		mustExec(t, f.store.DB(), `
CREATE TRIGGER fail_daily_update BEFORE UPDATE ON daily_ops
BEGIN SELECT RAISE(ABORT, 'daily update failed'); END`)
		result := f.service.runOne(context.Background(), f.runtime, "fixture", "a1", "checkin")
		if result.Err == nil {
			t.Fatal("checkin engine error should surface")
		}
	})

	t.Run("keepalive engine", func(t *testing.T) {
		f := newFixture(t)
		mustExec(t, f.store.DB(), `
CREATE TRIGGER fail_daily_update BEFORE UPDATE ON daily_ops
BEGIN SELECT RAISE(ABORT, 'daily update failed'); END`)
		result := f.service.runOne(context.Background(), f.runtime, "fixture", "a1", "keepalive")
		if result.Err == nil {
			t.Fatal("keepalive engine error should surface")
		}
	})

	t.Run("refresh", func(t *testing.T) {
		f := newFixture(t)
		f.runtime.usageErr = errors.New("usage failed")
		result := f.service.runOne(context.Background(), f.runtime, "fixture", "a1", "refresh")
		if !errors.Is(result.Err, f.runtime.usageErr) {
			t.Fatalf("refresh error = %v", result.Err)
		}
	})
}

func TestCoverageRecordRunErrors(t *testing.T) {
	ctx := context.Background()
	result := []runResult{{Provider: "p", Account: "a", Action: "checkin", State: "completed", Executed: true}}

	if err := (&Service{}).recordRun(ctx, "nil-db", "", "checkin", result); err != nil {
		t.Fatalf("nil db should be ignored: %v", err)
	}

	beginErr := errors.New("begin failed")
	db := openCoverageDB(t, &coverageDBConfig{beginErr: beginErr})
	svc := NewService(db, nil, nil, nil, nil, fixedNow)
	if err := svc.recordRun(ctx, "begin", "", "checkin", result); !errors.Is(err, beginErr) {
		t.Fatalf("begin error = %v", err)
	}

	t.Run("run insert", func(t *testing.T) {
		f := newFixture(t)
		mustExec(t, f.store.DB(), `
CREATE TRIGGER fail_run_insert BEFORE INSERT ON runs
BEGIN SELECT RAISE(ABORT, 'run insert failed'); END`)
		if err := f.service.recordRun(ctx, "run", "", "checkin", result); err == nil {
			t.Fatal("run insert failure should surface")
		}
	})

	t.Run("item insert", func(t *testing.T) {
		f := newFixture(t)
		mustExec(t, f.store.DB(), `
CREATE TRIGGER fail_item_insert BEFORE INSERT ON run_items
BEGIN SELECT RAISE(ABORT, 'item insert failed'); END`)
		if err := f.service.recordRun(ctx, "item", "", "checkin", result); err == nil {
			t.Fatal("run item insert failure should surface")
		}
	})
}

func TestCoverageHistorySQLiteErrors(t *testing.T) {
	t.Run("run query", func(t *testing.T) {
		f := newFixture(t)
		mustExec(t, f.store.DB(), `DROP TABLE run_items`)
		if _, _, err := f.service.History(context.Background(), "", "", 10); err == nil {
			t.Fatal("run item query failure should surface")
		}
	})

	t.Run("quota query", func(t *testing.T) {
		f := newFixture(t)
		mustExec(t, f.store.DB(), `DROP TABLE quota_readings`)
		if _, _, err := f.service.History(context.Background(), "", "", 10); err == nil {
			t.Fatal("quota query failure should surface")
		}
	})

	t.Run("invalid windows json", func(t *testing.T) {
		f := newFixture(t)
		mustExec(t, f.store.DB(), `
INSERT INTO quota_readings
  (provider, account_id, observed_at, plan, until, balance, windows_json)
VALUES ('p', 'a', '2026-10-30T00:00:00Z', '', '', '', 'not-json')`)
		_, readings, err := f.service.History(context.Background(), "", "", 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(readings) != 1 || readings[0].Windows == nil {
			t.Fatalf("invalid windows should produce an empty slice: %+v", readings)
		}
	})
}

func TestCoverageRowsScanAndIterationErrors(t *testing.T) {
	ctx := context.Background()
	operationColumns := []string{
		"operation", "state", "credits", "balance",
		"credits_known", "balance_known", "message",
	}
	runColumns := []string{
		"id", "provider", "account_id", "action", "state",
		"message", "credits", "balance", "model", "created_at",
	}
	quotaColumns := []string{"observed_at", "plan", "until", "balance", "windows_json"}

	t.Run("operation scan", func(t *testing.T) {
		db := openCoverageDB(t, &coverageDBConfig{query: func(string) (driver.Rows, error) {
			return &coverageRows{
				columns: operationColumns,
				values:  [][]driver.Value{{nil, "pending", 0.0, 0.0, int64(0), int64(0), ""}},
			}, nil
		}})
		if _, err := (&Service{db: db}).operationViews(ctx, "p", "a", "2026-10-30", nil); err == nil {
			t.Fatal("operation scan failure should surface")
		}
	})

	t.Run("operation iteration", func(t *testing.T) {
		nextErr := errors.New("operation next failed")
		db := openCoverageDB(t, &coverageDBConfig{query: func(string) (driver.Rows, error) {
			return &coverageRows{columns: operationColumns, nextErr: nextErr}, nil
		}})
		if _, err := (&Service{db: db}).operationViews(ctx, "p", "a", "2026-10-30", nil); !errors.Is(err, nextErr) {
			t.Fatalf("operation rows error = %v", err)
		}
	})

	t.Run("provider preferences scan", func(t *testing.T) {
		db := openCoverageDB(t, &coverageDBConfig{query: func(string) (driver.Rows, error) {
			return &coverageRows{
				columns: []string{"id", "enabled"},
				values:  [][]driver.Value{{"p", nil}},
			}, nil
		}})
		if _, err := (&Service{db: db}).providerPrefs(ctx); err == nil {
			t.Fatal("provider preference scan failure should surface")
		}
	})

	t.Run("run item scan", func(t *testing.T) {
		db := openCoverageDB(t, &coverageDBConfig{query: func(string) (driver.Rows, error) {
			return &coverageRows{
				columns: runColumns,
				values:  [][]driver.Value{{nil, "p", "a", "checkin", "completed", "", nil, nil, "", ""}},
			}, nil
		}})
		if _, _, err := (&Service{db: db}).History(ctx, "", "", 10); err == nil {
			t.Fatal("run item scan failure should surface")
		}
	})

	t.Run("run item iteration", func(t *testing.T) {
		nextErr := errors.New("run item next failed")
		db := openCoverageDB(t, &coverageDBConfig{query: func(string) (driver.Rows, error) {
			return &coverageRows{columns: runColumns, nextErr: nextErr}, nil
		}})
		if _, _, err := (&Service{db: db}).History(ctx, "", "", 10); !errors.Is(err, nextErr) {
			t.Fatalf("run item rows error = %v", err)
		}
	})

	t.Run("quota query", func(t *testing.T) {
		queryErr := errors.New("quota query failed")
		db := openCoverageDB(t, &coverageDBConfig{query: func(query string) (driver.Rows, error) {
			if strings.Contains(query, "FROM run_items") {
				return &coverageRows{columns: runColumns}, nil
			}
			return nil, queryErr
		}})
		if _, _, err := (&Service{db: db}).History(ctx, "", "", 10); !errors.Is(err, queryErr) {
			t.Fatalf("quota query error = %v", err)
		}
	})

	t.Run("quota scan", func(t *testing.T) {
		db := openCoverageDB(t, &coverageDBConfig{query: func(query string) (driver.Rows, error) {
			if strings.Contains(query, "FROM run_items") {
				return &coverageRows{columns: runColumns}, nil
			}
			return &coverageRows{
				columns: quotaColumns,
				values:  [][]driver.Value{{"", "", "", "", nil}},
			}, nil
		}})
		if _, _, err := (&Service{db: db}).History(ctx, "", "", 10); err == nil {
			t.Fatal("quota scan failure should surface")
		}
	})

	t.Run("quota iteration", func(t *testing.T) {
		nextErr := errors.New("quota next failed")
		db := openCoverageDB(t, &coverageDBConfig{query: func(query string) (driver.Rows, error) {
			if strings.Contains(query, "FROM run_items") {
				return &coverageRows{columns: runColumns}, nil
			}
			return &coverageRows{columns: quotaColumns, nextErr: nextErr}, nil
		}})
		if _, _, err := (&Service{db: db}).History(ctx, "", "", 10); !errors.Is(err, nextErr) {
			t.Fatalf("quota rows error = %v", err)
		}
	})
}

func TestCoverageKeepaliveSkipsOtherProvider(t *testing.T) {
	f := newFixture(t)
	f.runtime.providers = []providerruntime.Provider{
		{ID: "other", Name: "Other"},
		providerFixture(),
	}
	if err := f.service.Run(context.Background(), "fixture", "a1", "keepalive"); err != nil {
		t.Fatal(err)
	}
}

func TestCoverageHelperBranches(t *testing.T) {
	if !hasReply([]byte(`{"content":[{"type":"text","text":"ok"}]}`)) {
		t.Fatal("content array should count as a reply")
	}
	if usageCredits(providerruntime.Usage{}) != nil {
		t.Fatal("empty usage should have no credits")
	}
	value := 3.0
	credits, balance := usageValues(providerruntime.Usage{Windows: []providerruntime.UsageWindow{
		{},
		{Balance: &value},
	}})
	if credits == nil || balance == nil || *credits != value || *balance != value {
		t.Fatalf("usage values with unknown first window: %v %v", credits, balance)
	}
}
