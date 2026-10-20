package metric

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
)

// The diagnostic page reads this while the store may be closed or not yet open, so a
// nil Store and a Store without pools must both answer with zeroes rather than panic.
func TestPoolStatsOfNilStoreIsZero(t *testing.T) {
	var store *Store
	stats := store.PoolStats()
	if stats.HasRead {
		t.Fatal("a nil store reported a dedicated read pool")
	}
	if !reflect.DeepEqual(stats.Write, sql.DBStats{}) || !reflect.DeepEqual(stats.Read, sql.DBStats{}) {
		t.Fatalf("nil store pools = %+v / %+v, want zero values", stats.Write, stats.Read)
	}
}

func TestPoolStatsOfUnopenedStoreIsZero(t *testing.T) {
	store := &Store{}
	stats := store.PoolStats()
	if stats.HasRead || !reflect.DeepEqual(stats.Write, sql.DBStats{}) {
		t.Fatalf("unopened store pools = %+v, want zero values", stats)
	}
}

// A SQLite store with a dedicated WAL read pool reports both pools: the write pool is
// deliberately serialized (one connection), the read pool is the configured width. The
// MaxOpenConnections values are the evidence that the snapshot is of the real pools and
// not a copy of one of them.
func TestPoolStatsReportsWriteAndReadPools(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, SQLiteInDir(t.TempDir(), WithSQLiteReadPool(2)))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	stats := store.PoolStats()
	if !stats.HasRead {
		t.Fatal("HasRead is false although a dedicated read pool was configured")
	}
	if stats.Write.MaxOpenConnections != 1 {
		t.Fatalf("write pool MaxOpenConnections = %d, want 1 (SQLite serializes writes)", stats.Write.MaxOpenConnections)
	}
	if stats.Read.MaxOpenConnections != 2 {
		t.Fatalf("read pool MaxOpenConnections = %d, want the configured 2", stats.Read.MaxOpenConnections)
	}
	for name, pool := range map[string]sql.DBStats{"write": stats.Write, "read": stats.Read} {
		if pool.OpenConnections < 0 || pool.InUse < 0 || pool.Idle < 0 || pool.WaitCount < 0 || pool.WaitDuration < 0 {
			t.Fatalf("%s pool counters must be non-negative: %+v", name, pool)
		}
	}

	// Stats() only: reading the snapshot must not open a connection on either pool.
	again := store.PoolStats()
	if again.Write.OpenConnections > stats.Write.OpenConnections {
		t.Fatalf("write OpenConnections grew from %d to %d: PoolStats must not open connections",
			stats.Write.OpenConnections, again.Write.OpenConnections)
	}
	if again.Read.OpenConnections > stats.Read.OpenConnections {
		t.Fatalf("read OpenConnections grew from %d to %d: PoolStats must not open connections",
			stats.Read.OpenConnections, again.Read.OpenConnections)
	}
}

// Without a dedicated read pool, reads go through the write pool: HasRead must be false
// and Read must stay the zero value, so a reader cannot mistake a copy for a second pool.
func TestPoolStatsWithoutAReadPoolReportsHasReadFalse(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, SQLite(":memory:", WithMaxOpenConns(1)))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	stats := store.PoolStats()
	if stats.HasRead {
		t.Fatal("HasRead is true although an in-memory store cannot have a separate read pool")
	}
	if !reflect.DeepEqual(stats.Read, sql.DBStats{}) {
		t.Fatalf("read pool stats = %+v, want the zero value when there is no read pool", stats.Read)
	}
	if stats.Write.MaxOpenConnections != 1 {
		t.Fatalf("write pool MaxOpenConnections = %d, want 1", stats.Write.MaxOpenConnections)
	}
}

// sql.DB.Stats is documented as safe after Close; the diagnostic page can therefore be
// opened during shutdown without racing a panic.
func TestPoolStatsAfterCloseDoesNotPanic(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, SQLite(":memory:", WithMaxOpenConns(1)))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	stats := store.PoolStats()
	if stats.Write.MaxOpenConnections != 1 {
		t.Fatalf("after Close MaxOpenConnections = %d, want the configured 1", stats.Write.MaxOpenConnections)
	}
	if stats.Write.OpenConnections != 0 {
		t.Fatalf("after Close OpenConnections = %d, want 0", stats.Write.OpenConnections)
	}
}
