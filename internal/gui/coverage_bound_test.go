package gui

import (
	"context"
	"errors"
	"testing"
	"time"

	"qka/internal/features"
	"qka/internal/providers"
	"qka/internal/schedule"
	"qka/internal/selfupdate"
	"qka/internal/settings"
	"qka/internal/status"
	"qka/internal/tokenspeed"
	"qka/internal/workbuddy"
)

type coverageAccounts struct {
	err error
}

func (f *coverageAccounts) List(context.Context) ([]AccountView, error) {
	return []AccountView{{}}, f.err
}

func (f *coverageAccounts) Discover(context.Context, string) error { return f.err }

func (f *coverageAccounts) BeginLogin(context.Context, string) (workbuddy.LoginView, error) {
	return workbuddy.LoginView{Status: workbuddy.LoginPending}, f.err
}

func (f *coverageAccounts) LoginStatus(context.Context, string) workbuddy.LoginView {
	return workbuddy.LoginView{Status: workbuddy.LoginPending}
}

func (f *coverageAccounts) CancelLogin(context.Context, string) (workbuddy.LoginView, error) {
	return workbuddy.LoginView{Status: workbuddy.LoginCancelled}, f.err
}

func (f *coverageAccounts) ImportSource(context.Context, string, string, string) error {
	return f.err
}

func (f *coverageAccounts) SetEnabled(context.Context, string, string, bool) error {
	return f.err
}

func (f *coverageAccounts) Delete(context.Context, string, string) error { return f.err }

func (f *coverageAccounts) PinSource(context.Context, string, string, string, bool) error {
	return f.err
}

func (f *coverageAccounts) Run(context.Context, string, string, string) error {
	return f.err
}

func (f *coverageAccounts) SetTaskEnabled(context.Context, string, string, string, bool) error {
	return f.err
}

