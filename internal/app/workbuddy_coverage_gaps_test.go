package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"qka/internal/accounts"
	"qka/internal/capabilities"
	"qka/internal/engine"
	"qka/internal/store"
	"qka/internal/workbuddy"
)

func TestWorkBuddyCheckinLookupAndSessionFailures(t *testing.T) {
	now := time.Date(2026, 10, 29, 14, 0, 0, 0, engine.Beijing)
	target := &workBuddyCheckinTarget{active: true}
	srv := target.serve(t)
	defer srv.Close()
	a, acct, accountID := newWorkBuddyCheckinApp(t, srv.URL, now)
	ctx := context.Background()

	if err := acct.SetEnabled(ctx, "workbuddy-cn", accountID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := a.runWorkBuddyCheckin(ctx, accountID); err == nil ||
		!strings.Contains(err.Error(), "账号已停用") {
		t.Fatalf("disabled account error = %v", err)
	}

	oldGet := getWorkBuddyAccount
	oldPreferred := preferredWorkBuddySource
	oldCurrent := workBuddyCurrentState
	t.Cleanup(func() {
		getWorkBuddyAccount = oldGet
		preferredWorkBuddySource = oldPreferred
		workBuddyCurrentState = oldCurrent
	})

	if err := acct.SetEnabled(ctx, "workbuddy-cn", accountID, true); err != nil {
		t.Fatal(err)
	}
	preferredWorkBuddySource = func(
		*accounts.Service, context.Context, string, string,
	) (accounts.Source, error) {
		return accounts.Source{}, errors.New("preferred source failed")
	}
	if _, err := a.runWorkBuddyCheckin(ctx, accountID); err == nil {
		t.Fatal("preferred source failure should be returned")
	}
	preferredWorkBuddySource = oldPreferred

	workBuddyCurrentState = func(
		*engine.Engine, context.Context, engine.Key,
	) (engine.State, error) {
		return "", errors.New("state lookup failed")
	}
	if _, err := a.runWorkBuddyCheckin(ctx, accountID); err == nil {
		t.Fatal("current state failure should be returned")
	}
	workBuddyCurrentState = oldCurrent

	if _, err := (&App{}).newWorkBuddyManagedSession(ctx, accountID, "wbm_one"); err == nil {
		t.Fatal("session without services should fail")
	}
	getWorkBuddyAccount = func(
		*accounts.Service, context.Context, string, string,
	) (accounts.Account, error) {
		return accounts.Account{}, errors.New("account lookup failed")
	}
	if _, err := a.newWorkBuddyManagedSession(ctx, accountID, "wbm_one"); err == nil {
		t.Fatal("managed session account lookup failure should be returned")
	}
	getWorkBuddyAccount = oldGet

	result := workBuddyCheckinExternalResult(workbuddy.CheckinResult{
		Outcome: workbuddy.CheckinOutcome("unknown"), Message: "fallback",
	})
	if result.Kind != engine.RRetryable || result.Message != "fallback" {
		t.Fatalf("unknown outcome mapping = %+v", result)
	}

	a.credentials = nil
	got, err := a.runWorkBuddyCheckin(ctx, accountID)
	if err != nil {
		t.Fatal(err)
	}
	if obs := got.(engine.Observation); obs.State != engine.FailedRetryable {
		t.Fatalf("missing session should become retryable: %+v", obs)
	}
}

func TestWorkBuddyCheckinStateWriteFailures(t *testing.T) {
	now := time.Date(2026, 10, 29, 14, 0, 0, 0, engine.Beijing)
	tests := []struct {
		name    string
		target  *workBuddyCheckinTarget
		trigger string
	}{
		{
			name:   "engine run",
			target: &workBuddyCheckinTarget{active: true},
			trigger: `CREATE TRIGGER coverage_engine_run
BEFORE INSERT ON daily_ops
BEGIN
  SELECT RAISE(ABORT, 'daily op insert blocked');
END;`,
		},
		{
			name:   "provisional reconcile",
			target: &workBuddyCheckinTarget{active: true},
			trigger: `CREATE TRIGGER coverage_provisional_reconcile
BEFORE UPDATE ON daily_ops
WHEN NEW.message = '已领取，待确认'
BEGIN
  SELECT RAISE(ABORT, 'provisional reconcile blocked');
END;`,
		},
		{
			name:   "confirmed reconcile",
			target: &workBuddyCheckinTarget{active: true, claimCredit: 100},
			trigger: `CREATE TRIGGER coverage_confirmed_reconcile
BEFORE UPDATE ON daily_ops
WHEN NEW.message = '已到账'
BEGIN
  SELECT RAISE(ABORT, 'confirmed reconcile blocked');
END;`,
		},
		{
			name:   "usage",
			target: &workBuddyCheckinTarget{active: true, claimCredit: 100},
			trigger: `CREATE TRIGGER coverage_checkin_usage
BEFORE UPDATE ON daily_ops
WHEN NEW.credits_known = 1
BEGIN
  SELECT RAISE(ABORT, 'usage write blocked');
END;`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			target := tc.target
			srv := target.serve(t)
			defer srv.Close()
			a, _, accountID := newWorkBuddyCheckinApp(t, srv.URL, now)
			execCoverageSQL(t, a, tc.trigger)
			if _, err := a.runWorkBuddyCheckin(context.Background(), accountID); err == nil {
				t.Fatal("database write failure should be returned")
			}
		})
	}
}

