package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// HealthReport 是数据库健康检查的机读结果。
type HealthReport struct {
	Ok            bool              `json:"ok"`
	SchemaVersion int               `json:"schema_version"`
	Checks        map[string]string `json:"checks"`
}

var healthTables = []string{"accounts", "sources", "daily_ops", "logs"}

// HealthCheck 验证迁移版本、完整性与关键表是否可查询。
func (s *Store) HealthCheck(ctx context.Context) (HealthReport, error) {
	checks := map[string]string{}
	healthy := true

	version, err := s.SchemaVersion(ctx)
	if err != nil {
		return HealthReport{}, err
	}
	if version == latestVersion {
		checks["schema_version"] = "ok"
	} else {
		checks["schema_version"] = fmt.Sprintf("stale:%d", version)
		healthy = false
	}

	var integrity string
	if err := s.db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil {
		return HealthReport{}, err
	}
	if integrity == "ok" {
		checks["integrity"] = "ok"
	} else {
		checks["integrity"] = integrity
		healthy = false
	}

	for _, table := range healthTables {
		var one int
		err := s.db.QueryRowContext(ctx, `SELECT 1 FROM `+table+` LIMIT 1`).Scan(&one)
		// 空表返回 ErrNoRows 是正常的：表存在且可查询。
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			checks["table:"+table] = err.Error()
			healthy = false
		} else {
			checks["table:"+table] = "ok"
		}
	}

	return HealthReport{Ok: healthy, SchemaVersion: version, Checks: checks}, nil
}