func TestBoundCoverageHappyPaths(t *testing.T) {
	current := settings.Default()
	accounts := &coverageAccounts{}
	svc := NewBoundService(Deps{
		Targets: []string{"workbuddy-cn"},
		Status: func(context.Context) (status.View, error) {
			return status.View{}, nil
		},
		Plan: func(context.Context) (status.Plan, error) {
			return status.Plan{Items: []status.PlanItem{{Target: "workbuddy-cn"}}}, nil
		},
		Run:          func(context.Context) error { return nil },
		Settings:     func() *settings.Settings { return current },
		SaveSettings: func(*settings.Settings) error { return nil },
		ScheduleGet: func(context.Context) (schedule.View, error) {
			return schedule.View{Enabled: true}, nil
		},
		ScheduleSetEnabled: func(context.Context, bool) (schedule.View, error) {
			return schedule.View{Installed: true}, nil
		},
		ScheduleSetConfig: func(_ context.Context, interval int, action string) (schedule.View, error) {
			return schedule.View{IntervalMinutes: interval, Action: action}, nil
		},
		UpdateCheck: func(context.Context) (selfupdate.Status, error) {
			return selfupdate.Status{Available: true}, nil
		},
		UpdateInstall: func(context.Context) (selfupdate.Status, error) {
			return selfupdate.Status{Ready: true}, nil
		},
		TokenSpeedGet: func(context.Context) (tokenspeed.Snapshot, error) {
			return tokenspeed.Snapshot{OutputTokensPerSecond: 12}, nil
		},
		Accounts: accounts,
		ProvidersList: func(context.Context) (providers.View, error) {
			return providers.View{AccountCount: 1}, nil
		},
		ProviderBeginLogin: func(context.Context, string, int, map[string]string) (providers.LoginView, error) {
			return providers.LoginView{Status: "prompt"}, nil
		},
		ProviderFinishLogin: func(context.Context, string, string) (providers.LoginView, error) {
			return providers.LoginView{Status: "authorized"}, nil
		},
		ProviderLoginAPIKey: func(context.Context, string, int, map[string]string, string) (providers.LoginView, error) {
			return providers.LoginView{Status: "verified"}, nil
		},
		ProviderSetEnabled: func(context.Context, string, bool) error {
			return nil
		},
		ProviderSetAccountEnabled: func(context.Context, string, string, bool) error {
			return nil
		},
		ProviderSetTaskEnabled: func(context.Context, string, string, string, bool) error {
			return nil
		},
		ProviderRun: func(context.Context, string, string, string) error {
			return nil
		},
		ProviderTestModel: func(context.Context, string, string, string) (providers.ModelTestView, error) {
			return providers.ModelTestView{OK: true, Status: 200}, nil
		},
		ProviderDeleteAccount: func(context.Context, string, string) error {
			return nil
		},
		ProviderHistory: func(context.Context, string, string, int) ([]providers.RunItemView, []providers.QuotaReadingView, error) {
			return []providers.RunItemView{{RunID: "r1"}}, []providers.QuotaReadingView{{Plan: "pro"}}, nil
		},
		Logs: func(context.Context, LogFilter, int, int64) (LogPage, error) {
			return LogPage{Entries: []LogEntry{{ID: 1}}}, nil
		},
		AgentStatus: func(context.Context) (AgentView, error) {
			return NewAgentView(nil), nil
		},
		Plugins: func(context.Context) (PluginsView, error) {
			return NewPluginsView(nil, true, nil), nil
		},
		SetDevMode: func(bool) error { return nil },
	})

	checks := []struct {
		name string
		call func() error
	}{
		{"status", func() error { _, err := svc.Status(); return err }},
		{"plan", func() error { _, err := svc.PlanValidate(); return err }},
		{"run", svc.Run},
		{"settings schema", func() error { _, err := svc.SettingsSchema(); return err }},
		{"settings get", func() error { _, err := svc.SettingsGet(); return err }},
		{"settings bool", func() error { return svc.SettingsSet("launch_at_login", true) }},
		{"settings string", func() error { return svc.SettingsSetString("theme", "dark") }},
		{"schedule get", func() error { _, err := svc.ScheduleGet(); return err }},
		{"schedule enabled", func() error { _, err := svc.ScheduleSetEnabled(true); return err }},
		{"schedule config", func() error { _, err := svc.ScheduleSetConfig(30, "all"); return err }},
		{"update check", func() error { _, err := svc.UpdateCheck(); return err }},
		{"update install", func() error { _, err := svc.UpdateInstall(); return err }},
		{"token speed", func() error { _, err := svc.TokenSpeedGet(); return err }},
		{"apps", func() error { _, err := svc.AppsList(); return err }},
		{"providers", func() error { _, err := svc.ProvidersList(); return err }},
		{"provider begin", func() error {
			_, err := svc.ProvidersBeginLogin("qoder", 0, nil)
			return err
		}},
		{"provider finish", func() error {
			_, err := svc.ProvidersFinishLogin("session", "code")
			return err
		}},
		{"provider api key", func() error {
			_, err := svc.ProvidersLoginAPIKey("qoder", 0, nil, "key")
			return err
		}},
		{"provider enabled", func() error { return svc.ProvidersSetEnabled("qoder", true) }},
		{"provider account", func() error {
			return svc.ProvidersSetAccountEnabled("qoder", "a", true)
		}},
		{"provider task", func() error {
			return svc.ProvidersSetTaskEnabled("qoder", "a", "checkin", true)
		}},
		{"provider run", func() error { return svc.ProvidersRun("qoder", "a", "checkin") }},
		{"provider test model", func() error {
			_, err := svc.ProvidersTestModel("qoder", "a", "model-a")
			return err
		}},
		{"provider delete", func() error { return svc.ProvidersDeleteAccount("qoder", "a") }},
		{"provider history", func() error { _, err := svc.ProvidersHistory("qoder", "a", 10); return err }},
		{"accounts list", func() error { _, err := svc.AccountsList(); return err }},
		{"accounts run", func() error { return svc.AccountsRun("workbuddy-cn", "a", "checkin") }},
		{"accounts task", func() error {
			return svc.AccountsSetTaskEnabled("workbuddy-cn", "a", "checkin", false)
		}},
		{"accounts enabled", func() error {
			return svc.AccountsSetEnabled("workbuddy-cn", "a", true)
		}},
		{"accounts delete", func() error { return svc.AccountsDelete("workbuddy-cn", "a") }},
		{"accounts pin", func() error {
			return svc.AccountsPinSource("workbuddy-cn", "a", "s", true)
		}},
		{"accounts discover", func() error { return svc.AccountsDiscover("workbuddy-cn") }},
		{"accounts begin", func() error {
			_, err := svc.AccountsBeginLogin("workbuddy-cn")
			return err
		}},
		{"accounts status", func() error {
			_, err := svc.AccountsLoginStatus("workbuddy-cn")
			return err
		}},
		{"accounts cancel", func() error {
			_, err := svc.AccountsCancelLogin("workbuddy-cn")
			return err
		}},
		{"accounts import", func() error {
			return svc.AccountsImportSource("workbuddy-cn", "a", "s")
		}},
		{"logs", func() error { _, err := svc.LogsTail("", "", "", "", 10, 0); return err }},
		{"agent", func() error { _, err := svc.AgentStatus(); return err }},
		{"plugins", func() error { _, err := svc.PluginsList(); return err }},
		{"dev mode", func() error { return svc.SetDevMode(true) }},
	}
	for _, check := range checks {
		if err := check.call(); err != nil {
			t.Errorf("%s: %v", check.name, err)
		}
	}
}

