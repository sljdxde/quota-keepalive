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

// afternoonAt 是固定的北京午后时刻（用于落库 signed_confirmed）。
func afternoonAt() time.Time {
	return time.Date(2026, 10, 29, 14, 0, 0, 0, time.UTC)
}

func setupWired(t *testing.T, targetURL string) wiredEnv {
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

	eng := engine.New(st.DB(), func() time.Time { return afternoonAt() }, "host")
	return wiredEnv{st: st, host: host, eng: eng}
}

func TestCheckinPersistsDailyOpAndLog(t *testing.T) {
	target := newFakeTarget(t, scenario{accounts: healthyAccounts()})
	defer target.Close()
	env := setupWired(t, target.URL)

	key := engine.Key{Target: "workbuddy-cn", AccountID: "u_1", Operation: engine.Checkin}
	obs, err := env.eng.Run(context.Background(), key, CheckinExecutor(env.host))
	if err != nil {
		t.Fatalf("Run 失败: %v", err)
	}
	if obs.State != engine.SignedConfirmed {
		t.Fatalf("午后成功应 confirmed，实际 %s", obs.State)
	}

	// daily_ops 落库。
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

	// logs 写入。
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