func TestWorkBuddyReviewFailureAndRaceBranches(t *testing.T) {
	now := time.Date(2026, 10, 29, 14, 0, 0, 0, engine.Beijing)

	t.Run("session", func(t *testing.T) {
		target := &workBuddyCheckinTarget{active: true}
		srv := target.serve(t)
		defer srv.Close()
		a, acct, accountID := newWorkBuddyCheckinApp(t, srv.URL, now)
		setWorkBuddyDailyState(t, a, accountID, engine.ManualRequired)
		preferred := workBuddyPreferred(t, acct, accountID)
		a.credentials = nil
		if _, err := a.reviewWorkBuddyFallback(
			context.Background(), accountID, preferred, workBuddyCheckinKey(accountID),
		); err == nil {
			t.Fatal("review without session services should fail")
		}
	})

	t.Run("canceled", func(t *testing.T) {
		target := &workBuddyCheckinTarget{active: true}
		srv := target.serve(t)
		defer srv.Close()
		a, acct, accountID := newWorkBuddyCheckinApp(t, srv.URL, now)
		setWorkBuddyDailyState(t, a, accountID, engine.ManualRequired)
		preferred := workBuddyPreferred(t, acct, accountID)
		account, err := acct.Get(context.Background(), "workbuddy-cn", accountID)
		if err != nil {
			t.Fatal(err)
		}
		oldGet := getWorkBuddyAccount
		t.Cleanup(func() { getWorkBuddyAccount = oldGet })
		getWorkBuddyAccount = func(
			*accounts.Service, context.Context, string, string,
		) (accounts.Account, error) {
			return account, nil
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := a.reviewWorkBuddyFallback(
			ctx, accountID, preferred, workBuddyCheckinKey(accountID),
		); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled review error = %v", err)
		}
	})

	t.Run("temporary failure", func(t *testing.T) {
		target := &workBuddyCheckinTarget{active: true, failUID: "app-uid"}
		srv := target.serve(t)
		defer srv.Close()
		a, acct, accountID := newWorkBuddyCheckinApp(t, srv.URL, now)
		setWorkBuddyDailyState(t, a, accountID, engine.ManualRequired)
		preferred := workBuddyPreferred(t, acct, accountID)
		got, err := a.reviewWorkBuddyFallback(
			context.Background(), accountID, preferred, workBuddyCheckinKey(accountID),
		)
		if err != nil {
			t.Fatal(err)
		}
		obs := got.(engine.Observation)
		if obs.State != engine.ManualRequired ||
			!strings.Contains(obs.Message, "复查暂不可用") {
			t.Fatalf("temporary review result = %+v", obs)
		}
	})

	t.Run("reconcile error", func(t *testing.T) {
		target := &workBuddyCheckinTarget{active: true, checked: true}
		srv := target.serve(t)
		defer srv.Close()
		a, acct, accountID := newWorkBuddyCheckinApp(t, srv.URL, now)
		setWorkBuddyDailyState(t, a, accountID, engine.ManualRequired)
		execCoverageSQL(t, a, `CREATE TRIGGER coverage_review_reconcile
BEFORE UPDATE ON daily_ops
WHEN NEW.state = 'already'
BEGIN
  SELECT RAISE(ABORT, 'review reconcile blocked');
END;`)
		preferred := workBuddyPreferred(t, acct, accountID)
		if _, err := a.reviewWorkBuddyFallback(
			context.Background(), accountID, preferred, workBuddyCheckinKey(accountID),
		); err == nil {
			t.Fatal("review reconcile failure should be returned")
		}
	})

	t.Run("state changed", func(t *testing.T) {
		target := &workBuddyCheckinTarget{active: true, checked: true}
		srv := target.serve(t)
		defer srv.Close()
		a, acct, accountID := newWorkBuddyCheckinApp(t, srv.URL, now)
		setWorkBuddyDailyState(t, a, accountID, engine.FailedRetryable)
		preferred := workBuddyPreferred(t, acct, accountID)
		got, err := a.reviewWorkBuddyFallback(
			context.Background(), accountID, preferred, workBuddyCheckinKey(accountID),
		)
		if err != nil {
			t.Fatal(err)
		}
		obs := got.(engine.Observation)
		if obs.State != engine.FailedRetryable ||
			!strings.Contains(obs.Message, "复查期间状态已变化") {
			t.Fatalf("state race result = %+v", obs)
		}
	})

	t.Run("state reread error", func(t *testing.T) {
		target := &workBuddyCheckinTarget{active: true, checked: true}
		srv := target.serve(t)
		defer srv.Close()
		a, acct, accountID := newWorkBuddyCheckinApp(t, srv.URL, now)
		setWorkBuddyDailyState(t, a, accountID, engine.FailedRetryable)
		preferred := workBuddyPreferred(t, acct, accountID)
		oldCurrent := workBuddyCurrentState
		t.Cleanup(func() { workBuddyCurrentState = oldCurrent })
		workBuddyCurrentState = func(
			*engine.Engine, context.Context, engine.Key,
		) (engine.State, error) {
			return "", errors.New("state reread failed")
		}
		if _, err := a.reviewWorkBuddyFallback(
			context.Background(), accountID, preferred, workBuddyCheckinKey(accountID),
		); err == nil {
			t.Fatal("state reread failure should be returned")
		}
	})
}

