package accounts

import (
	"context"
	"testing"
)

func TestDiscoverDefaultsAndInsertErrors(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()
	if err := svc.Discover(ctx, "qoder-cn", []DiscoveredAccount{{
		ID: "default", SourceID: "s_default",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Discover(ctx, "qoder-cn", []DiscoveredAccount{{
		ID: "imported", SourceID: "s_imported", SourceKind: KindImported,
	}}); err != nil {
		t.Fatal(err)
	}
	acc, err := svc.Get(ctx, "qoder-cn", "imported")
	if err != nil || !acc.Sources[0].ReadOnly || acc.Sources[0].Kind != KindImported {
		t.Fatalf("导入来源默认应只读，实际 %+v %v", acc, err)
	}

	if _, err := svc.db.Exec(`DROP TABLE accounts`); err != nil {
		t.Fatal(err)
	}
	if err := svc.Discover(ctx, "qoder-cn", discovered()[:1]); err == nil {
		t.Fatal("账号写入失败应返回")
	}

	svc = newService(t)
	if _, err := svc.db.Exec(`DROP TABLE sources`); err != nil {
		t.Fatal(err)
	}
	if err := svc.Discover(ctx, "qoder-cn", discovered()[:1]); err == nil {
		t.Fatal("来源写入失败应返回")
	}

	svc = newService(t)
	if err := svc.db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := svc.Discover(ctx, "qoder-cn", nil); err == nil {
		t.Fatal("事务开始失败应返回")
	}
}

func TestSetEnabledAndTaskErrors(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()
	if err := svc.Discover(ctx, "qoder-cn", discovered()[:1]); err != nil {
		t.Fatal(err)
	}
	if err := svc.db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetEnabled(ctx, "qoder-cn", "q_1", false); err == nil {
		t.Fatal("关闭数据库后更新应失败")
	}

	svc = newService(t)
	if err := svc.Discover(ctx, "qoder-cn", discovered()[:1]); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetTaskEnabled(ctx, "qoder-cn", "q_1", "", true); err == nil {
		t.Fatal("空操作类型应拒绝")
	}
	if err := svc.SetTaskEnabled(ctx, "qoder-cn", "missing", "checkin", true); err == nil {
		t.Fatal("不存在账号应拒绝")
	}
	if _, err := svc.db.Exec(`DROP TABLE account_task_preferences`); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetTaskEnabled(ctx, "qoder-cn", "q_1", "checkin", true); err == nil {
		t.Fatal("偏好写入失败应返回")
	}
	if _, err := svc.TaskPreferences(ctx, "qoder-cn", "q_1"); err == nil {
		t.Fatal("偏好查询失败应返回")
	}
	if _, err := svc.db.Exec(`CREATE TABLE account_task_preferences (
		operation TEXT, enabled INTEGER, target TEXT, account_id TEXT,
		created_at TEXT, updated_at TEXT
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`INSERT INTO account_task_preferences VALUES (NULL,1,'qoder-cn','q_1','t','t')`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.TaskPreferences(ctx, "qoder-cn", "q_1"); err == nil {
		t.Fatal("偏好 Scan 失败应返回")
	}
}

func TestListGetSourceErrorBranches(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		mutate func(*Service)
		call   func(*Service) error
	}{
		{"list query", func(s *Service) { _, _ = s.db.Exec(`DROP TABLE accounts`) },
			func(s *Service) error { _, err := s.List(ctx, ""); return err }},
		{"list scan", func(s *Service) {
			_, _ = s.db.Exec(`DROP TABLE accounts`)
			_, _ = s.db.Exec(`CREATE TABLE accounts (target TEXT,id TEXT,display_name TEXT,masked_identity TEXT,enabled INTEGER)`)
			_, _ = s.db.Exec(`INSERT INTO accounts VALUES (NULL,'x','x','x',1)`)
		}, func(s *Service) error { _, err := s.List(ctx, ""); return err }},
		{"list sources", func(s *Service) { _, _ = s.db.Exec(`DROP TABLE sources`) },
			func(s *Service) error { _, err := s.List(ctx, ""); return err }},
		{"get sources", func(s *Service) { _, _ = s.db.Exec(`DROP TABLE sources`) },
			func(s *Service) error { _, err := s.Get(ctx, "qoder-cn", "q_1"); return err }},
		{"preferred sources", func(s *Service) { _, _ = s.db.Exec(`DROP TABLE sources`) },
			func(s *Service) error { _, err := s.PreferredSource(ctx, "qoder-cn", "q_1"); return err }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newService(t)
			if err := svc.Discover(ctx, "qoder-cn", discovered()[:1]); err != nil {
				t.Fatal(err)
			}
			tc.mutate(svc)
			if err := tc.call(svc); err == nil {
				t.Fatal("数据库错误应返回")
			}
		})
	}
}

func TestLoadSourcesScanAndDeleteError(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()
	if _, err := svc.db.Exec(`DROP TABLE sources`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`CREATE TABLE sources (
		id TEXT, kind TEXT, read_only INTEGER, health TEXT, pinned INTEGER,
		target TEXT, account_id TEXT, created_at TEXT, updated_at TEXT,
		credential_ref TEXT
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`INSERT INTO sources VALUES (NULL,'managed',0,'healthy',0,'qoder-cn','q_1','t','t','')`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.loadSources(ctx, "qoder-cn", "q_1"); err == nil {
		t.Fatal("来源 Scan 错误应返回")
	}
	if err := svc.Delete(ctx, "qoder-cn", "q_1"); err == nil {
		t.Fatal("删除前读取来源失败应返回")
	}
}
