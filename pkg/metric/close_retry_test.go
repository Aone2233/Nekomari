package metric

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sqlite3 "github.com/mattn/go-sqlite3"
)

type closeFaultConnector struct {
	driver *sqlite3.SQLiteDriver
	dsn    string
}

func (c *closeFaultConnector) Connect(context.Context) (driver.Conn, error) {
	return c.driver.Open(c.dsn)
}

func (c *closeFaultConnector) Driver() driver.Driver { return c.driver }

// This wrapper models the ambiguous case: SQLite commits successfully, but
// the caller receives an error instead of the acknowledgement.
type lostCommitConnector struct {
	base *sqlite3.SQLiteDriver
	dsn  string
	fail *atomic.Bool
}

func (c *lostCommitConnector) Connect(context.Context) (driver.Conn, error) {
	conn, err := c.base.Open(c.dsn)
	if err != nil {
		return nil, err
	}
	return &lostCommitConn{SQLiteConn: conn.(*sqlite3.SQLiteConn), fail: c.fail}, nil
}

func (c *lostCommitConnector) Driver() driver.Driver { return c.base }

type lostCommitConn struct {
	*sqlite3.SQLiteConn
	fail *atomic.Bool
}

func (c *lostCommitConn) Begin() (driver.Tx, error) {
	tx, err := c.SQLiteConn.Begin()
	if err != nil {
		return nil, err
	}
	return &lostCommitTx{Tx: tx, fail: c.fail}, nil
}

func (c *lostCommitConn) BeginTx(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
	tx, err := c.SQLiteConn.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return &lostCommitTx{Tx: tx, fail: c.fail}, nil
}

type lostCommitTx struct {
	driver.Tx
	fail *atomic.Bool
}

type failPoolCloseConnector struct {
	base *sqlite3.SQLiteDriver
	dsn  string
}

func (c *failPoolCloseConnector) Connect(context.Context) (driver.Conn, error) {
	conn, err := c.base.Open(c.dsn)
	if err != nil {
		return nil, err
	}
	return &failPoolCloseConn{SQLiteConn: conn.(*sqlite3.SQLiteConn)}, nil
}

func (c *failPoolCloseConnector) Driver() driver.Driver { return c.base }

type failPoolCloseConn struct{ *sqlite3.SQLiteConn }

func (c *failPoolCloseConn) Close() error {
	if err := c.SQLiteConn.Close(); err != nil {
		return err
	}
	return errors.New("simulated pool close failure")
}

func TestClosePoolFailureIsStickyAfterSuccessfulFlush(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "pool-close.db")
	db := sql.OpenDB(&failPoolCloseConnector{base: &sqlite3.SQLiteDriver{}, dsn: path})
	s, err := Open(ctx, SQLite("", WithDB(db), WithMaxOpenConns(1)))
	if err != nil {
		t.Fatal(err)
	}
	// Exercise the owned-pool branch while using a fault-injecting connector.
	s.ownedDB = true
	if err := s.CreateMetric(ctx, Definition{Name: "pool-close", RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, Point{MetricName: "pool-close", EntityID: "node", Timestamp: time.Now(), Value: 7}); err != nil {
		t.Fatal(err)
	}
	first := s.CloseContext(ctx)
	if first == nil || !strings.Contains(first.Error(), "simulated pool close failure") {
		t.Fatalf("first close = %v", first)
	}
	if err := s.CloseContext(ctx); err == nil || err.Error() != first.Error() {
		t.Fatalf("failed pool close incorrectly became success: %v", err)
	}
	if err := s.Ping(ctx); !errors.Is(err, ErrClosed) {
		t.Fatalf("pool-close-failed store usable: %v", err)
	}
	assertSnapshotSummary(t, path, "pool-close", time.Minute, 1, 7)
}

func (tx *lostCommitTx) Commit() error {
	if err := tx.Tx.Commit(); err != nil {
		return err
	}
	if tx.fail.CompareAndSwap(true, false) {
		return errors.New("simulated lost commit acknowledgement")
	}
	return nil
}

