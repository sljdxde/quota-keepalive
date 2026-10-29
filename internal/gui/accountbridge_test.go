package gui

import (
	"context"
	"testing"

	"qka/internal/accounts"
	"qka/internal/login"
	"qka/internal/store"
)

type fakeDiscoverer struct {
	accounts []accounts.DiscoveredAccount
}

func (f *fakeDiscoverer) Discover(_ context.Context, _ string) ([]accounts.DiscoveredAccount, error) {
	return f.accounts, nil
}

type fakeEntries struct {
	entry login.Entry
	ok    bool
}

func (f *fakeEntries) Entry(_ string) (login.Entry, bool) {
	return f.entry, f.ok
}

func newTestBridge(t *testing.T, hosts HostDiscoverer, entries LoginEntries) (AccountBridge, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	acct := accounts.NewService(st.DB())
	loginSvc := login.NewService([]string{"work.example.com"})
	var opened string
	bridge := NewAccountBridge(acct, loginSvc, hosts, entries, func(u string) error {
		opened = u
		return nil
	})
	return bridge, opened
}

func TestBridgeDiscoverThenList(t *testing.T) {
	hosts := &fakeDiscoverer{
		accounts: []accounts.DiscoveredAccount{
			{ID: "u1", DisplayName: "User One", MaskedIdentity: "****1234",
				SourceID: "s1", SourceKind: accounts.KindManaged},
		},
	}
	bridge, _ := newTestBridge(t, hosts, &fakeEntries{})

	if err := bridge.Discover(context.Background(), "workbuddy-cn"); err != nil {
		t.Fatalf("Discover 失败: %v", err)
	}
	list, err := bridge.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "u1" {
		t.Fatalf("发现后应列出 1 个账号，实际 %+v", list)
	}
}

func TestBridgeBeginLoginOpensOfficialURL(t *testing.T) {
	entries := &fakeEntries{
		ok: true,
		entry: login.Entry{
			Target: "workbuddy-cn", Kind: login.KindHTTPS,
			Location: "https://work.example.com/login", TimeoutSec: 120,
		},
	}
	dir := t.TempDir()
	st, _ := store.Open(dir)
	acct := accounts.NewService(st.DB())
	loginSvc := login.NewService([]string{"work.example.com"})
	var opened string
	bridge := NewAccountBridge(acct, loginSvc, &fakeDiscoverer{}, entries, func(u string) error {
		opened = u
		return nil
	})

	if err := bridge.BeginLogin(context.Background(), "workbuddy-cn"); err != nil {
		t.Fatalf("BeginLogin 失败: %v", err)
	}
	if opened != "https://work.example.com/login" {
		t.Errorf("应打开官方登录 URL，实际 %s", opened)
	}
}

func TestBridgeBeginLoginMissingEntry(t *testing.T) {
	bridge, _ := newTestBridge(t, &fakeDiscoverer{}, &fakeEntries{ok: false})
	if err := bridge.BeginLogin(context.Background(), "nope"); err != ErrNoOfficialEntry {
		t.Fatalf("无官方入口应返回 ErrNoOfficialEntry，实际 %v", err)
	}
}
