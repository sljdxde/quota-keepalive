package adapterwiring

import (
	"context"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"qka/internal/adapterhost"
	"qka/internal/capabilities"
	"qka/internal/engine"
	"qka/internal/store"
)

type wiredEnv struct {
	st   *store.Store
	host *adapterhost.Host
	eng  *engine.Engine
}

func beijingAt(hour int) time.Time {
	return time.Date(2026, 10, 29, hour, 0, 0, 0,
		time.FixedZone("Asia/Shanghai", 8*3600))
}

func setupWiredAt(t *testing.T, targetURL string, now func() time.Time) wiredEnv {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	u, _ := url.Parse(targetURL)
	fetch := capabilities.NewHTTPFetch(
		capabilities.AllowedHostsFromManifest([]string{u.Host}),
		capabilities.GrantFromNeeds([]string{"http.outbound"}))

	mux := NewHookMux()
	RegisterHTTPFetch(mux, fetch)
	RegisterLogWrite(mux, st.DB())
	mux.Init["baseURL"] = targetURL

	adapterDir := filepath.Join("..", "..", "adapters", "workbuddy-cn")
	host, err := adapterhost.LaunchWith(context.Background(), "bun", adapterDir, mux)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Close() })

	eng := engine.New(st.DB(), now, "host")
	return wiredEnv{st: st, host: host, eng: eng}
}

func checkinKey() engine.Key {
	return engine.Key{Target: "workbuddy-cn", AccountID: "u_1", Operation: engine.Checkin}
}

func TestCheckinPersistsDailyOpAndLog(t *testing.T) {
	target := newFakeTarget(t, scenario{accounts: healthyAccounts()})
	defer target.Close()
	env := setupWiredAt(t, target.URL, func() time.Time { return beijingAt(14) })

	obs, err := env.eng.Run(context.Background(), checkinKey(), CheckinExecutor(env.host))
	if err != nil {
		t.Fatalf("Run 失败: %v", err)
	}
	if obs.State != engine.SignedConfirmed {
		t.Fatalf("午后成功应 confirmed，实际 %s", obs.State)
	}

	var state string
	err = env.st.DB().QueryRowContext(context.Background(),
		`SELECT state FROM daily_ops WHERE target = ? AND account_id = ? AND operation = ?`,
		"workbuddy-cn", "u_1", "checkin").Scan(&state)
	if err != nil {
		t.Fatalf("daily_ops 未落库: %v", err)
	}
	if state != "signed_confirmed" {
		t.Errorf("落库状态错误: %s", state)
	}

	var msg string
	err = env.st.DB().QueryRowContext(context.Background(),
		`SELECT message FROM logs WHERE target = ? AND account_id = ?`,
		"workbuddy-cn", "u_1").Scan(&msg)
	if err != nil {
		t.Fatalf("日志未写入: %v", err)
	}
	if msg != "签到完成" {
		t.Errorf("日志内容错误: %s", msg)
	}
}

func TestCheckinRunnerEvidenceGate(t *testing.T) {
	cases := []struct {
		name      string
		hour      int
		evidence  bool
		wantState engine.State
		wantMsg   string
	}{
		{"午前总是待复核", 10, true, engine.SignedProvisional, "已领取，待复核"},
		{"午后证据充分确认", 14, true, engine.SignedConfirmed, "已到账"},
		{"午后缺证据待复核", 14, false, engine.SignedProvisional, "已领取，待复核"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := newFakeTarget(t, scenario{
				accounts: healthyAccounts(), checkinEvidence: tc.evidence,
			})
			defer target.Close()

			now := func() time.Time { return beijingAt(tc.hour) }
			env := setupWiredAt(t, target.URL, now)
			runner := NewCheckinRunner(env.eng, env.host, now,
				target.URL+"/api/checkin")

			obs, err := runner.Run(context.Background(), checkinKey())
			if err != nil {
				t.Fatalf("Run 失败: %v", err)
			}
			if obs.State != tc.wantState {
				t.Errorf("期望状态 %s，实际 %s", tc.wantState, obs.State)
			}
			if obs.Message != tc.wantMsg {
				t.Errorf("期望文案 %q，实际 %q", tc.wantMsg, obs.Message)
			}

			var state string
			_ = env.st.DB().QueryRowContext(context.Background(),
				`SELECT state FROM daily_ops WHERE target = ? AND account_id = ?`,
				"workbuddy-cn", "u_1").Scan(&state)
			if state != string(tc.wantState) {
				t.Errorf("落库状态应 %s，实际 %s", tc.wantState, state)
			}
		})
	}
}
