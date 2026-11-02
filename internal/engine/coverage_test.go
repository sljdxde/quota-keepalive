package engine

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"testing"
	"time"
)

func TestEngineSmallHelpers(t *testing.T) {
	e := New(nil, nil, "")
	if e.clock == nil {
		t.Fatal("nil clock should default to time.Now")
	}
	if got := e.WithLeaseTTL(time.Second); got != e || e.leaseTTL != time.Second {
		t.Fatalf("WithLeaseTTL should mutate and return the engine: got=%p want=%p ttl=%v",
			got, e, e.leaseTTL)
	}
	if got := Transition(Checkin, RManualRequired, morning()); got != ManualRequired {
		t.Fatalf("manual-required transition = %s, want %s", got, ManualRequired)
	}
	if got := Transition(Operation("unknown"), RSuccess, morning()); got != Pending {
		t.Fatalf("unknown operation transition = %s, want %s", got, Pending)
	}
}

func TestCurrentStateReconcileAndUsage(t *testing.T) {
	ctx := context.Background()
	e := newTestEngine(t, fixedClock(afternoon()))

	state, err := e.CurrentState(ctx, checkinKey)
	if err != nil {
		t.Fatal(err)
	}
	if state != Pending {
		t.Fatalf("missing row state = %s, want pending", state)
	}

	if err := e.ensureRow(ctx, checkinKey, BeijingDay(afternoon()), formatTS(afternoon())); err != nil {
		t.Fatal(err)
	}
	if err := e.ReconcileState(ctx, checkinKey, Already, "confirmed already"); err != nil {
		t.Fatal(err)
	}
	state, err = e.CurrentState(ctx, checkinKey)
	if err != nil {
		t.Fatal(err)
	}
	if state != Already {
		t.Fatalf("reconciled state = %s, want already", state)
	}

	applied, err := e.ReconcileStateIfCurrent(ctx, checkinKey, Already, Inactive, "closed")
	if err != nil {
		t.Fatal(err)
	}
	if !applied {
		t.Fatal("matching state should be reconciled")
	}
	applied, err = e.ReconcileStateIfCurrent(ctx, checkinKey, Already, Inactive, "stale")
	if err != nil {
		t.Fatal(err)
	}
	if applied {
		t.Fatal("stale expected state must not overwrite the current state")
	}

	if err := e.RecordUsage(ctx, keepaliveKey(), nil, nil); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM daily_ops WHERE operation = 'keepalive'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("nil usage should not create a row, got %d", count)
	}

	zero := 0.0
	if err := e.RecordUsage(ctx, keepaliveKey(), &zero, nil); err != nil {
		t.Fatal(err)
	}
	balance := 42.5
	if err := e.RecordUsage(ctx, keepaliveKey(), nil, &balance); err != nil {
		t.Fatal(err)
	}
	credits := 7.25
	balance = 99.5
	if err := e.RecordUsage(ctx, keepaliveKey(), &credits, &balance); err != nil {
		t.Fatal(err)
	}
	var gotCredits, gotBalance float64
	var creditsKnown, balanceKnown int
	if err := e.db.QueryRow(`
SELECT credits, credits_known, balance, balance_known
FROM daily_ops
WHERE target = ? AND account_id = ? AND operation = ? AND day_key = ?`,
		keepaliveKey().Target, keepaliveKey().AccountID, string(Keepalive),
		BeijingDay(afternoon())).Scan(
		&gotCredits, &creditsKnown, &gotBalance, &balanceKnown); err != nil {
		t.Fatal(err)
	}
	if gotCredits != credits || creditsKnown != 1 {
		t.Fatalf("credits = %v known=%d, want %v known=1", gotCredits, creditsKnown, credits)
	}
	if gotBalance != balance || balanceKnown != 1 {
		t.Fatalf("balance = %v known=%d, want %v known=1", gotBalance, balanceKnown, balance)
	}
}

func TestRunExecutorFailureBecomesRetryable(t *testing.T) {
	e := newTestEngine(t, fixedClock(morning()))
	wantErr := errors.New("upstream unavailable")
	obs, err := e.Run(context.Background(), keepaliveKey(), func(context.Context, Key) (ExternalResult, error) {
		return ExternalResult{}, wantErr
	})
	if err != nil {
		t.Fatal(err)
	}
	if obs.State != FailedRetryable || obs.Message != wantErr.Error() {
		t.Fatalf("executor error should become retryable: %+v", obs)
	}
}