func TestBoundCoverageNotWired(t *testing.T) {
	svc := NewBoundService(Deps{})
	checks := []struct {
		name string
		call func() error
	}{
		{"schedule get", func() error { _, err := svc.ScheduleGet(); return err }},
		{"schedule enabled", func() error { _, err := svc.ScheduleSetEnabled(true); return err }},
		{"schedule config", func() error { _, err := svc.ScheduleSetConfig(1, "all"); return err }},
		{"update check", func() error { _, err := svc.UpdateCheck(); return err }},
		{"update install", func() error { _, err := svc.UpdateInstall(); return err }},
		{"token speed", func() error { _, err := svc.TokenSpeedGet(); return err }},
		{"providers", func() error { _, err := svc.ProvidersList(); return err }},
		{"provider begin", func() error { _, err := svc.ProvidersBeginLogin("", 0, nil); return err }},
		{"provider finish", func() error { _, err := svc.ProvidersFinishLogin("", ""); return err }},
		{"provider api key", func() error { _, err := svc.ProvidersLoginAPIKey("", 0, nil, ""); return err }},
		{"provider enabled", func() error { return svc.ProvidersSetEnabled("", true) }},
		{"provider account", func() error { return svc.ProvidersSetAccountEnabled("", "", true) }},
		{"provider task", func() error { return svc.ProvidersSetTaskEnabled("", "", "", true) }},
		{"provider run", func() error { return svc.ProvidersRun("", "", "checkin") }},
		{"provider test model", func() error {
			_, err := svc.ProvidersTestModel("", "", "")
			return err
		}},
		{"provider delete", func() error { return svc.ProvidersDeleteAccount("", "") }},
		{"provider history", func() error { _, err := svc.ProvidersHistory("", "", 1); return err }},
		{"logs", func() error { _, err := svc.LogsTail("", "", "", "", 1, 0); return err }},
		{"agent", func() error { _, err := svc.AgentStatus(); return err }},
		{"plugins", func() error { _, err := svc.PluginsList(); return err }},
		{"dev mode", func() error { return svc.SetDevMode(true) }},
	}
	for _, check := range checks {
		if err := check.call(); !errors.Is(err, errNotWired) {
			t.Errorf("%s returned %v, want errNotWired", check.name, err)
		}
	}
}