func TestCloseRetainsHotBucketsAfterFlushFailure(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "close.db")
	s, err := Open(ctx, SQLite(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.CreateMetric(ctx, Definition{Name: "close-retry", RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Add(30 * time.Second)
	if err := s.Write(ctx, Point{MetricName: "close-retry", EntityID: "node", Timestamp: at, Value: 7}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ExecContext(ctx, `DROP TABLE metric_rollups`); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseContext(ctx); err == nil {
		t.Fatal("close unexpectedly succeeded with broken rollup table")
	}
	if err := s.Ping(ctx); err != nil {
		t.Fatalf("failed close made store unusable: %v", err)
	}
	if len(s.hot) != 1 {
		t.Fatalf("failed close discarded hot data: %d buckets", len(s.hot))
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseContext(ctx); err != nil {
		t.Fatalf("close retry: %v", err)
	}
	assertSnapshotSummary(t, path, "close-retry", time.Minute, 1, 7)
}

func TestCloseCommitFailureFailsClosed(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "commit.db")
	var deny atomic.Bool
	var attempts atomic.Int32
	d := &sqlite3.SQLiteDriver{ConnectHook: func(conn *sqlite3.SQLiteConn) error {
		conn.RegisterAuthorizer(func(op int, arg1, _, _ string) int {
			if op == sqlite3.SQLITE_TRANSACTION && arg1 == "COMMIT" && deny.Load() {
				attempts.Add(1)
				return sqlite3.SQLITE_DENY
			}
			return sqlite3.SQLITE_OK
		})
		return nil
	}}
	db := sql.OpenDB(&closeFaultConnector{driver: d, dsn: path})
	t.Cleanup(func() { _ = db.Close() })
	s, err := Open(ctx, SQLite("", WithDB(db), WithMaxOpenConns(1)))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateMetric(ctx, Definition{Name: "uncertain", RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListMetrics(ctx); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Add(30 * time.Second)
	point := Point{MetricName: "uncertain", EntityID: "node", Timestamp: at, Value: 7}
	if err := s.Write(ctx, point); err != nil {
		t.Fatal(err)
	}
	deny.Store(true)
	if err := s.CloseContext(ctx); !errors.Is(err, ErrCommitUncertain) {
		t.Fatalf("commit failure = %v", err)
	}
	deny.Store(false)
	if err := s.CloseContext(ctx); !errors.Is(err, ErrCommitUncertain) {
		t.Fatalf("uncertain commit was retried: %v", err)
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("commit attempted %d times, want 1", got)
	}
	if err := s.Ping(ctx); err != nil || len(s.hot) != 1 {
		t.Fatalf("uncertain close lost live state: ping=%v hot=%d", err, len(s.hot))
	}
	point.Timestamp = at.Add(time.Second)
	if err := s.Write(ctx, point); !errors.Is(err, ErrCommitUncertain) {
		t.Fatalf("write admitted after uncertain commit: %v", err)
	}
	if err := s.SnapshotSQLite(ctx, filepath.Join(t.TempDir(), "snapshot.db")); !errors.Is(err, ErrCommitUncertain) {
		t.Fatalf("snapshot accepted uncertain commit: %v", err)
	}
	// This fixture denied COMMIT before it could run; explicitly discard the
	// scratch-only poisoned handle rather than pretending recovery is general.
	s.rollupViewMu.Lock()
	s.hotCommitErr = nil
	s.rollupViewMu.Unlock()
	if err := s.CloseContext(ctx); err != nil {
		t.Fatal(fmt.Errorf("clean up scratch store: %w", err))
	}
}

func TestCloseLostCommitAcknowledgementNeverRetriesDelta(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "committed.db")
	var fail atomic.Bool
	db := sql.OpenDB(&lostCommitConnector{base: &sqlite3.SQLiteDriver{}, dsn: path, fail: &fail})
	s, err := Open(ctx, SQLite("", WithDB(db), WithMaxOpenConns(1)))
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := s.CreateMetric(ctx, Definition{Name: "committed", RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Add(30 * time.Second)
	if err := s.Write(ctx, Point{MetricName: "committed", EntityID: "node", Timestamp: at, Value: 9}); err != nil {
		t.Fatal(err)
	}
	fail.Store(true)
	if err := s.CloseContext(ctx); !errors.Is(err, ErrCommitUncertain) {
		t.Fatalf("lost acknowledgement = %v", err)
	}
	if err := s.CloseContext(ctx); !errors.Is(err, ErrCommitUncertain) {
		t.Fatalf("uncertain commit was retried: %v", err)
	}
	query := Query{MetricName: "committed", EntityID: "node", Start: at.Add(-time.Minute), End: at.Add(time.Minute)}
	if _, err := s.Query(ctx, query); !errors.Is(err, ErrCommitUncertain) {
		t.Fatalf("raw query exposed an ambiguous view: %v", err)
	}
	if _, err := s.QueryBatch(ctx, BatchQuery{MetricNames: []string{"committed"}, EntityIDs: []string{"node"}, Start: query.Start, End: query.End}); !errors.Is(err, ErrCommitUncertain) {
		t.Fatalf("raw batch exposed an ambiguous view: %v", err)
	}
	if _, err := s.Series(ctx, AggregateQuery{Query: query, Aggregation: AggSum, Interval: time.Minute}, at); !errors.Is(err, ErrCommitUncertain) {
		t.Fatalf("series exposed an ambiguous hot/persisted view: %v", err)
	}
	if _, err := s.Stats(ctx, query); !errors.Is(err, ErrCommitUncertain) {
		t.Fatalf("stats exposed an ambiguous hot/persisted view: %v", err)
	}
	if _, err := s.EntityIDs(ctx, query); !errors.Is(err, ErrCommitUncertain) {
		t.Fatalf("entity scan exposed an ambiguous view: %v", err)
	}
	if _, _, err := s.LatestBefore(ctx, "committed", "node", at.Add(time.Minute)); !errors.Is(err, ErrCommitUncertain) {
		t.Fatalf("latest exposed an ambiguous view: %v", err)
	}
	var count int
	var sum float64
	if err := db.QueryRowContext(ctx, `SELECT count, sum FROM metric_rollups`).Scan(&count, &sum); err != nil || count != 1 || sum != 9 {
		t.Fatalf("committed rows count=%d sum=%v err=%v", count, sum, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCloseRespectsCancellationAndExternalDBOwnership(t *testing.T) {
	path := filepath.Join(t.TempDir(), "caller.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s, err := Open(t.Context(), SQLite("", WithDB(db)))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateMetric(t.Context(), Definition{Name: "cancel", RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Add(30 * time.Second)
	if err := s.Write(t.Context(), Point{MetricName: "cancel", EntityID: "node", Timestamp: at, Value: 5}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := s.CloseContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled close = %v", err)
	}
	if err := s.CloseContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := db.PingContext(t.Context()); err != nil {
		t.Fatalf("caller-owned pool was closed: %v", err)
	}
	if err := s.CloseContext(t.Context()); err != nil {
		t.Fatalf("repeated close: %v", err)
	}
}

func TestSQLiteSnapshotRefusesExistingDestination(t *testing.T) {
	s, err := Open(t.Context(), SQLite(filepath.Join(t.TempDir(), "live.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	path := filepath.Join(t.TempDir(), "existing.db")
	if err := os.WriteFile(path, []byte("do not replace"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.SnapshotSQLite(t.Context(), path); err == nil {
		t.Fatal("existing destination was accepted")
	}
	contents, err := os.ReadFile(path)
	if err != nil || !strings.EqualFold(string(contents), "do not replace") {
		t.Fatalf("existing destination changed: %q, %v", contents, err)
	}
}
