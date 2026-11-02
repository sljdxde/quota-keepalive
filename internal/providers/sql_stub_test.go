package providers

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
)

const coverageDriverName = "qka-provider-coverage"

var (
	coverageDriverID      atomic.Uint64
	coverageDriverConfigs sync.Map
)

func init() {
	sql.Register(coverageDriverName, coverageDriver{})
}

type coverageDBConfig struct {
	query    func(string) (driver.Rows, error)
	exec     func(string) (driver.Result, error)
	beginErr error
}

type coverageDriver struct{}

func (coverageDriver) Open(name string) (driver.Conn, error) {
	value, ok := coverageDriverConfigs.Load(name)
	if !ok {
		return nil, fmt.Errorf("coverage driver config %q not found", name)
	}
	return &coverageConn{cfg: value.(*coverageDBConfig)}, nil
}

type coverageConn struct {
	cfg *coverageDBConfig
}

func (c *coverageConn) Prepare(query string) (driver.Stmt, error) {
	return &coverageStmt{cfg: c.cfg, query: query}, nil
}

func (c *coverageConn) Close() error { return nil }

func (c *coverageConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

func (c *coverageConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	if c.cfg.beginErr != nil {
		return nil, c.cfg.beginErr
	}
	return coverageTx{}, nil
}

func (c *coverageConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	if c.cfg.query != nil {
		return c.cfg.query(query)
	}
	return &coverageRows{columns: []string{"value"}}, nil
}

func (c *coverageConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	if c.cfg.exec != nil {
		return c.cfg.exec(query)
	}
	return coverageResult{affected: 1}, nil
}

type coverageStmt struct {
	cfg   *coverageDBConfig
	query string
}

func (s *coverageStmt) Close() error  { return nil }
func (s *coverageStmt) NumInput() int { return -1 }

func (s *coverageStmt) Exec([]driver.Value) (driver.Result, error) {
	if s.cfg.exec != nil {
		return s.cfg.exec(s.query)
	}
	return coverageResult{affected: 1}, nil
}

func (s *coverageStmt) Query([]driver.Value) (driver.Rows, error) {
	if s.cfg.query != nil {
		return s.cfg.query(s.query)
	}
	return &coverageRows{columns: []string{"value"}}, nil
}

type coverageRows struct {
	columns []string
	values  [][]driver.Value
	nextErr error
	index   int
}

func (r *coverageRows) Columns() []string { return r.columns }
func (r *coverageRows) Close() error      { return nil }

func (r *coverageRows) Next(dest []driver.Value) error {
	if r.index < len(r.values) {
		copy(dest, r.values[r.index])
		r.index++
		return nil
	}
	if r.nextErr != nil {
		err := r.nextErr
		r.nextErr = nil
		return err
	}
	return io.EOF
}

type coverageResult struct {
	affected    int64
	affectedErr error
}

func (r coverageResult) LastInsertId() (int64, error) { return 0, nil }
func (r coverageResult) RowsAffected() (int64, error) { return r.affected, r.affectedErr }

type coverageTx struct{}

func (coverageTx) Commit() error   { return nil }
func (coverageTx) Rollback() error { return nil }

func openCoverageDB(t *testing.T, cfg *coverageDBConfig) *sql.DB {
	t.Helper()
	name := t.Name() + "-" + strconv.FormatUint(coverageDriverID.Add(1), 10)
	coverageDriverConfigs.Store(name, cfg)
	db, err := sql.Open(coverageDriverName, name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		coverageDriverConfigs.Delete(name)
	})
	return db
}