func TestBoundCoverageCallbackErrors(t *testing.T) {
	boom := errors.New("boom")
	failingAccounts := &coverageAccounts{err: boom}
	svc := NewBoundService(Deps{
		Status: func(context.Context) (status.View, error) { return status.View{}, boom },
		Plan:   func(context.Context) (status.Plan, error) { return status.Plan{}, boom },
		Run:    func(context.Context) error { return boom },
		Settings: func() *settings.Settings {
			return settings.Default()
		},
		SaveSettings:      func(*settings.Settings) error { return boom },
		AfterSaveSettings: func(*settings.Settings) error { return boom },
		ScheduleGet:       func(context.Context) (schedule.View, error) { return schedule.View{}, boom },
		ScheduleSetEnabled: func(context.Context, bool) (schedule.View, error) {
			return schedule.View{}, boom
		},
		ScheduleSetConfig: func(context.Context, int, string) (schedule.View, error) {
			return schedule.View{}, boom
		},
		UpdateCheck:   func(context.Context) (selfupdate.Status, error) { return selfupdate.Status{}, boom },
		UpdateInstall: func(context.Context) (selfupdate.Status, error) { return selfupdate.Status{}, boom },
		TokenSpeedGet: func(context.Context) (tokenspeed.Snapshot, error) {
			return tokenspeed.Snapshot{}, boom
		},
		Accounts: failingAccounts,
		ProvidersList: func(context.Context) (providers.View, error) {
			return providers.View{}, boom
		},
		ProviderBeginLogin: func(context.Context, string, int, map[string]string) (providers.LoginView, error) {
			return providers.LoginView{}, boom
		},
		ProviderFinishLogin: func(context.Context, string, string) (providers.LoginView, error) {
			return providers.LoginView{}, boom
		},
		ProviderLoginAPIKey: func(context.Context, string, int, map[string]string, string) (providers.LoginView, error) {
			return providers.LoginView{}, boom
		},
		ProviderSetEnabled: func(context.Context, string, bool) error {
			return boom
		},
		ProviderSetAccountEnabled: func(context.Context, string, string, bool) error {
			return boom
		},
		ProviderSetTaskEnabled: func(context.Context, string, string, string, bool) error {
			return boom
		},
		ProviderRun: func(context.Context, string, string, string) error {
			return boom
		},
		ProviderTestModel: func(context.Context, string, string, string) (providers.ModelTestView, error) {
			return providers.ModelTestView{}, boom
		},
		ProviderDeleteAccount: func(context.Context, string, string) error {
			return boom
		},
		ProviderHistory: func(context.Context, string, string, int) ([]providers.RunItemView, []providers.QuotaReadingView, error) {
			return nil, nil, boom
		},
		Logs: func(context.Context, LogFilter, int, int64) (LogPage, error) {
			return LogPage{}, boom
		},
		AgentStatus: func(context.Context) (AgentView, error) { return AgentView{}, boom },
		Plugins:     func(context.Context) (PluginsView, error) { return PluginsView{}, boom },
		SetDevMode:  func(bool) error { return boom },
	})

	checks := []struct {
		name string
		call func() error
	}{
		{"status", func() error { _, err := svc.Status(); return err }},
		{"plan", func() error { _, err := svc.PlanValidate(); return err }},
		{"run plan", svc.Run},
		{"schedule get", func() error { _, err := svc.ScheduleGet(); return err }},
		{"schedule enabled", func() error { _, err := svc.ScheduleSetEnabled(true); return err }},
		{"schedule config", func() error { _, err := svc.ScheduleSetConfig(1, "all"); return err }},
		{"update check", func() error { _, err := svc.UpdateCheck(); return err }},
		{"update install", func() error { _, err := svc.UpdateInstall(); return err }},
		{"token speed", func() error { _, err := svc.TokenSpeedGet(); return err }},
		{"apps", func() error { _, err := svc.AppsList(); return err }},
		{"providers", func() error { _, err := svc.ProvidersList(); return err }},
		{"provider begin", func() error { _, err := svc.ProvidersBeginLogin("", 0, nil); return err }},
		{"provider finish", func() error { _, err := svc.ProvidersFinishLogin("", ""); return err }},
		{"provider api key", func() error { _, err := svc.ProvidersLoginAPIKey("", 0, nil, ""); return err }},
		{"provider enabled", func() error { return svc.ProvidersSetEnabled("", true) }},
		{"provider account", func() error { return svc.ProvidersSetAccountEnabled("", "", true) }},
		{"provider task", func() error { return svc.ProvidersSetTaskEnabled("", "", "", true) }},
		{"provider run", func() error { return svc.ProvidersRun("", "", "checkin") }},
		{"provider test model", func() error {
			_, err := svc.ProvidersTestModel("", "", "")
			return err
		}},
		{"provider delete", func() error { return svc.ProvidersDeleteAccount("", "") }},
		{"provider history", func() error { _, err := svc.ProvidersHistory("", "", 1); return err }},
		{"accounts list", func() error { _, err := svc.AccountsList(); return err }},
		{"accounts run", func() error { return svc.AccountsRun("", "", "checkin") }},
		{"accounts task", func() error { return svc.AccountsSetTaskEnabled("", "", "", true) }},
		{"accounts enabled", func() error { return svc.AccountsSetEnabled("", "", true) }},
		{"accounts delete", func() error { return svc.AccountsDelete("", "") }},
		{"accounts pin", func() error { return svc.AccountsPinSource("", "", "", true) }},
		{"accounts discover", func() error { return svc.AccountsDiscover("") }},
		{"accounts begin", func() error { _, err := svc.AccountsBeginLogin(""); return err }},
		{"accounts cancel", func() error { _, err := svc.AccountsCancelLogin(""); return err }},
		{"accounts import", func() error { return svc.AccountsImportSource("", "", "") }},
		{"logs", func() error { _, err := svc.LogsTail("", "", "", "", 1, 0); return err }},
		{"agent", func() error { _, err := svc.AgentStatus(); return err }},
		{"plugins", func() error { _, err := svc.PluginsList(); return err }},
		{"dev mode", func() error { return svc.SetDevMode(true) }},
	}
	for _, check := range checks {
		if err := check.call(); !errors.Is(err, boom) {
			t.Errorf("%s returned %v, want boom", check.name, err)
		}
	}

	runSvc := NewBoundService(Deps{
		Plan: func(context.Context) (status.Plan, error) { return status.Plan{}, nil },
		Run:  func(context.Context) error { return boom },
	})
	if err := runSvc.Run(); !errors.Is(err, boom) {
		t.Fatalf("run callback error returned %v", err)
	}
}