func TestEngineDatabaseErrorPaths(t *testing.T) {
	ctx := context.Background()
	execErr := errors.New("exec failed")
	rowsErr := errors.New("rows affected failed")
	queryErr := errors.New("query failed")

	t.Run("acquire exec", func(t *testing.T) {
		db := newScriptDB(t, func(int, string, []driver.NamedValue) (driver.Result, error) {
			return nil, execErr
		}, nil)
		_, err := New(db, fixedClock(morning()), "h").acquire(
			ctx, checkinKey, BeijingDay(morning()), morning())
		if !errors.Is(err, execErr) {
			t.Fatalf("acquire error = %v, want %v", err, execErr)
		}
	})

	t.Run("acquire rows affected", func(t *testing.T) {
		db := newScriptDB(t, func(int, string, []driver.NamedValue) (driver.Result, error) {
			return scriptResult(0, rowsErr), nil
		}, nil)
		_, err := New(db, fixedClock(morning()), "h").acquire(
			ctx, checkinKey, BeijingDay(morning()), morning())
		if !errors.Is(err, rowsErr) {
			t.Fatalf("acquire rows error = %v, want %v", err, rowsErr)
		}
	})

	t.Run("current state query", func(t *testing.T) {
		db := newScriptDB(t, nil, func(string, []driver.NamedValue) (driver.Rows, error) {
			return nil, queryErr
		})
		_, err := New(db, fixedClock(morning()), "h").CurrentState(ctx, checkinKey)
		if !errors.Is(err, queryErr) {
			t.Fatalf("query error = %v, want %v", err, queryErr)
		}
	})

	t.Run("reconcile exec", func(t *testing.T) {
		db := newScriptDB(t, func(int, string, []driver.NamedValue) (driver.Result, error) {
			return nil, execErr
		}, nil)
		err := New(db, fixedClock(morning()), "h").ReconcileState(ctx, checkinKey, Already, "")
		if !errors.Is(err, execErr) {
			t.Fatalf("reconcile error = %v, want %v", err, execErr)
		}
	})

	t.Run("conditional reconcile exec", func(t *testing.T) {
		db := newScriptDB(t, func(int, string, []driver.NamedValue) (driver.Result, error) {
			return nil, execErr
		}, nil)
		_, err := New(db, fixedClock(morning()), "h").ReconcileStateIfCurrent(
			ctx, checkinKey, Pending, Already, "")
		if !errors.Is(err, execErr) {
			t.Fatalf("conditional reconcile error = %v, want %v", err, execErr)
		}
	})

	t.Run("conditional reconcile rows affected", func(t *testing.T) {
		db := newScriptDB(t, func(int, string, []driver.NamedValue) (driver.Result, error) {
			return scriptResult(0, rowsErr), nil
		}, nil)
		_, err := New(db, fixedClock(morning()), "h").ReconcileStateIfCurrent(
			ctx, checkinKey, Pending, Already, "")
		if !errors.Is(err, rowsErr) {
			t.Fatalf("conditional reconcile rows error = %v, want %v", err, rowsErr)
		}
	})

	t.Run("record usage ensure", func(t *testing.T) {
		db := newScriptDB(t, func(int, string, []driver.NamedValue) (driver.Result, error) {
			return nil, execErr
		}, nil)
		value := 1.0
		err := New(db, fixedClock(morning()), "h").RecordUsage(ctx, checkinKey, &value, nil)
		if !errors.Is(err, execErr) {
			t.Fatalf("record ensure error = %v, want %v", err, execErr)
		}
	})

	t.Run("record usage update", func(t *testing.T) {
		db := newScriptDB(t, func(call int, _ string, _ []driver.NamedValue) (driver.Result, error) {
			if call == 1 {
				return scriptResult(1, nil), nil
			}
			return nil, execErr
		}, nil)
		value := 1.0
		err := New(db, fixedClock(morning()), "h").RecordUsage(ctx, checkinKey, &value, nil)
		if !errors.Is(err, execErr) {
			t.Fatalf("record update error = %v, want %v", err, execErr)
		}
	})
}

