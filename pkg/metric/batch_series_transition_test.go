package metric

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	sqlite3 "github.com/mattn/go-sqlite3"
)

type blockedRollupRead struct {
	mu      sync.Mutex
	match   string
	entered chan struct{}
	release chan struct{}
}

type blockedRollupConnector struct {
	base *sqlite3.SQLiteDriver
	dsn  string
	gate *blockedRollupRead
}

func (c *blockedRollupConnector) Connect(context.Context) (driver.Conn, error) {
	conn, err := c.base.Open(c.dsn)
	if err != nil {
		return nil, err
	}
	return &blockedRollupConn{SQLiteConn: conn.(*sqlite3.SQLiteConn), gate: c.gate}, nil
}

func (c *blockedRollupConnector) Driver() driver.Driver { return c.base }

type blockedRollupConn struct {
	*sqlite3.SQLiteConn
	gate *blockedRollupRead
}

func (c *blockedRollupConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	rows, err := c.SQLiteConn.QueryContext(ctx, query, args)
	c.gate.mu.Lock()
	match := c.gate.entered != nil && strings.Contains(query, c.gate.match)
	c.gate.mu.Unlock()
	if err != nil || !match {
		return rows, err
	}
	return &blockedRollupRows{Rows: rows, ctx: ctx, gate: c.gate}, nil
}

type blockedRollupRows struct {
	driver.Rows
	ctx  context.Context
	gate *blockedRollupRead
}

func (r *blockedRollupRows) Close() error {
	err := r.Rows.Close()
	r.gate.mu.Lock()
	entered, release := r.gate.entered, r.gate.release
	r.gate.entered, r.gate.release = nil, nil
	r.gate.mu.Unlock()
	if entered != nil {
		close(entered)
		select {
		case <-release:
		case <-r.ctx.Done():
			return r.ctx.Err()
		}
	}
	return err
}

func attachBlockedReadPool(t *testing.T, ctx context.Context, s *Store, path string, gate *blockedRollupRead) {
	t.Helper()
	readDB := sql.OpenDB(&blockedRollupConnector{base: &sqlite3.SQLiteDriver{}, dsn: path, gate: gate})
	readDB.SetMaxOpenConns(1)
	if err := s.configureSQLite(ctx, readDB); err != nil {
		_ = readDB.Close()
		t.Fatal(err)
	}
	s.readDB, s.ownedReadDB = readDB, true
}

// A deletion must not remove durable history and its memory representation in
// the middle of the same rollup query when the reader uses a separate pool.
func TestSeriesBatchDeletionUsesOneStoreView(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	gate := &blockedRollupRead{}
	path := filepath.Join(t.TempDir(), "view.db")
	s, err := Open(ctx, SQLite(path, WithMaxOpenConns(1)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.CreateMetric(ctx, Definition{Name: "view", RetentionDays: 30}); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Minute).Add(10 * time.Second)
	if err := s.Write(ctx, Point{MetricName: "view", EntityID: "node", Timestamp: base, Value: 10}); err != nil {
		t.Fatal(err)
	}
	if err := s.PersistHotRollups(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, Point{MetricName: "view", EntityID: "node", Timestamp: base.Add(time.Second), Value: 20}); err != nil {
		t.Fatal(err)
	}
	attachBlockedReadPool(t, ctx, s, path, gate)
	entered, release := make(chan struct{}), make(chan struct{})
	gate.mu.Lock()
	gate.match, gate.entered, gate.release = "FROM metric_rollups r", entered, release
	gate.mu.Unlock()
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	type queryResult struct {
		points []AggregatePoint
		err    error
	}
	queryDone := make(chan queryResult, 1)
	go func() {
		points, err := s.AggregateRollup(ctx, AggregateQuery{
			Query:       Query{MetricName: "view", EntityID: "node", Start: base.Add(-time.Minute), End: base.Add(time.Minute)},
			Aggregation: AggSum, Interval: time.Minute,
		}, time.Minute)
		queryDone <- queryResult{points, err}
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("read did not reach persisted scan")
	}
	deleteDone := make(chan error, 1)
	deleteStarted := make(chan struct{})
	go func() {
		close(deleteStarted)
		_, err := s.DeleteSeries(ctx, Query{MetricName: "view", EntityID: "node"})
		deleteDone <- err
	}()
	<-deleteStarted
	// The read pool has released SQLite's statement, while the query still has
	// not read the in-memory half. Deletion must wait for that whole view.
	select {
	case err := <-deleteDone:
		t.Fatalf("delete completed while read was paused: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	result := <-queryDone
	if result.err != nil || len(result.points) != 1 || result.points[0].Count != 2 || result.points[0].Value != 30 {
		t.Fatalf("concurrent view = %+v, %v; want complete 2/30", result.points, result.err)
	}
	if err := <-deleteDone; err != nil {
		t.Fatalf("delete after read: %v", err)
	}
}

// Rows committed during a flush must not become visible after a query has
// already captured an older series dictionary.
func TestSeriesBatchDictionarySharesFlushView(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	gate := &blockedRollupRead{}
	path := filepath.Join(t.TempDir(), "view.db")
	s, err := Open(ctx, SQLite(path, WithMaxOpenConns(1)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.CreateMetric(ctx, Definition{Name: "view", RetentionDays: 30}); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Minute).Add(10 * time.Second)
	if err := s.Write(ctx, Point{MetricName: "view", EntityID: "node", Timestamp: at, Value: 10}); err != nil {
		t.Fatal(err)
	}
	attachBlockedReadPool(t, ctx, s, path, gate)
	entered, release := make(chan struct{}), make(chan struct{})
	gate.mu.Lock()
	gate.match, gate.entered, gate.release = "FROM metric_series s", entered, release
	gate.mu.Unlock()
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	type queryResult struct {
		points []AggregatePoint
		err    error
	}
	queryDone := make(chan queryResult, 1)
	go func() {
		points, err := s.AggregateRollup(ctx, AggregateQuery{
			Query:       Query{MetricName: "view", EntityID: "node", Start: at.Add(-time.Minute), End: at.Add(time.Minute)},
			Aggregation: AggSum, Interval: time.Minute,
		}, time.Minute)
		queryDone <- queryResult{points, err}
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("read did not finish its series dictionary scan")
	}
	flushDone := make(chan error, 1)
	flushStarted := make(chan struct{})
	go func() {
		close(flushStarted)
		_, err := s.Flush(ctx, at.Add(2*time.Minute))
		flushDone <- err
	}()
	<-flushStarted
	select {
	case err := <-flushDone:
		t.Fatalf("flush completed while dictionary view was paused: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	result := <-queryDone
	if result.err != nil || len(result.points) != 1 || result.points[0].Count != 1 || result.points[0].Value != 10 {
		t.Fatalf("dictionary/flush view = %+v, %v; want 1/10", result.points, result.err)
	}
	if err := <-flushDone; err != nil {
		t.Fatal(err)
	}
}
