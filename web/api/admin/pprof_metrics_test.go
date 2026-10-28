package admin

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Aone2233/nekomari/pkg/metric"
)

// withMetricStoreForPprof swaps the process-wide store lookup for one test. Production
// reads metricstore.GetStore(); the seam exists so the field can be tested both ways
// without initializing the panel's metric store.
func withMetricStoreForPprof(t *testing.T, store *metric.Store) {
	t.Helper()
	previous := metricStoreForPprof
	metricStoreForPprof = func() *metric.Store { return store }
	t.Cleanup(func() { metricStoreForPprof = previous })
}

func openPprofMetricStore(t *testing.T, opts ...metric.Option) *metric.Store {
	t.Helper()
	store, err := metric.Open(context.Background(), metric.SQLiteInDir(t.TempDir(), opts...))
	if err != nil {
		t.Fatalf("open metric store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// No store means no metrics_db: the field is omitted, exactly like db_pool before the
// main database exists. A field of zeroes would read as "the pool is empty".
func TestPprofSummaryOmitsMetricsDBWithoutAStore(t *testing.T) {
	withMetricStoreForPprof(t, nil)

	if got := metricDatabasePools(); got != nil {
		t.Fatalf("metricDatabasePools() = %+v, want nil without a store", got)
	}
	runtimeValues := runtimeBlock(t, pprofSummaryData(t))
	if value, present := runtimeValues["metrics_db"]; present {
		t.Fatalf("runtime.metrics_db is present (%v) although no metric store is initialized", value)
	}
}

// With a store the runtime block carries metrics_db: the write pool flattened under the
// same five names as db_pool, and the dedicated read pool nested as "read".
func TestPprofSummaryReportsTheMetricDatabasePools(t *testing.T) {
	store := openPprofMetricStore(t, metric.WithSQLiteReadPool(2))
	withMetricStoreForPprof(t, store)

	snapshot := metricDatabasePools()
	if snapshot == nil {
		t.Fatal("metricDatabasePools() = nil although a store is initialized")
	}
	if snapshot.Read == nil {
		t.Fatal("the configured read pool was not reported")
	}

	runtimeValues := runtimeBlock(t, pprofSummaryData(t))
	metricsDB, ok := runtimeValues["metrics_db"].(map[string]any)
	if !ok {
		t.Fatalf("runtime.metrics_db is missing or not an object: %v", runtimeValues["metrics_db"])
	}
	for _, field := range []string{"open", "in_use", "idle", "wait_count", "wait_duration_ms"} {
		if _, ok := metricsDB[field].(float64); !ok {
			t.Errorf("metrics_db.%s is missing or not a number: %v", field, metricsDB[field])
		}
	}
	read, ok := metricsDB["read"].(map[string]any)
	if !ok {
		t.Fatalf("metrics_db.read is missing or not an object: %v", metricsDB["read"])
	}
	for _, field := range []string{"open", "in_use", "idle", "wait_count", "wait_duration_ms"} {
		if _, ok := read[field].(float64); !ok {
			t.Errorf("metrics_db.read.%s is missing or not a number: %v", field, read[field])
		}
	}
}

// Without a read pool the nested object is absent rather than zeroed: "reads go through
// the write pool" is a different fact from "a read pool exists but is empty".
func TestPprofSummaryOmitsMetricsDBReadWithoutAReadPool(t *testing.T) {
	store := openPprofMetricStore(t) // in-memory: no separate read pool possible
	withMetricStoreForPprof(t, store)

	if snapshot := metricDatabasePools(); snapshot == nil || snapshot.Read != nil {
		t.Fatalf("metricDatabasePools() = %+v, want a write pool and no read pool", snapshot)
	}

	runtimeValues := runtimeBlock(t, pprofSummaryData(t))
	metricsDB, ok := runtimeValues["metrics_db"].(map[string]any)
	if !ok {
		t.Fatalf("runtime.metrics_db is missing or not an object: %v", runtimeValues["metrics_db"])
	}
	if value, present := metricsDB["read"]; present {
		t.Fatalf("metrics_db.read is present (%v) although the store has no read pool", value)
	}
}

// The JSON names are the frontend contract: pin the flattened write pool and the optional
// nested read pool, so a rename here fails this test instead of blanking the page.
func TestPprofMetricDatabasePoolJSONContract(t *testing.T) {
	write := pprofDatabasePool{Open: 1, InUse: 1, Idle: 0, WaitCount: 7, WaitDurationMS: 9}

	raw, err := json.Marshal(pprofMetricDatabasePool{pprofDatabasePool: write})
	if err != nil {
		t.Fatalf("marshal write-only pool: %v", err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("the metric pool object is not a JSON object: %v", err)
	}
	if len(decoded) != 5 {
		t.Fatalf("write-only metrics_db has fields %v, want exactly the five db_pool names", decoded)
	}
	for _, field := range []string{"open", "in_use", "idle", "wait_count", "wait_duration_ms"} {
		if _, ok := decoded[field]; !ok {
			t.Errorf("write-only metrics_db is missing %q: %s", field, raw)
		}
	}

	read := pprofDatabasePool{Open: 2, InUse: 0, Idle: 2, WaitCount: 1, WaitDurationMS: 3}
	raw, err = json.Marshal(pprofMetricDatabasePool{pprofDatabasePool: write, Read: &read})
	if err != nil {
		t.Fatalf("marshal pool with a read pool: %v", err)
	}
	var withRead map[string]json.RawMessage
	if err := json.Unmarshal(raw, &withRead); err != nil {
		t.Fatalf("the metric pool object is not a JSON object: %v", err)
	}
	if _, ok := withRead["read"]; !ok {
		t.Fatalf("metrics_db lost its read pool: %s", raw)
	}
	var readFields map[string]float64
	if err := json.Unmarshal(withRead["read"], &readFields); err != nil {
		t.Fatalf("metrics_db.read is not a flat number object: %v", err)
	}
	if len(readFields) != 5 {
		t.Fatalf("metrics_db.read has fields %v, want exactly five", readFields)
	}
}

func TestPprofThemeCacheSummaryHasOnlySafeStatusFields(t *testing.T) {
	runtimeValues := runtimeBlock(t, pprofSummaryData(t))
	cache, ok := runtimeValues["theme_cache"].(map[string]any)
	if !ok || len(cache) != 2 {
		t.Fatalf("theme_cache = %#v", runtimeValues["theme_cache"])
	}
	if _, ok := cache["disk"].(string); !ok {
		t.Fatalf("invalid disk state: %#v", cache)
	}
	if _, ok := cache["memory_loaded"].(bool); !ok {
		t.Fatalf("invalid memory state: %#v", cache)
	}
}