func TestRunDatabaseErrorPaths(t *testing.T) {
	ctx := context.Background()
	execErr := errors.New("exec failed")
	queryErr := errors.New("query failed")

	t.Run("ensure row", func(t *testing.T) {
		db := newScriptDB(t, func(int, string, []driver.NamedValue) (driver.Result, error) {
			return nil, execErr
		}, nil)
		_, err := New(db, fixedClock(morning()), "h").Run(ctx, checkinKey, nil)
		if !errors.Is(err, execErr) {
			t.Fatalf("ensure error = %v, want %v", err, execErr)
		}
	})

	t.Run("acquire", func(t *testing.T) {
		db := newScriptDB(t, func(call int, _ string, _ []driver.NamedValue) (driver.Result, error) {
			if call == 1 {
				return scriptResult(1, nil), nil
			}
			return nil, execErr
		}, nil)
		_, err := New(db, fixedClock(morning()), "h").Run(ctx, checkinKey, nil)
		if !errors.Is(err, execErr) {
			t.Fatalf("acquire error = %v, want %v", err, execErr)
		}
	})

	t.Run("read state without lease", func(t *testing.T) {
		db := newScriptDB(t,
			func(call int, _ string, _ []driver.NamedValue) (driver.Result, error) {
				return scriptResult(0, nil), nil
			},
			func(string, []driver.NamedValue) (driver.Rows, error) {
				return nil, queryErr
			})
		_, err := New(db, fixedClock(morning()), "h").Run(ctx, checkinKey, nil)
		if !errors.Is(err, queryErr) {
			t.Fatalf("read state error = %v, want %v", err, queryErr)
		}
	})

	t.Run("complete", func(t *testing.T) {
		db := newScriptDB(t, func(call int, _ string, _ []driver.NamedValue) (driver.Result, error) {
			switch call {
			case 1, 2:
				return scriptResult(1, nil), nil
			default:
				return nil, execErr
			}
		}, nil)
		_, err := New(db, fixedClock(morning()), "h").Run(
			ctx, checkinKey, func(context.Context, Key) (ExternalResult, error) {
				return ExternalResult{Kind: RSuccess}, nil
			})
		if !errors.Is(err, execErr) {
			t.Fatalf("complete error = %v, want %v", err, execErr)
		}
	})
}

type scriptDriver struct{}

func (scriptDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("script driver requires OpenDB")
}

type scriptConnector struct {
	conn *scriptConn
}

func (c scriptConnector) Connect(context.Context) (driver.Conn, error) {
	return c.conn, nil
}

func (scriptConnector) Driver() driver.Driver {
	return scriptDriver{}
}

type scriptConn struct {
	execFn  func(int, string, []driver.NamedValue) (driver.Result, error)
	queryFn func(string, []driver.NamedValue) (driver.Rows, error)
	execs   int
}

func (c *scriptConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is not supported")
}

func (c *scriptConn) Close() error {
	return nil
}

func (c *scriptConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are not supported")
}

func (c *scriptConn) ExecContext(
	_ context.Context,
	query string,
	args []driver.NamedValue,
) (driver.Result, error) {
	c.execs++
	if c.execFn == nil {
		return scriptResult(1, nil), nil
	}
	return c.execFn(c.execs, query, args)
}

func (c *scriptConn) QueryContext(
	_ context.Context,
	query string,
	args []driver.NamedValue,
) (driver.Rows, error) {
	if c.queryFn == nil {
		return &emptyRows{}, nil
	}
	return c.queryFn(query, args)
}

type scriptResultValue struct {
	rows int64
	err  error
}

func scriptResult(rows int64, err error) driver.Result {
	return scriptResultValue{rows: rows, err: err}
}

func (scriptResultValue) LastInsertId() (int64, error) {
	return 0, nil
}

func (r scriptResultValue) RowsAffected() (int64, error) {
	return r.rows, r.err
}

type emptyRows struct{}

func (*emptyRows) Columns() []string {
	return []string{"value"}
}

func (*emptyRows) Close() error {
	return nil
}

func (*emptyRows) Next(dest []driver.Value) error {
	return io.EOF
}

func newScriptDB(
	t *testing.T,
	execFn func(int, string, []driver.NamedValue) (driver.Result, error),
	queryFn func(string, []driver.NamedValue) (driver.Rows, error),
) *sql.DB {
	t.Helper()
	db := sql.OpenDB(scriptConnector{conn: &scriptConn{execFn: execFn, queryFn: queryFn}})
	t.Cleanup(func() { _ = db.Close() })
	return db
}
