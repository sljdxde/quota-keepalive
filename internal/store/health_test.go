package store

import (
	"context"
	"testing"
)

func TestHealthCheckPassesOnFreshDatabase(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer s.Close()

	report, err := s.HealthCheck(context.Background())
	if err != nil {
		t.Fatalf("健康检查失败: %v", err)
	}
	if !report.Ok {
		t.Errorf("新数据库应健康，检查明细: %+v", report.Checks)
	}
	if report.SchemaVersion != latestVersion {
		t.Errorf("健康报告版本期望 %d，实际 %d", latestVersion, report.SchemaVersion)
	}
	if report.Checks["integrity"] != "ok" {
		t.Errorf("integrity_check 期望 ok，实际 %q", report.Checks["integrity"])
	}
}

func TestHealthCheckVerifiesAllTablesQueryable(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer s.Close()

	report, err := s.HealthCheck(context.Background())
	if err != nil {
		t.Fatalf("健康检查失败: %v", err)
	}
	for _, table := range []string{"accounts", "sources", "daily_ops", "logs"} {
		if report.Checks["table:"+table] != "ok" {
			t.Errorf("表 %s 应可查询，实际 %q", table, report.Checks["table:"+table])
		}
	}
}