func TestBoundCoverageSettingsErrors(t *testing.T) {
	boom := errors.New("boom")
	current := settings.Default()
	svc := NewBoundService(Deps{
		Settings:          func() *settings.Settings { return current },
		SaveSettings:      func(*settings.Settings) error { return boom },
		AfterSaveSettings: func(*settings.Settings) error { return boom },
	})
	if err := svc.SettingsSet("missing", true); !errors.Is(err, settings.ErrUnknownKey) {
		t.Fatalf("unknown bool setting returned %v", err)
	}
	if err := svc.SettingsSet("launch_at_login", true); !errors.Is(err, boom) {
		t.Fatalf("save error returned %v", err)
	}
	if err := svc.SettingsSetString("missing", "dark"); !errors.Is(err, settings.ErrUnknownKey) {
		t.Fatalf("unknown string setting returned %v", err)
	}
	if err := svc.SettingsSetString("theme", "dark"); !errors.Is(err, boom) {
		t.Fatalf("string save error returned %v", err)
	}

	saving := NewBoundService(Deps{
		Settings:          func() *settings.Settings { return current },
		SaveSettings:      func(*settings.Settings) error { return nil },
		AfterSaveSettings: func(*settings.Settings) error { return boom },
	})
	if err := saving.SettingsSet("launch_at_login", true); !errors.Is(err, boom) {
		t.Fatalf("bool side-effect error returned %v", err)
	}
	if err := saving.SettingsSetString("theme", "dark"); !errors.Is(err, boom) {
		t.Fatalf("string side-effect error returned %v", err)
	}
}

func TestBoundUpdateInstallSchedulesQuit(t *testing.T) {
	quit := make(chan struct{}, 1)
	svc := NewBoundService(Deps{
		UpdateInstall: func(context.Context) (selfupdate.Status, error) {
			return selfupdate.Status{Ready: true}, nil
		},
		QuitApp: func() { quit <- struct{}{} },
	})
	if _, err := svc.UpdateInstall(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-quit:
	case <-time.After(time.Second):
		t.Fatal("UpdateInstall did not schedule QuitApp")
	}
}

func TestBoundProviderRunRejectsUnsupportedAction(t *testing.T) {
	called := false
	svc := NewBoundService(Deps{ProviderRun: func(context.Context, string, string, string) error {
		called = true
		return nil
	}})
	if err := svc.ProvidersRun("", "", "unsupported"); !errors.Is(err, errUnsupportedAccountAction) {
		t.Fatalf("unsupported provider action returned %v", err)
	}
	if called {
		t.Fatal("unsupported provider action should not reach the callback")
	}
}

func TestBoundTaskOperationsHideDisabledKeepalive(t *testing.T) {
	svc := NewBoundService(Deps{
		ProviderSetTaskEnabled: func(context.Context, string, string, string, bool) error {
			t.Fatal("disabled keepalive must not reach the provider callback")
			return nil
		},
		ProviderRun: func(context.Context, string, string, string) error {
			t.Fatal("disabled keepalive must not reach the provider runner")
			return nil
		},
		Accounts: &coverageAccounts{},
	})
	if err := svc.ProvidersSetTaskEnabled("qoder", "a", "keepalive", true); !errors.Is(err, features.ErrKeepaliveDisabled) {
		t.Fatalf("provider task keepalive = %v", err)
	}
	if err := svc.ProvidersRun("qoder", "a", "keepalive"); !errors.Is(err, features.ErrKeepaliveDisabled) {
		t.Fatalf("provider run keepalive = %v", err)
	}
	if err := svc.AccountsSetTaskEnabled("workbuddy-cn", "a", "keepalive", true); !errors.Is(err, features.ErrKeepaliveDisabled) {
		t.Fatalf("account task keepalive = %v", err)
	}
}

func TestBoundSettingsStringEvents(t *testing.T) {
	_ = New(Deps{})
	current := settings.Default()
	svc := NewBoundService(Deps{
		Settings:     func() *settings.Settings { return current },
		SaveSettings: func(*settings.Settings) error { return nil },
		TokenSpeedGet: func(context.Context) (tokenspeed.Snapshot, error) {
			return tokenspeed.Snapshot{OutputTokensPerSecond: 4}, nil
		},
	})
	for _, tc := range []struct {
		key   string
		value string
	}{
		{key: "theme", value: "light"},
		{key: "background", value: "coast"},
		{key: "glass", value: "clear"},
		{key: "title_theme", value: "space"},
	} {
		if err := svc.SettingsSetString(tc.key, tc.value); err != nil {
			t.Errorf("%s: %v", tc.key, err)
		}
	}
}
