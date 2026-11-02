package registry

import (
	"context"
	"testing"
)

func TestNewWithNilClockAndEmptyList(t *testing.T) {
	reg := New(nil, nil)
	list, err := reg.List(context.Background())
	if err != nil || list != nil {
		t.Fatalf("空注册表应成功返回空列表，实际 %+v %v", list, err)
	}
}

func TestListPropagatesDatabaseErrors(t *testing.T) {
	reg, st := newRegistry(t)
	reg.SetAdapters([]LoadedAdapter{{Manifest: sampleManifest()}})
	if _, err := st.DB().Exec(`DROP TABLE accounts`); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.List(context.Background()); err == nil {
		t.Fatal("账号查询失败应返回")
	}
	if _, err := reg.AgentsList(context.Background()); err == nil {
		t.Fatal("AgentsList 应传播 List 错误")
	}

	reg, st = newRegistry(t)
	reg.SetAdapters([]LoadedAdapter{{Manifest: sampleManifest()}})
	if _, err := st.DB().Exec(`DROP TABLE logs`); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.List(context.Background()); err == nil {
		t.Fatal("最近调用查询失败应返回")
	}
}

func TestRegistryScanAndSourceQueryErrors(t *testing.T) {
	reg, st := newRegistry(t)
	if _, err := st.DB().Exec(`DROP TABLE accounts`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`CREATE TABLE accounts (
		id TEXT, target TEXT, display_name TEXT, masked_identity TEXT, enabled INTEGER
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`INSERT INTO accounts VALUES (NULL, 'workbuddy-cn', '', '', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.loadAccounts(context.Background(), "workbuddy-cn"); err == nil {
		t.Fatal("账号 Scan 错误应返回")
	}

	reg, st = newRegistry(t)
	seedAccount(t, st, "workbuddy-cn", "u_1", true)
	if _, err := st.DB().Exec(`DROP TABLE sources`); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.loadAccounts(context.Background(), "workbuddy-cn"); err == nil {
		t.Fatal("加载账号时来源查询错误应返回")
	}
	if _, err := reg.loadSources(context.Background(), "workbuddy-cn", "u_1"); err == nil {
		t.Fatal("来源查询错误应返回")
	}

	if _, err := st.DB().Exec(`CREATE TABLE sources (
		id TEXT, target TEXT, account_id TEXT, kind TEXT, read_only INTEGER,
		health TEXT, pinned INTEGER
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`INSERT INTO sources VALUES (NULL, 'workbuddy-cn', 'u_1', '', 0, '', 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.loadSources(context.Background(), "workbuddy-cn", "u_1"); err == nil {
		t.Fatal("来源 Scan 错误应返回")
	}
}