func TestWorkBuddyReviewUsageFailures(t *testing.T) {
	now := time.Date(2026, 10, 29, 14, 0, 0, 0, engine.Beijing)
	tests := []struct {
		name   string
		target *workBuddyCheckinTarget
	}{
		{
			name: "already checked",
			target: &workBuddyCheckinTarget{
				active: true, checked: true, todayCredit: 50,
			},
		},
		{
			name: "unauthorized status",
			target: &workBuddyCheckinTarget{
				active: true, statusUnauthorized: true,
				balancePackages: []map[string]any{{"CycleRemainCapacity": "80"}},
			},
		},
		{
			name: "inactive",
			target: &workBuddyCheckinTarget{
				active:          false,
				balancePackages: []map[string]any{{"CycleRemainCapacity": "80"}},
			},
		},
		{
			name: "not checked",
			target: &workBuddyCheckinTarget{
				active:          true,
				balancePackages: []map[string]any{{"CycleRemainCapacity": "80"}},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			target := tc.target
			srv := target.serve(t)
			defer srv.Close()
			a, _, accountID := newWorkBuddyCheckinApp(t, srv.URL, now)
			setWorkBuddyDailyState(t, a, accountID, engine.ManualRequired)
			execCoverageSQL(t, a, `CREATE TRIGGER coverage_review_usage
BEFORE UPDATE ON daily_ops
WHEN NEW.credits_known = 1 OR NEW.balance_known = 1
BEGIN
  SELECT RAISE(ABORT, 'review usage write blocked');
END;`)
			if _, err := a.runWorkBuddyCheckin(context.Background(), accountID); err == nil {
				t.Fatal("review usage write failure should be returned")
			}
		})
	}
}

