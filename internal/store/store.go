// Package store 管理配置目录下的 SQLite：账号、登录态来源、每日操作（含执行租约）与日志的唯一真源。
package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"

	"qka/internal/atomicfile"
)

var (
	sqlOpen   = sql.Open
	chmodPath = os.Chmod
)

// Store 包装一个配置目录对应的 SQLite 数据库。
type Store struct {
	db        *sql.DB
	configDir string
}

// Open 在配置目录打开（必要时创建）owner-only 的 SQLite，应用迁移并返回可用 Store。
// pragma 通过 DSN 下发，确保连接池中的每个连接都生效。
func Open(configDir string) (*Store, error) {
	if err := atomicfile.EnsureDir(configDir, 0o700); err != nil {
		return nil, err
	}
	dbPath := filepath.Join(configDir, "qka.db")

	dsn := fmt.Sprintf(
		"file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)",
		dbPath,
	)

	db, err := sqlOpen("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	s := &Store{db: db, configDir: configDir}

	ctx := context.Background()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}

	// 显式收敛数据库文件权限，规避 umask 导致的过宽权限。
	if err := chmodPath(dbPath, 0o600); err != nil {
		_ = db.Close()
		return nil, err
	}

	return s, nil
}

// Close 关闭数据库连接。
func (s *Store) Close() error {
	return s.db.Close()
}

// ConfigDir 返回该 Store 对应的配置目录。
func (s *Store) ConfigDir() string {
	return s.configDir
}

// DB 返回底层数据库连接，供业务包（引擎等）执行其领域 SQL。
func (s *Store) DB() *sql.DB {
	return s.db
}
