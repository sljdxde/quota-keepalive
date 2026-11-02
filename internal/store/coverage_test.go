package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenFailureBranches(t *testing.T) {
	t.Run("ensure directory", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(dir, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(dir); err == nil {
			t.Fatal("expected EnsureDir failure")
		}
	})

	t.Run("sql open", func(t *testing.T) {
		want := errors.New("sql open failed")
		old := sqlOpen
		sqlOpen = func(string, string) (*sql.DB, error) {
			return nil, want
		}
		t.Cleanup(func() { sqlOpen = old })
		if _, err := Open(t.TempDir()); !errors.Is(err, want) {
			t.Fatalf("Open error = %v, want %v", err, want)
		}
	})

	t.Run("ping", func(t *testing.T) {
		config := t.TempDir()
		if err := os.Mkdir(filepath.Join(config, "qka.db"), 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(config); err == nil {
			t.Fatal("expected ping failure for database directory")
		}
	})

	t.Run("migrate", func(t *testing.T) {
		config := t.TempDir()
		db, err := sql.Open("sqlite", filepath.Join(config, "qka.db"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`CREATE TABLE schema_migrations (applied_at TEXT NOT NULL)`); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(config); err == nil {
			t.Fatal("expected migration failure for incompatible schema_migrations")
		}
	})

	t.Run("chmod", func(t *testing.T) {
		want := errors.New("chmod failed")
		old := chmodPath
		chmodPath = func(string, os.FileMode) error {
			return want
		}
		t.Cleanup(func() { chmodPath = old })
		if _, err := Open(t.TempDir()); !errors.Is(err, want) {
			t.Fatalf("Open error = %v, want %v", err, want)
		}
	})
}

func TestStoreAccessors(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.ConfigDir() == "" {
		t.Fatal("ConfigDir should return the opened directory")
	}
	if s.DB() == nil {
		t.Fatal("DB should return the database handle")
	}
}

func TestHealthCheckFailureBranches(t *testing.T) {
	t.Run("schema version query", func(t *testing.T) {
		s, err := Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := s.HealthCheck(context.Background()); err == nil {
			t.Fatal("expected schema version query failure")
		}
	})

	t.Run("stale schema", func(t *testing.T) {
		s, err := Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version > 1`); err != nil {
			t.Fatal(err)
		}
		report, err := s.HealthCheck(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if report.Ok || report.Checks["schema_version"] != "stale:1" {
			t.Fatalf("stale report = %+v", report)
		}
	})

	t.Run("table query", func(t *testing.T) {
		s, err := Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if _, err := s.db.Exec(`DROP TABLE logs`); err != nil {
			t.Fatal(err)
		}
		report, err := s.HealthCheck(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if report.Ok || report.Checks["table:logs"] == "ok" {
			t.Fatalf("missing table should fail health check: %+v", report)
		}
	})

	t.Run("integrity query", func(t *testing.T) {
		want := errors.New("integrity failed")
		db := newStoreScriptDB(
			t,
			nil,
			func(call int, _ string) (driver.Rows, error) {
				if call == 1 {
					return &singleRows{value: int64(latestVersion)}, nil
				}
				return nil, want
			},
			nil,
		)
		s := &Store{db: db}
		if _, err := s.HealthCheck(context.Background()); !errors.Is(err, want) {
			t.Fatalf("HealthCheck error = %v, want %v", err, want)
		}
	})

	t.Run("integrity value", func(t *testing.T) {
		db := newStoreScriptDB(
			t,
			nil,
			func(call int, _ string) (driver.Rows, error) {
				if call == 1 {
					return &singleRows{value: int64(latestVersion)}, nil
				}
				if call == 2 {
					return &singleRows{value: "corrupt"}, nil
				}
				return &emptyStoreRows{}, nil
			},
			nil,
		)
		s := &Store{db: db}
		report, err := s.HealthCheck(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if report.Ok || report.Checks["integrity"] != "corrupt" {
			t.Fatalf("integrity report = %+v", report)
		}
	})
}

func TestMigrateFailureBranches(t *testing.T) {
	want := errors.New("scripted database failure")

	t.Run("ensure migrations table", func(t *testing.T) {
		db := newStoreScriptDB(
			t,
			func(int, string) (driver.Result, error) { return nil, want },
			nil,
			nil,
		)
		if err := (&Store{db: db}).migrate(context.Background()); !errors.Is(err, want) {
			t.Fatalf("migrate error = %v, want %v", err, want)
		}
	})

	t.Run("version query", func(t *testing.T) {
		db := newStoreScriptDB(
			t,
			nil,
			func(int, string) (driver.Rows, error) { return nil, want },
			nil,
		)
		if err := (&Store{db: db}).migrate(context.Background()); !errors.Is(err, want) {
			t.Fatalf("migrate error = %v, want %v", err, want)
		}
	})

	t.Run("begin transaction", func(t *testing.T) {
		db := newStoreScriptDB(
			t,
			nil,
			func(int, string) (driver.Rows, error) { return &singleRows{value: int64(0)}, nil },
			func() (driver.Tx, error) { return nil, want },
		)
		if err := (&Store{db: db}).migrate(context.Background()); !errors.Is(err, want) {
			t.Fatalf("migrate error = %v, want %v", err, want)
		}
	})

	t.Run("migration statement", func(t *testing.T) {
		tx := &scriptStoreTx{}
		db := newStoreScriptDB(
			t,
			func(call int, _ string) (driver.Result, error) {
				if call == 1 {
					return storeScriptResult{}, nil
				}
				return nil, want
			},
			func(int, string) (driver.Rows, error) { return &singleRows{value: int64(0)}, nil },
			func() (driver.Tx, error) { return tx, nil },
		)
		if err := (&Store{db: db}).migrate(context.Background()); !errors.Is(err, want) {
			t.Fatalf("migrate error = %v, want %v", err, want)
		}
		if !tx.rolledBack {
			t.Fatal("failed migration should roll back")
		}
	})

	t.Run("migration record", func(t *testing.T) {
		tx := &scriptStoreTx{}
		db := newStoreScriptDB(
			t,
			func(call int, _ string) (driver.Result, error) {
				if call <= 2 {
					return storeScriptResult{}, nil
				}
				return nil, want
			},
			func(int, string) (driver.Rows, error) { return &singleRows{value: int64(0)}, nil },
			func() (driver.Tx, error) { return tx, nil },
		)
		if err := (&Store{db: db}).migrate(context.Background()); !errors.Is(err, want) {
			t.Fatalf("migrate error = %v, want %v", err, want)
		}
		if !tx.rolledBack {
			t.Fatal("failed migration record should roll back")
		}
	})

	t.Run("commit", func(t *testing.T) {
		tx := &scriptStoreTx{commitErr: want}
		db := newStoreScriptDB(
			t,
			nil,
			func(int, string) (driver.Rows, error) { return &singleRows{value: int64(0)}, nil },
			func() (driver.Tx, error) { return tx, nil },
		)
		if err := (&Store{db: db}).migrate(context.Background()); !errors.Is(err, want) {
			t.Fatalf("migrate error = %v, want %v", err, want)
		}
	})
}

type scriptStoreConnector struct {
	conn *scriptStoreConn
}

func (c scriptStoreConnector) Connect(context.Context) (driver.Conn, error) {
	return c.conn, nil
}

func (scriptStoreConnector) Driver() driver.Driver {
	return scriptStoreDriver{}
}

type scriptStoreDriver struct{}

func (scriptStoreDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("script driver requires OpenDB")
}

type scriptStoreConn struct {
	execFn  func(int, string) (driver.Result, error)
	queryFn func(int, string) (driver.Rows, error)
	beginFn func() (driver.Tx, error)
	execs   int
	queries int
}

func (c *scriptStoreConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is not supported")
}

func (c *scriptStoreConn) Close() error {
	return nil
}

func (c *scriptStoreConn) Begin() (driver.Tx, error) {
	if c.beginFn == nil {
		return nil, errors.New("transactions are not supported")
	}
	return c.beginFn()
}

func (c *scriptStoreConn) ExecContext(
	context.Context,
	string,
	[]driver.NamedValue,
) (driver.Result, error) {
	c.execs++
	if c.execFn == nil {
		return storeScriptResult{}, nil
	}
	return c.execFn(c.execs, "")
}

func (c *scriptStoreConn) QueryContext(
	context.Context,
	string,
	[]driver.NamedValue,
) (driver.Rows, error) {
	c.queries++
	if c.queryFn == nil {
		return &singleRows{value: int64(0)}, nil
	}
	return c.queryFn(c.queries, "")
}

type scriptStoreTx struct {
	execFn     func(int) (driver.Result, error)
	commitErr  error
	execs      int
	rolledBack bool
}

func (t *scriptStoreTx) Commit() error {
	return t.commitErr
}

func (t *scriptStoreTx) Rollback() error {
	t.rolledBack = true
	return nil
}

func (t *scriptStoreTx) ExecContext(
	context.Context,
	string,
	[]driver.NamedValue,
) (driver.Result, error) {
	t.execs++
	if t.execFn == nil {
		return storeScriptResult{}, nil
	}
	return t.execFn(t.execs)
}

type storeScriptResult struct{}

func (storeScriptResult) LastInsertId() (int64, error) {
	return 0, nil
}

func (storeScriptResult) RowsAffected() (int64, error) {
	return 1, nil
}

type singleRows struct {
	value driver.Value
	read  bool
}

func (*singleRows) Columns() []string {
	return []string{"value"}
}

func (*singleRows) Close() error {
	return nil
}

func (r *singleRows) Next(dest []driver.Value) error {
	if r.read {
		return io.EOF
	}
	r.read = true
	dest[0] = r.value
	return nil
}

type emptyStoreRows struct{}

func (*emptyStoreRows) Columns() []string {
	return []string{"value"}
}

func (*emptyStoreRows) Close() error {
	return nil
}

func (*emptyStoreRows) Next([]driver.Value) error {
	return io.EOF
}

func newStoreScriptDB(
	t *testing.T,
	execFn func(int, string) (driver.Result, error),
	queryFn func(int, string) (driver.Rows, error),
	beginFn func() (driver.Tx, error),
) *sql.DB {
	t.Helper()
	db := sql.OpenDB(scriptStoreConnector{conn: &scriptStoreConn{
		execFn:  execFn,
		queryFn: queryFn,
		beginFn: beginFn,
	}})
	t.Cleanup(func() { _ = db.Close() })
	return db
}