func TestWorkBuddyRefreshFailures(t *testing.T) {
	now := time.Date(2026, 10, 29, 14, 0, 0, 0, engine.Beijing)
	target := &workBuddyCheckinTarget{active: true}
	srv := target.serve(t)
	defer srv.Close()
	a, _, accountID := newWorkBuddyCheckinApp(t, srv.URL, now)
	ctx := context.Background()

	oldGet := getWorkBuddyAccount
	oldPreferred := preferredWorkBuddySource
	t.Cleanup(func() {
		getWorkBuddyAccount = oldGet
		preferredWorkBuddySource = oldPreferred
	})
	getWorkBuddyAccount = func(
		*accounts.Service, context.Context, string, string,
	) (accounts.Account, error) {
		return accounts.Account{}, errors.New("refresh account lookup failed")
	}
	if err := a.refreshWorkBuddyUsage(ctx, accountID); err == nil {
		t.Fatal("refresh account lookup failure should be returned")
	}
	getWorkBuddyAccount = oldGet

	preferredWorkBuddySource = func(
		*accounts.Service, context.Context, string, string,
	) (accounts.Source, error) {
		return accounts.Source{}, errors.New("refresh preferred source failed")
	}
	if err := a.refreshWorkBuddyUsage(ctx, accountID); err == nil {
		t.Fatal("refresh preferred source failure should be returned")
	}
	preferredWorkBuddySource = oldPreferred

	a.credentials = nil
	if err := a.refreshWorkBuddyUsage(ctx, accountID); err == nil {
		t.Fatal("refresh without credentials should fail")
	}
	a.credentials = capabilities.NewCredentialService(
		failingAppSecretStore{},
		newAppSecretStore(),
		capabilities.Grant{
			"credentials.managed": true,
			"credentials.resolve": true,
		},
		func() time.Time { return now },
	)

	target.mu.Lock()
	target.failUID = "app-uid"
	target.mu.Unlock()
	if err := a.refreshWorkBuddyUsage(ctx, accountID); err == nil {
		t.Fatal("refresh review failure should be returned")
	}

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	imported := accounts.NewService(st.DB())
	if err := imported.Discover(ctx, "workbuddy-cn", []accounts.DiscoveredAccount{{
		ID: "imported", DisplayName: "Imported", MaskedIdentity: "****",
		SourceID: "source", SourceKind: accounts.KindImported,
	}}); err != nil {
		t.Fatal(err)
	}
	importedApp := &App{
		cfg: Config{BaseURLs: map[string]string{"workbuddy-cn": srv.URL}},
		now: func() time.Time { return now }, store: st, accounts: imported,
		engine: engine.New(st.DB(), func() time.Time { return now }, "checkin-test"),
	}
	if err := importedApp.refreshWorkBuddyUsage(ctx, "imported"); err == nil ||
		!strings.Contains(err.Error(), "受管来源") {
		t.Fatalf("imported refresh error = %v", err)
	}
}

