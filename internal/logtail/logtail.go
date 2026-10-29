// Package logtail 提供基于游标、有界、可筛选的日志查询（log_tail），
// 以及 30 天日志滚动清理。
package logtail

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

const (
	// DefaultLimit 是未指定条数时的默认值。
	DefaultLimit = 50
	// MaxLimit 是单次请求允许的最大条数。
	MaxLimit = 200
	// Retention 是日志保留时长。
	Retention = 30 * 24 * time.Hour
)

var (
	// ErrLimitTooLarge 表示请求条数超过允许上限。
	ErrLimitTooLarge = errors.New("logtail: 请求条数超过允许上限")
)

// Filter 是日志筛选条件（目标应用/账号/操作/状态）。
type Filter struct {
	Target    string
	AccountID string
	Operation string
	State     string
}

// Entry 是一行日志（一行一事件）。
type Entry struct {
	ID        int64  `json:"id"`
	TS        string `json:"ts"`
	Target    string `json:"target"`
	AccountID string `json:"account_id"`
	Operation string `json:"operation"`
	State     string `json:"state"`
	Level     string `json:"level"`
	Message   string `json:"message"`
}

// Page 是一页日志。
type Page struct {
	Entries    []Entry `json:"entries"`
	NextCursor int64   `json:"next_cursor"`
}

// Tail 基于游标查询日志；limit 有界，filter 按四类筛选，按 id 正序。
func Tail(ctx context.Context, db *sql.DB, filter Filter, limit int, cursor int64) (*Page, error) {
	if limit <= 0 {
		limit = DefaultLimit
	}
	if limit > MaxLimit {
		return nil, ErrLimitTooLarge
	}

	var where []string
	var args []any
	if cursor > 0 {
		where = append(where, "id > ?")
		args = append(args, cursor)
	}
	if filter.Target != "" {
		where = append(where, "target = ?")
		args = append(args, filter.Target)
	}
	if filter.AccountID != "" {
		where = append(where, "account_id = ?")
		args = append(args, filter.AccountID)
	}
	if filter.Operation != "" {
		where = append(where, "operation = ?")
		args = append(args, filter.Operation)
	}
	if filter.State != "" {
		where = append(where, "state = ?")
		args = append(args, filter.State)
	}

	query := "SELECT id, ts, target, account_id, operation, state, level, message FROM logs"
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY id ASC LIMIT ?"
	args = append(args, limit)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	page := &Page{Entries: []Entry{}}
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.TS, &e.Target, &e.AccountID,
			&e.Operation, &e.State, &e.Level, &e.Message); err != nil {
			return nil, err
		}
		page.Entries = append(page.Entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 取满 limit 才可能有下一页，游标为最后一条 id。
	if len(page.Entries) == limit {
		page.NextCursor = page.Entries[len(page.Entries)-1].ID
	}
	return page, nil
}

// Prune 删除超过保留期（30 天）的日志。
func Prune(ctx context.Context, db *sql.DB, now time.Time) error {
	cutoff := now.Add(-Retention).UTC().Format(time.RFC3339)
	_, err := db.ExecContext(ctx, "DELETE FROM logs WHERE ts < ?", cutoff)
	return err
}
