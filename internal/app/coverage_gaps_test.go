package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"qka/internal/accounts"
	"qka/internal/adapterhost"
	"qka/internal/features"
	"qka/internal/gui"
	"qka/internal/providers"
	"qka/internal/settings"
	"qka/internal/status"
)

func TestAccountManagementSuccessPaths(t *testing.T) {
	a := coverageApp(t)
	ctx := context.Background()
	if err := a.accounts.Discover(ctx, "target", []accounts.DiscoveredAccount{{
		ID: "acct", DisplayName: "Account", MaskedIdentity: "****",
		SourceID: "src", SourceKind: accounts.KindManaged,
	}}); err != nil {
		t.Fatal(err)
	}

	if _, err := a.manageAccounts(ctx, map[string]any{
		"action": "set_enabled", "target": "target", "account_id": "acct", "enabled": true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.manageAccounts(ctx, map[string]any{
		"action": "pin", "target": "target", "account_id": "acct",
		"source_id": "src", "pinned": true,
	}); err != nil {
		t.Fatal(err)
	}
	acct, err := a.accounts.Get(ctx, "target", "acct")
	if err != nil {
		t.Fatal(err)
	}
	if len(acct.Sources) != 1 || !acct.Sources[0].Pinned {
		t.Fatalf("pin result = %+v", acct.Sources)
	}
	if _, err := a.manageAccounts(ctx, map[string]any{
		"action": "delete", "target": "target", "account_id": "acct",
	}); err != nil {
		t.Fatal(err)
	}
	items, err := a.accounts.List(ctx, "target")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("delete left accounts: %+v", items)
	}
}

func TestAdapterLaunchFailureIsRecorded(t *testing.T) {
	root := t.TempDir()
	adapters := filepath.Join(root, "adapters")
	adapterDir := filepath.Join(adapters, "broken")
	if err := os.MkdirAll(adapterDir, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := adapterhost.Manifest{
		ID: "broken", Name: "Broken", Version: "1", Entry: "adapter.js",
		Actions: []adapterhost.Action{{Op: "checkin.run"}},
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(adapterDir, "manifest.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	a, err := Build(context.Background(), Config{
		ConfigDir:    filepath.Join(root, "config"),
		AdaptersRoot: adapters,
		BunPath:      filepath.Join(root, "missing-bun"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	loaded := a.LoadedAdapters()
	if len(loaded) != 1 || loaded[0].LoadErr == nil || loaded[0].Host != nil {
		t.Fatalf("loaded = %+v", loaded)
	}
	if !a.registryAdapters()[0].Disabled {
		t.Fatal("failed adapter must be disabled in registry view")
	}
}

func TestDiscoverHostResultsAndPersistenceFailure(t *testing.T) {
	t.Run("invalid JSON", func(t *testing.T) {
		a := coverageApp(t)
		host := launchCoverageAdapterHost(t, `[]`)
		a.hosts["script-target"] = host
		if _, err := a.discoverViaHost(context.Background(), "script-target"); err == nil {
			t.Fatal("invalid adapter result should fail")
		}
	})

	t.Run("discover persistence", func(t *testing.T) {
		a := coverageApp(t)
		host := launchCoverageAdapterHost(t, `{
			"status":"ok",
			"accounts":[{
				"id":"a1","displayName":"one","maskedIdentity":"****1",
				"source":{"id":"s1","kind":"managed","health":"healthy"}
			}]
		}`)
		a.hosts["script-target"] = host
		if _, err := a.store.DB().Exec(`
CREATE TRIGGER coverage_fail_source_insert
BEFORE INSERT ON sources
BEGIN
  SELECT RAISE(ABORT, 'source insert blocked');
END;`); err != nil {
			t.Fatal(err)
		}
		if _, err := a.discoverAccounts(context.Background(), "script-target"); err == nil {
			t.Fatal("discover persistence failure should be returned")
		}
	})
}

func TestRunAllActionFilteringSkippingAndResults(t *testing.T) {
	ctx := context.Background()
	oldOperation := executeRunOperation
	t.Cleanup(func() { executeRunOperation = oldOperation })

	t.Run("plan failure", func(t *testing.T) {
		a := coverageApp(t)
		if _, err := a.store.DB().Exec(`DROP TABLE accounts`); err != nil {
			t.Fatal(err)
		}
		a.status = status.NewService(a.store.DB(), a.now, func(string) []string {
			return []string{"checkin"}
		})
		if _, err := a.RunAllActionSummary(ctx, "all"); err == nil {
			t.Fatal("plan failure should be returned")
		}
	})

	t.Run("action filter", func(t *testing.T) {
		a := coverageApp(t)
		discoverAppAccount(t, a, "target", "a1", "s1", accounts.HealthHealthy)
		a.status = status.NewService(a.store.DB(), a.now, func(string) []string {
			return []string{"checkin", "keepalive"}
		})
		executeRunOperation = func(*App, context.Context, string, string, string) (any, error) {
			return "ok", nil
		}
		if _, err := a.RunAllActionSummary(ctx, "keepalive"); !errors.Is(err, features.ErrKeepaliveDisabled) {
			t.Fatalf("keepalive must be disabled before execution, got %v", err)
		}
		summary, err := a.RunAllActionSummary(ctx, "all")
		if err != nil || summary.Attempted != 1 || summary.Succeeded != 1 {
			t.Fatalf("summary = %+v err=%v", summary, err)
		}
	})

	t.Run("no change", func(t *testing.T) {
		a := coverageApp(t)
		discoverAppAccount(t, a, "target", "a1", "s1", accounts.HealthHealthy)
		if err := a.accounts.MarkSourceFailure(ctx, "target", "a1", "s1"); err != nil {
			t.Fatal(err)
		}
		summary, err := a.RunAllActionSummary(ctx, "all")
		if err != nil || summary.Attempted != 0 || summary.Skipped != 1 {
			t.Fatalf("summary = %+v err=%v", summary, err)
		}
	})

	t.Run("operation failure", func(t *testing.T) {
		a := coverageApp(t)
		discoverAppAccount(t, a, "target", "a1", "s1", accounts.HealthHealthy)
		executeRunOperation = func(*App, context.Context, string, string, string) (any, error) {
			return nil, errors.New("operation failed")
		}
		summary, err := a.RunAllActionSummary(ctx, "all")
		if err == nil || summary.Attempted != 1 || summary.NeedsAttention != 1 {
			t.Fatalf("summary = %+v err=%v", summary, err)
		}
	})
}

func TestRunAllReturnsProviderRuntimeFailure(t *testing.T) {
	a := coverageApp(t)
	ctx := context.Background()
	if err := a.accounts.Discover(ctx, "workbuddy-cn", []accounts.DiscoveredAccount{{
		ID: "wb", DisplayName: "WB", MaskedIdentity: "****",
		SourceID: "src", SourceKind: accounts.KindManaged,
		Health: accounts.HealthReauthRequired,
	}}); err != nil {
		t.Fatal(err)
	}
	a.cfg.ProviderPluginsRoot = "plugins"
	a.providers = providers.NewService(
		a.store.DB(), a.accounts, a.credentials, a.engine,
		func(context.Context) (providers.Runtime, error) {
			return nil, errors.New("provider runtime unavailable")
		},
		a.now,
	)
	if _, err := a.RunAllActionSummary(ctx, "all"); err == nil {
		t.Fatal("provider error should be returned")
	}
}

func TestGUIErrorAndContentConversions(t *testing.T) {
	oldExecutable, oldHome, oldOpen := appExecutable, userHomeDir, openBrowser
	t.Cleanup(func() {
		appExecutable, userHomeDir, openBrowser = oldExecutable, oldHome, oldOpen
	})

	a := coverageApp(t)
	state := &guiState{
		app: a, settings: settings.Default(),
		audit: []gui.AuditLine{},
	}
	if _, err := a.store.DB().Exec(`
INSERT INTO logs(ts,target,account_id,operation,state,level,message)
VALUES('2026-10-30T10:00:00Z','target','acct','checkin','ok','info','message')`); err != nil {
		t.Fatal(err)
	}
	page, err := state.logs(context.Background(), gui.LogFilter{Target: "target"}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 1 || page.Entries[0].Message != "message" {
		t.Fatalf("log page = %+v", page)
	}
	if _, err := a.store.DB().Exec(`DROP TABLE logs`); err != nil {
		t.Fatal(err)
	}
	if _, err := state.logs(context.Background(), gui.LogFilter{}, 10, 0); err == nil {
		t.Fatal("log query failure should be returned")
	}

	a.loaded = []Loaded{{
		Manifest: adapterhost.Manifest{
			ID: "adapter", Name: "Adapter", Version: "1",
		},
	}}
	plugins, err := state.plugins(context.Background())
	if err != nil || len(plugins.Plugins) != 1 || plugins.Plugins[0].ID != "adapter" {
		t.Fatalf("plugins = %+v err=%v", plugins, err)
	}

	changed := settings.Default()
	if err := changed.Set("launch_at_login", true); err != nil {
		t.Fatal(err)
	}
	homeFile := filepath.Join(t.TempDir(), "home-file")
	if err := os.WriteFile(homeFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	appExecutable = func() (string, error) { return "/bin/sh", nil }
	userHomeDir = func() (string, error) { return homeFile, nil }
	if err := state.afterSaveSettings(changed); err == nil {
		t.Fatal("autostart write failure should be returned")
	}

	lockPath := filepath.Join(t.TempDir(), "config-file")
	if err := os.WriteFile(lockPath, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.cfg.ConfigDir = lockPath
	if err := a.RunProviderAccount(context.Background(), "p", "a", "checkin"); err == nil {
		t.Fatal("provider run lock failure should be returned")
	}
	a.cfg.ConfigDir = ""
	if err := a.RunAccount(context.Background(), "missing", "a", "checkin"); err == nil {
		t.Fatal("ordinary account operation failure should be returned")
	}
}

func TestToolKeepaliveDisabledAndEffectsBranches(t *testing.T) {
	a := coverageApp(t)
	host := launchCoverageAdapterHost(t, `{"ok":true}`)
	a.hosts["script-target"] = host
	if _, err := a.runOperation(context.Background(), "script-target", "acct", "keepalive"); !errors.Is(err, features.ErrKeepaliveDisabled) {
		t.Fatalf("keepalive must be rejected, got %v", err)
	}

	a.loaded = []Loaded{{
		Manifest: adapterhost.Manifest{ID: "running"},
		Host:     host,
	}}
	if a.effectsComplete() {
		t.Fatal("running adapter without actions should make effects incomplete")
	}
}

func TestNilStoreTaskRunIsNoop(t *testing.T) {
	if err := (&App{}).RecordTaskRun(context.Background(), RunSummary{}); err != nil {
		t.Fatal(err)
	}
}

func discoverAppAccount(
	t *testing.T, a *App, target, accountID, sourceID string, health accounts.SourceHealth,
) {
	t.Helper()
	if err := a.accounts.Discover(context.Background(), target, []accounts.DiscoveredAccount{{
		ID: accountID, DisplayName: accountID, MaskedIdentity: "****",
		SourceID: sourceID, SourceKind: accounts.KindManaged, Health: health,
	}}); err != nil {
		t.Fatal(err)
	}
}

func launchCoverageAdapterHost(t *testing.T, result string) *adapterhost.Host {
	t.Helper()
	dir := t.TempDir()
	manifest := adapterhost.Manifest{
		ID: "script-target", Name: "Script Target", Version: "1", Entry: "adapter.sh",
		Actions: []adapterhost.Action{
			{Op: "accounts.discover"},
			{Op: "checkin.run"},
			{Op: "keepalive.run"},
		},
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`#!/usr/bin/env python3
import json
import sys

result_payload = json.loads(r'''%s''')
for line in sys.stdin:
    request = json.loads(line)
    result = {} if request["method"] == "initialize" else result_payload
    print(json.dumps({
        "jsonrpc": "2.0",
        "id": request["id"],
        "result": result,
    }), flush=True)
`, result)
	scriptPath := filepath.Join(dir, "adapter.sh")
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	host, err := adapterhost.LaunchWith(ctx, scriptPath, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Close() })
	return host
}