func TestWorkBuddyVerificationFailureBranches(t *testing.T) {
	ctx := context.Background()
	verified := workbuddy.VerifiedAccount{
		Target: "workbuddy-cn", AccountID: "wb", SourceID: "src",
		DisplayName: "WB", MaskedIdentity: "****",
	}

	t.Run("not wired", func(t *testing.T) {
		if _, err := (&workBuddyVerificationSink{}).AcceptVerifiedAccount(
			ctx, verified, []byte("secret"),
		); err == nil {
			t.Fatal("verification sink without services should fail")
		}
	})

	t.Run("account lookup error", func(t *testing.T) {
		st, err := store.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = st.Close() })
		acct := accounts.NewService(st.DB())
		creds := capabilities.NewCredentialService(
			failingAppSecretStore{}, newAppSecretStore(),
			capabilities.Grant{"credentials.managed": true}, nil,
		)
		if _, err := st.DB().Exec(`DROP TABLE accounts`); err != nil {
			t.Fatal(err)
		}
		if _, err := newWorkBuddyVerificationSink(acct, creds).AcceptVerifiedAccount(
			ctx, verified, []byte("secret"),
		); err == nil {
			t.Fatal("non-NotFound account lookup error should be returned")
		}
	})

	t.Run("new account rollback", func(t *testing.T) {
		st, err := store.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = st.Close() })
		acct := accounts.NewService(st.DB())
		file := newAppSecretStore()
		creds := capabilities.NewCredentialService(
			failingAppSecretStore{}, file,
			capabilities.Grant{
				"credentials.managed": true,
				"credentials.resolve": true,
			}, nil,
		)
		if _, err := st.DB().Exec(`CREATE TRIGGER coverage_verification_insert
BEFORE INSERT ON accounts
BEGIN
  SELECT RAISE(ABORT, 'account insert blocked');
END;`); err != nil {
			t.Fatal(err)
		}
		if _, err := newWorkBuddyVerificationSink(acct, creds).AcceptVerifiedAccount(
			ctx, verified, []byte("new-secret"),
		); err == nil {
			t.Fatal("new account write failure should be returned")
		}
		_, err = creds.Resolve(ctx, capabilities.CredentialRef{
			Target: verified.Target, AccountID: verified.AccountID,
			SourceID: verified.SourceID, Kind: capabilities.Managed,
		})
		if !errors.Is(err, capabilities.ErrSecretNotFound) {
			t.Fatalf("failed new account must remove persisted credential: %v", err)
		}
	})

	t.Run("restore old credential error", func(t *testing.T) {
		st, err := store.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = st.Close() })
		acct := accounts.NewService(st.DB())
		file := newAppSecretStore()
		flaky := &flakyAppSecretStore{appSecretStore: file}
		creds := capabilities.NewCredentialService(
			failingAppSecretStore{}, flaky,
			capabilities.Grant{
				"credentials.managed": true,
				"credentials.resolve": true,
			}, nil,
		)
		if err := acct.Discover(ctx, verified.Target, []accounts.DiscoveredAccount{{
			ID: verified.AccountID, DisplayName: "WB", MaskedIdentity: "****",
			SourceID: verified.SourceID, SourceKind: accounts.KindManaged,
		}}); err != nil {
			t.Fatal(err)
		}
		ref := capabilities.CredentialRef{
			Target: verified.Target, AccountID: verified.AccountID,
			SourceID: verified.SourceID, Kind: capabilities.Managed,
		}
		if err := creds.Persist(ctx, ref, "old-secret"); err != nil {
			t.Fatal(err)
		}
		flaky.setCalls = 0
		flaky.failSetAt = 2
		if _, err := st.DB().Exec(`CREATE TRIGGER coverage_verification_update
BEFORE UPDATE ON accounts
BEGIN
  SELECT RAISE(ABORT, 'account update blocked');
END;`); err != nil {
			t.Fatal(err)
		}
		_, err = newWorkBuddyVerificationSink(acct, creds).AcceptVerifiedAccount(
			ctx, verified, []byte("new-secret"),
		)
		if err == nil || !strings.Contains(err.Error(), "恢复旧凭据失败") {
			t.Fatalf("credential restore failure = %v", err)
		}
	})
}

type flakyAppSecretStore struct {
	*appSecretStore
	setCalls  int
	failSetAt int
}

func (s *flakyAppSecretStore) Set(
	ctx context.Context, service, account, secret string,
) error {
	s.setCalls++
	if s.setCalls == s.failSetAt {
		return errors.New("secret persistence failed")
	}
	return s.appSecretStore.Set(ctx, service, account, secret)
}

func setWorkBuddyDailyState(t *testing.T, a *App, accountID string, state engine.State) {
	t.Helper()
	day := engine.BeijingDay(a.now())
	ts := a.now().UTC().Format(time.RFC3339)
	if _, err := a.store.DB().Exec(`
INSERT INTO daily_ops
  (target, account_id, operation, day_key, state, updated_at)
VALUES ('workbuddy-cn', ?, 'checkin', ?, ?, ?)
ON CONFLICT(target, account_id, operation, day_key)
DO UPDATE SET state = excluded.state, message = '', updated_at = excluded.updated_at`,
		accountID, day, string(state), ts); err != nil {
		t.Fatal(err)
	}
}

func workBuddyPreferred(t *testing.T, acct *accounts.Service, accountID string) accounts.Source {
	t.Helper()
	source, err := acct.PreferredSource(context.Background(), "workbuddy-cn", accountID)
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func workBuddyCheckinKey(accountID string) engine.Key {
	return engine.Key{
		Target: "workbuddy-cn", AccountID: accountID, Operation: engine.Checkin,
	}
}

func execCoverageSQL(t *testing.T, a *App, statement string) {
	t.Helper()
	if _, err := a.store.DB().Exec(statement); err != nil {
		t.Fatal(err)
	}
}
