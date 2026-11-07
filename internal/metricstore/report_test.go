package metricstore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/pkg/metric"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
	sqlite3 "github.com/mattn/go-sqlite3"
)

func useReportTestStore(t *testing.T, policy *metric.RollupPolicy) *metric.Store {
	t.Helper()
	ctx := context.Background()
	opts := []metric.Option{metric.WithMaxOpenConns(1)}
	if policy != nil {
		opts = append(opts, metric.WithRollupPolicy(*policy))
	}
	dsn := fmt.Sprintf("file:report-%d?mode=memory&cache=shared", time.Now().UnixNano())
	s, err := metric.Open(ctx, metric.SQLite(dsn, opts...))
	if err != nil {
		t.Fatalf("open metric store: %v", err)
	}
	if err := createMetricDefinitions(ctx, s); err != nil {
		_ = s.Close()
		t.Fatalf("create metric definitions: %v", err)
	}

	storeMu.Lock()
	previous := store
	store = s
	storeMu.Unlock()
	clearReportTrafficStates()
	t.Cleanup(func() {
		clearReportTrafficStates()
		storeMu.Lock()
		store = previous
		storeMu.Unlock()
		_ = s.Close()
	})
	return s
}

type reportCounterFault struct {
	remaining atomic.Int32
	denied    atomic.Int32
}

func (f *reportCounterFault) denyRollupRead() bool {
	for {
		remaining := f.remaining.Load()
		if remaining <= 0 {
			return false
		}
		if f.remaining.CompareAndSwap(remaining, remaining-1) {
			f.denied.Add(1)
			return true
		}
	}
}

type reportSQLiteConnector struct {
	driver *sqlite3.SQLiteDriver
	dsn    string
}

func (c *reportSQLiteConnector) Connect(context.Context) (driver.Conn, error) {
	return c.driver.Open(c.dsn)
}

func (c *reportSQLiteConnector) Driver() driver.Driver {
	return c.driver
}

// useReportCounterFailureStore denies exactly two rollup reads after schema
// setup, exercising failed counter restoration while leaving writes usable.
func useReportCounterFailureStore(t *testing.T) (*metric.Store, *reportCounterFault) {
	t.Helper()
	fault := &reportCounterFault{}
	dsn := fmt.Sprintf("file:report-counter-fault-%d?mode=memory&cache=shared", time.Now().UnixNano())
	driver := &sqlite3.SQLiteDriver{
		ConnectHook: func(conn *sqlite3.SQLiteConn) error {
			conn.RegisterAuthorizer(func(op int, arg1, _, _ string) int {
				if op == sqlite3.SQLITE_READ && arg1 == "metric_rollups" && fault.denyRollupRead() {
					return sqlite3.SQLITE_DENY
				}
				return sqlite3.SQLITE_OK
			})
			return nil
		},
	}
	db := sql.OpenDB(&reportSQLiteConnector{driver: driver, dsn: dsn})
	s, err := metric.Open(context.Background(), metric.SQLite("", metric.WithDB(db), metric.WithMaxOpenConns(1)))
	if err != nil {
		_ = db.Close()
		t.Fatalf("open metric store: %v", err)
	}
	if err := createMetricDefinitions(context.Background(), s); err != nil {
		_ = s.Close()
		_ = db.Close()
		t.Fatalf("create metric definitions: %v", err)
	}
	fault.remaining.Store(2)
	storeMu.Lock()
	previous := store
	store = s
	storeMu.Unlock()
	t.Cleanup(func() {
		clearReportTrafficStates()
		storeMu.Lock()
		store = previous
		storeMu.Unlock()
		_ = s.Close()
		_ = db.Close()
	})
	return s, fault
}

func TestReportBatchCounterRestoreFailureInitializesStateOnce(t *testing.T) {
	s, fault := useReportCounterFailureStore(t)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Second)
	first := v2.Report{
		UUID:      "counter-restore-failure",
		UpdatedAt: base,
		CPU:       v2.CPUReport{Usage: 10},
		Network:   v2.NetworkReport{TotalUp: 100, TotalDown: 200, CycleUp: 40, CycleDown: 60},
	}

	if _, err := writeReportBatch(ctx, []v2.Report{first}); err != nil {
		t.Fatalf("write first report after counter restore failure: %v", err)
	}
	if fault.denied.Load() != 2 {
		t.Fatalf("counter restore queries = %d, want 2", fault.denied.Load())
	}
	stateValue, ok := reportTrafficStates.Load(first.UUID)
	if !ok {
		t.Fatal("report traffic state was not persisted")
	}
	state := stateValue.(*reportTrafficState)
	state.mu.Lock()
	initialized := state.initialized
	state.mu.Unlock()
	if !initialized {
		t.Fatal("report traffic state was not initialized after restore failure")
	}

	second := first
	second.UpdatedAt = base.Add(time.Second)
	second.Network.TotalUp = 150
	second.Network.TotalDown = 260
	if _, err := writeReportBatch(ctx, []v2.Report{second}); err != nil {
		t.Fatalf("write second report: %v", err)
	}
	if fault.denied.Load() != 2 {
		t.Fatalf("counter restore queries after second batch = %d, want no repeat", fault.denied.Load())
	}
	assertMetricValues(t, s, MetricNetTotalUp, first.UUID, base.Add(-time.Second), base.Add(2*time.Second), []float64{100, 150})
	assertMetricValues(t, s, MetricTrafficUp, first.UUID, base.Add(-time.Second), base.Add(2*time.Second), []float64{40, 40})
	assertMetricValues(t, s, MetricNetTotalDown, first.UUID, base.Add(-time.Second), base.Add(2*time.Second), []float64{200, 260})
	assertMetricValues(t, s, MetricTrafficDown, first.UUID, base.Add(-time.Second), base.Add(2*time.Second), []float64{60, 60})
}

func TestReportAcceptedMaintenanceFailureIsNotRequeued(t *testing.T) {
	s := useReportTestStore(t, nil)
	ctx := t.Context()
	base := time.Now().UTC().Truncate(time.Minute).Add(-4 * time.Minute)
	if _, err := s.ExecContext(ctx, `CREATE TRIGGER reject_metric_insert BEFORE INSERT ON metric_rollups BEGIN SELECT RAISE(ABORT, 'injected rollup failure'); END`); err != nil {
		t.Fatal(err)
	}
	pending := []v2.Report{{UUID: "accepted-report", UpdatedAt: base.Add(5 * time.Second), CPU: v2.CPUReport{Usage: 42}, Network: v2.NetworkReport{TotalUp: 100, TotalDown: 200}}}
	if err := writePendingReports(ctx, &pending); err != nil || len(pending) != 0 {
		t.Fatalf("accepted report remains retryable: pending=%d err=%v", len(pending), err)
	}
	stateValue, ok := reportTrafficStates.Load("accepted-report")
	if !ok {
		t.Fatal("accepted report did not advance its traffic state")
	}
	state := stateValue.(*reportTrafficState)
	state.mu.Lock()
	timestamp := state.timestamp
	state.mu.Unlock()
	if !timestamp.Equal(base.Add(5 * time.Second)) {
		t.Fatalf("accepted report state timestamp = %s", timestamp)
	}
	if _, err := s.ExecContext(ctx, `DROP TRIGGER reject_metric_insert`); err != nil {
		t.Fatal(err)
	}
	if err := s.PersistHotRollups(ctx); err != nil {
		t.Fatal(err)
	}
	var rows []metric.PersistedRollup
	if _, err := s.ExportRollups(ctx, MetricCPU, 100, func(batch []metric.PersistedRollup) error {
		rows = append(rows, batch...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Count != 1 || rows[0].Sum != 42 {
		t.Fatalf("accepted report lost or duplicated: %+v", rows)
	}
}

func TestPingAcceptedMaintenanceFailureIsNotRequeued(t *testing.T) {
	s := useReportTestStore(t, nil)
	ctx := t.Context()
	at := time.Now().UTC().Truncate(time.Minute).Add(-4*time.Minute + 5*time.Second)
	if _, err := s.ExecContext(ctx, `CREATE TRIGGER reject_ping_insert BEFORE INSERT ON metric_rollups BEGIN SELECT RAISE(ABORT, 'injected rollup failure'); END`); err != nil {
		t.Fatal(err)
	}
	pending := []models.PingRecord{{TaskId: 12, Client: "accepted-ping", Time: at, Value: 7}}
	if err := writePendingPingRecords(ctx, &pending); err != nil || len(pending) != 0 {
		t.Fatalf("accepted ping remains retryable: pending=%d err=%v", len(pending), err)
	}
	if _, err := s.ExecContext(ctx, `DROP TRIGGER reject_ping_insert`); err != nil {
		t.Fatal(err)
	}
	if err := s.PersistHotRollups(ctx); err != nil {
		t.Fatal(err)
	}
	var rows []metric.PersistedRollup
	if _, err := s.ExportRollups(ctx, MetricPingLatency, 100, func(batch []metric.PersistedRollup) error {
		rows = append(rows, batch...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Count != 1 || rows[0].Sum != 7 {
		t.Fatalf("accepted ping lost or duplicated: %+v", rows)
	}
}

func TestAmbiguousReportDoesNotBlockOtherNodesOrSkipSameNode(t *testing.T) {
	s := useReportTestStore(t, nil)
	ctx := t.Context()
	base := time.Now().UTC().Truncate(time.Minute).Add(-4 * time.Minute)
	if err := s.Write(ctx, metric.Point{MetricName: MetricCPU, EntityID: "blocked", Timestamp: base.Add(20 * time.Second), Value: 10}); err != nil {
		t.Fatal(err)
	}
	// A different Store handle cannot reconstruct exact identities from rollups.
	if err := s.PersistHotRollups(ctx); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "snapshot.db")
	if err := s.SnapshotSQLite(ctx, path); err != nil {
		t.Fatal(err)
	}
	other, err := metric.Open(ctx, metric.SQLite(path))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	storeMu.Lock()
	store = other
	storeMu.Unlock()
	t.Cleanup(func() {
		storeMu.Lock()
		store = s
		storeMu.Unlock()
	})
	pending := []v2.Report{
		{UUID: "blocked", UpdatedAt: base.Add(10 * time.Second), CPU: v2.CPUReport{Usage: 1}},
		{UUID: "healthy", UpdatedAt: base.Add(10 * time.Second), CPU: v2.CPUReport{Usage: 2}},
		{UUID: "blocked", UpdatedAt: base.Add(30 * time.Second), CPU: v2.CPUReport{Usage: 3}},
	}
	err = writePendingReports(ctx, &pending)
	if !errors.Is(err, metric.ErrRawHistoryUnavailable) || len(pending) != 2 || pending[0].UUID != "blocked" || pending[1].UUID != "blocked" {
		t.Fatalf("ambiguous queue outcome: pending=%+v err=%v", pending, err)
	}
	points, err := other.Query(ctx, metric.Query{MetricName: MetricCPU, EntityID: "healthy", Start: base, End: base.Add(time.Minute)})
	if err != nil || len(points) != 1 || points[0].Value != 2 {
		t.Fatalf("unrelated node did not progress: points=%+v err=%v", points, err)
	}
	points, err = other.Query(ctx, metric.Query{MetricName: MetricCPU, EntityID: "blocked", Start: base, End: base.Add(time.Minute)})
	if err != nil || len(points) != 0 {
		t.Fatalf("later blocked-node report bypassed earlier report: points=%+v err=%v", points, err)
	}
}

func TestAmbiguousPingDoesNotBlockOtherSeriesOrSkipSameSeries(t *testing.T) {
	s := useReportTestStore(t, nil)
	ctx := t.Context()
	base := time.Now().UTC().Truncate(time.Minute).Add(-4 * time.Minute)
	seed := models.PingRecord{Client: "node", TaskId: 12, Time: base.Add(20 * time.Second), Value: 10}
	if err := writePingRecords(ctx, []models.PingRecord{seed}); err != nil {
		t.Fatal(err)
	}
	if err := s.PersistHotRollups(ctx); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "snapshot.db")
	if err := s.SnapshotSQLite(ctx, path); err != nil {
		t.Fatal(err)
	}
	other, err := metric.Open(ctx, metric.SQLite(path))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	storeMu.Lock()
	store = other
	storeMu.Unlock()
	t.Cleanup(func() {
		storeMu.Lock()
		store = s
		storeMu.Unlock()
	})
	pending := []models.PingRecord{
		{Client: "node", TaskId: 12, Time: base.Add(10 * time.Second), Value: 1},
		{Client: "node", TaskId: 13, Time: base.Add(10 * time.Second), Value: 2},
		{Client: "node", TaskId: 12, Time: base.Add(30 * time.Second), Value: 3},
	}
	err = writePendingPingRecords(ctx, &pending)
	if !errors.Is(err, metric.ErrRawHistoryUnavailable) || len(pending) != 2 || pending[0].TaskId != 12 || pending[1].TaskId != 12 {
		t.Fatalf("ambiguous ping queue outcome: pending=%+v err=%v", pending, err)
	}
	points, err := other.Query(ctx, metric.Query{MetricName: MetricPingLatency, EntityID: "node", Tags: map[string]string{"task_id": "13"}, Start: base, End: base.Add(time.Minute)})
	if err != nil || len(points) != 1 || points[0].Value != 2 {
		t.Fatalf("unrelated ping did not progress: points=%+v err=%v", points, err)
	}
	points, err = other.Query(ctx, metric.Query{MetricName: MetricPingLatency, EntityID: "node", Tags: map[string]string{"task_id": "12"}, Start: base, End: base.Add(time.Minute)})
	if err != nil || len(points) != 0 {
		t.Fatalf("later blocked ping bypassed earlier record: points=%+v err=%v", points, err)
	}
}

func TestWriteReportStoresMinuteMetricsAndResetAwareTraffic(t *testing.T) {
	ctx := context.Background()
	policy := defaultRollupPolicy()
	s := useReportTestStore(t, &policy)
	base := time.Now().UTC().Truncate(time.Minute).Add(5 * time.Second)
	now := base.Add(45 * time.Second)

	report := v2.Report{
		UUID:        "node-a",
		UpdatedAt:   base,
		CPU:         v2.CPUReport{Usage: 12.5},
		Ram:         v2.RamReport{Used: 100, Total: 1000},
		Swap:        v2.RamReport{Used: 20, Total: 200},
		Load:        v2.LoadReport{Load1: 0.5},
		Disk:        v2.DiskReport{Used: 300, Total: 3000},
		Network:     v2.NetworkReport{Up: 3, Down: 4, TotalUp: 100, TotalDown: 200, CycleUp: 40, CycleDown: 60},
		Process:     7,
		Connections: v2.ConnectionsReport{TCP: 8, UDP: 9},
		GPU: &v2.GPUDetailReport{
			AverageUsage: 25,
			DetailedInfo: []v2.GPUDeviceInfo{{
				Name: "GPU 0", MemoryUsed: 400, MemoryTotal: 800, Utilization: 30, Temperature: 55,
			}},
		},
	}
	if _, err := WriteReport(ctx, report); err != nil {
		t.Fatalf("write first report: %v", err)
	}

	report.UpdatedAt = base.Add(3 * time.Second)
	report.Network.TotalUp = 150
	report.Network.TotalDown = 260
	report.Network.CycleUp = 90
	report.Network.CycleDown = 140
	if _, err := WriteReport(ctx, report); err != nil {
		t.Fatalf("write second report: %v", err)
	}

	// A kernel counter that drops (host or interface restarted) while the cycle cumulative keeps rising.
	//
	// This is the shape that produced the phantom: the old code took a delta here, saw `current < previous`,
	// and - because the current value was below its reset cap - recorded the whole cumulative as one
	// interval's traffic. The cycle value must simply be stored, whatever the kernel counter did.
	report.UpdatedAt = base.Add(6 * time.Second)
	report.Network.TotalUp = 20
	report.Network.TotalDown = 30
	report.Network.CycleUp = 120
	report.Network.CycleDown = 180
	if _, err := WriteReport(ctx, report); err != nil {
		t.Fatalf("write reset report: %v", err)
	}

	// `traffic.*` mirrors the agent's cycle cumulative, stored as-is: 40, then 90, then 120 - with the same
	// value regardless of the kernel counter's dip. Reading it means taking the last value, never summing it
	// (that would be "cumulative x sample count") and never differencing it.
	assertMetricValues(t, s, MetricTrafficUp, report.UUID, base.Add(-time.Second), base.Add(time.Minute), []float64{40, 90, 120})
	assertMetricValues(t, s, MetricTrafficDown, report.UUID, base.Add(-time.Second), base.Add(time.Minute), []float64{60, 140, 180})
	// And the kernel counter is stored raw, which is what makes it the only legitimate subject of a delta.
	assertMetricValues(t, s, MetricNetTotalUp, report.UUID, base.Add(-time.Second), base.Add(time.Minute), []float64{100, 150, 20})
	// The sum aggregate is asserted because it is *available*, and the value documents why summing this
	// metric is wrong for a reader: 40+90+120 = 250 is not "traffic used", it is three snapshots of one
	// cumulative. The correct aggregate for `traffic.*` is `last`; the frontends were changed to use it.
	assertMetricAggregate(t, s, MetricTrafficUp, report.UUID, base.Add(-time.Second), base.Add(time.Minute), metric.AggSum, 250, 3)
	assertMetricAggregate(t, s, MetricTrafficUp, report.UUID, base.Add(-time.Second), base.Add(time.Minute), metric.AggLast, 120, 3)
	assertMetricAggregate(t, s, MetricTrafficDown, report.UUID, base.Add(-time.Second), base.Add(time.Minute), metric.AggLast, 180, 3)

	gpuPoints, err := s.Query(ctx, metric.Query{
		MetricName: MetricGPUDeviceUsage,
		EntityID:   report.UUID,
		Start:      base.Add(-time.Second),
		End:        base.Add(time.Minute),
		Tags:       map[string]string{"device_index": "0"},
		Order:      metric.OrderAsc,
	})
	if err != nil {
		t.Fatalf("query GPU points: %v", err)
	}
	if len(gpuPoints) != 3 || !gpuPoints[2].Timestamp.Equal(base.Add(6*time.Second)) || gpuPoints[2].Tags["device_name"] != "GPU 0" {
		t.Fatalf("unexpected GPU points: %#v", gpuPoints)
	}

	if _, err := s.Compact(ctx, now); err != nil {
		t.Fatalf("compact reports: %v", err)
	}
	deleteReportTrafficState(report.UUID)
	report.UpdatedAt = now
	report.Network.TotalUp = 35
	report.Network.TotalDown = 50
	report.Network.CycleUp = 130
	report.Network.CycleDown = 200
	if _, err := WriteReport(ctx, report); err != nil {
		t.Fatalf("write after restoring rollup baseline: %v", err)
	}
	// The cycle cumulative continues from the previous report's 120/180 rather than restarting, even though
	// the counter state was explicitly dropped — which is the point: it is a value the agent owns, not
	// something derived from a baseline this process has to remember.
	assertMetricValues(t, s, MetricTrafficUp, report.UUID, now.Add(-time.Second), now.Add(time.Second), []float64{130})
	assertMetricValues(t, s, MetricTrafficDown, report.UUID, now.Add(-time.Second), now.Add(time.Second), []float64{200})
}

func TestWriteReportSkipsMetricsWithoutAgentData(t *testing.T) {
	ctx := context.Background()
	s := useReportTestStore(t, nil)
	timestamp := time.Now().UTC()
	if _, err := WriteReport(ctx, v2.Report{
		UUID: "node-without-gpu", UpdatedAt: timestamp,
	}); err != nil {
		t.Fatalf("write report: %v", err)
	}
	points, err := s.Query(ctx, metric.Query{
		MetricName: MetricGPU, EntityID: "node-without-gpu",
		Start: timestamp.Add(-time.Second), End: timestamp.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("query GPU metric: %v", err)
	}
	if len(points) != 0 {
		t.Fatalf("GPU metric was written without GPU data: %#v", points)
	}
}

func TestReportBatcherFlushesQueuedReports(t *testing.T) {
	ctx := context.Background()
	s := useReportTestStore(t, nil)
	StartReportBatcher()
	t.Cleanup(func() {
		if err := StopReportBatcher(ctx); err != nil {
			t.Errorf("stop report batcher: %v", err)
		}
	})

	base := time.Now().UTC().Truncate(time.Minute).Add(10 * time.Second)
	first := v2.Report{
		UUID:      "batched-node",
		UpdatedAt: base,
		CPU:       v2.CPUReport{Usage: 10},
		Network:   v2.NetworkReport{TotalUp: 100, TotalDown: 200, CycleUp: 40, CycleDown: 60},
	}
	second := first
	second.UpdatedAt = base.Add(3 * time.Second)
	second.CPU.Usage = 20
	second.Network.TotalUp = 150
	second.Network.TotalDown = 260

	if _, err := WriteReport(ctx, first); err != nil {
		t.Fatalf("queue first report: %v", err)
	}
	if _, err := WriteReport(ctx, second); err != nil {
		t.Fatalf("queue second report: %v", err)
	}
	points, err := s.Query(ctx, metric.Query{
		MetricName: MetricCPU,
		EntityID:   first.UUID,
		Start:      base.Add(-time.Second),
		End:        base.Add(time.Minute),
		Order:      metric.OrderAsc,
	})
	if err != nil {
		t.Fatalf("query before flush: %v", err)
	}
	if len(points) != 0 {
		t.Fatalf("queued reports were written before flush: %#v", points)
	}

	if err := FlushReportBatch(ctx); err != nil {
		t.Fatalf("flush report batch: %v", err)
	}
	assertMetricValues(t, s, MetricCPU, first.UUID, base.Add(-time.Second), base.Add(time.Minute), []float64{10, 20})
	assertMetricValues(t, s, MetricNetTotalUp, first.UUID, base.Add(-time.Second), base.Add(time.Minute), []float64{100, 150})
	assertMetricValues(t, s, MetricTrafficUp, first.UUID, base.Add(-time.Second), base.Add(time.Minute), []float64{40, 40})
	assertMetricValues(t, s, MetricNetTotalDown, first.UUID, base.Add(-time.Second), base.Add(time.Minute), []float64{200, 260})
	assertMetricValues(t, s, MetricTrafficDown, first.UUID, base.Add(-time.Second), base.Add(time.Minute), []float64{60, 60})
	assertMetricAggregate(t, s, MetricCPU, first.UUID, base.Add(-time.Second), base.Add(time.Minute), metric.AggAvg, 15, 2)
}

func TestFailedReportBatcherStopRetainsAcceptedRecordsForRetry(t *testing.T) {
	ctx := context.Background()
	s := useReportTestStore(t, nil)
	StartReportBatcher()
	t.Cleanup(func() {
		if err := StopReportBatcher(ctx); err != nil {
			t.Errorf("stop report batcher: %v", err)
		}
	})
	base := time.Now().UTC()
	record := models.PingRecord{Client: "stop-retry-node", TaskId: 41, Time: base, Value: 17}
	if err := WritePingRecord(ctx, record); err != nil {
		t.Fatal(err)
	}
	if err := storeOperations.Acquire(ctx); err != nil {
		t.Fatal(err)
	}
	stopCtx, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	err := StopReportBatcher(stopCtx)
	cancel()
	storeOperations.Release()
	if err == nil {
		t.Fatal("blocked flush was reported as a successful stop")
	}
	if err := WritePingRecord(ctx, record); !errors.Is(err, ErrReportBatchStopped) {
		t.Fatalf("stopping batcher admitted another record: %v", err)
	}
	if err := StopReportBatcher(ctx); err != nil {
		t.Fatalf("retry failed to drain accepted record: %v", err)
	}
	assertMetricValues(t, s, MetricPingLatency, record.Client, base.Add(-time.Second), base.Add(time.Minute), []float64{17})
}

func TestReportBatcherStopReturnsFlushFailureAndCanRetry(t *testing.T) {
	ctx := context.Background()
	s := useReportTestStore(t, nil)
	StartReportBatcher()
	t.Cleanup(func() {
		if err := StopReportBatcher(ctx); err != nil {
			t.Errorf("stop report batcher: %v", err)
		}
	})
	base := time.Now().UTC()
	record := models.PingRecord{Client: "stop-error-node", TaskId: 42, Time: base, Value: 23}
	if err := WritePingRecord(ctx, record); err != nil {
		t.Fatal(err)
	}
	storeMu.Lock()
	store = nil
	storeMu.Unlock()
	defer func() {
		storeMu.Lock()
		store = s
		storeMu.Unlock()
	}()
	stopCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	err := StopReportBatcher(stopCtx)
	cancel()
	if err == nil || !strings.Contains(err.Error(), "metric store not enabled") {
		t.Fatalf("stop should return the flush error, got %v", err)
	}
	storeMu.Lock()
	store = s
	storeMu.Unlock()
	if err := WritePingRecord(ctx, record); !errors.Is(err, ErrReportBatchStopped) {
		t.Fatalf("failed stop admitted another record: %v", err)
	}
	if err := StopReportBatcher(ctx); err != nil {
		t.Fatalf("retry failed: %v", err)
	}
	assertMetricValues(t, s, MetricPingLatency, record.Client, base.Add(-time.Second), base.Add(time.Minute), []float64{23})
}

func TestReportBatcherConcurrentStopsShareCompletedDrain(t *testing.T) {
	ctx := context.Background()
	s := useReportTestStore(t, nil)
	StartReportBatcher()
	t.Cleanup(func() {
		if err := StopReportBatcher(ctx); err != nil {
			t.Errorf("stop report batcher: %v", err)
		}
	})
	base := time.Now().UTC()
	record := models.PingRecord{Client: "concurrent-stop-node", TaskId: 43, Time: base, Value: 31}
	if err := WritePingRecord(ctx, record); err != nil {
		t.Fatal(err)
	}
	if err := storeOperations.Acquire(ctx); err != nil {
		t.Fatal(err)
	}
	const callers = 16
	var started sync.WaitGroup
	started.Add(callers)
	results := make(chan error, callers)
	stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for i := 0; i < callers; i++ {
		go func() {
			started.Done()
			results <- StopReportBatcher(stopCtx)
		}()
	}
	started.Wait()
	storeOperations.Release()
	for i := 0; i < callers; i++ {
		if err := <-results; err != nil {
			t.Fatalf("concurrent stop %d failed: %v", i, err)
		}
	}
	assertMetricValues(t, s, MetricPingLatency, record.Client, base.Add(-time.Second), base.Add(time.Minute), []float64{31})
}

func TestPingBatcherFlushesLatencyAndLossTogether(t *testing.T) {
	ctx := context.Background()
	s := useReportTestStore(t, nil)
	StartReportBatcher()
	t.Cleanup(func() {
		if err := StopReportBatcher(ctx); err != nil {
			t.Errorf("stop report batcher: %v", err)
		}
	})

	base := time.Now().UTC().Truncate(time.Second)
	records := []models.PingRecord{
		{Client: "ping-node", TaskId: 7, Time: base, Value: 24},
		{Client: "ping-node", TaskId: 7, Time: base.Add(time.Minute), Value: -1},
		{Client: "ping-node", TaskId: 8, Time: base, Value: 31},
	}
	for _, record := range records {
		if err := WritePingRecord(ctx, record); err != nil {
			t.Fatalf("queue ping record: %v", err)
		}
	}

	for _, name := range pingMetricNames {
		points, err := s.Query(ctx, metric.Query{
			MetricName: name,
			EntityID:   "ping-node",
			Start:      base.Add(-time.Second),
			End:        base.Add(2 * time.Minute),
		})
		if err != nil {
			t.Fatalf("query queued %s points: %v", name, err)
		}
		if len(points) != 0 {
			t.Fatalf("queued %s points were written before flush: %#v", name, points)
		}
	}

	if err := FlushReportBatch(ctx); err != nil {
		t.Fatalf("flush ping batch: %v", err)
	}

	latency, err := s.Query(ctx, metric.Query{
		MetricName: MetricPingLatency,
		EntityID:   "ping-node",
		Tags:       map[string]string{"task_id": "7"},
		Start:      base.Add(-time.Second),
		End:        base.Add(2 * time.Minute),
		Order:      metric.OrderAsc,
	})
	if err != nil {
		t.Fatalf("query flushed latency points: %v", err)
	}
	if len(latency) != 2 || latency[0].Value != 24 || latency[1].Value != -1 {
		t.Fatalf("latency points = %#v, want both original samples", latency)
	}

	loss, err := s.Query(ctx, metric.Query{
		MetricName: MetricPingLoss,
		EntityID:   "ping-node",
		Tags:       map[string]string{"task_id": "7"},
		Start:      base.Add(-time.Second),
		End:        base.Add(2 * time.Minute),
		Order:      metric.OrderAsc,
	})
	if err != nil {
		t.Fatalf("query flushed loss points: %v", err)
	}
	if len(loss) != 2 || loss[0].Value != 0 || loss[1].Value != 1 {
		t.Fatalf("loss points = %#v, want success and loss samples", loss)
	}
}

func TestPinnedPingSyncWriteBypassesPendingBatch(t *testing.T) {
	ctx := context.Background()
	s := useReportTestStore(t, nil)
	StartReportBatcher()
	t.Cleanup(func() {
		if err := StopReportBatcher(ctx); err != nil {
			t.Errorf("stop report batcher: %v", err)
		}
	})
	base := time.Now().UTC()
	if err := PreparePinnedPingHistory(ctx, "sync-node", models.PingTask{Id: 19, Type: "tcp", TargetFamily: "ipv6"}); err != nil {
		t.Fatal(err)
	}
	if err := WritePingRecordSync(ctx, models.PingRecord{Client: "sync-node", TaskId: 19, Time: base, Value: 3, ValueUS: 3456, PingType: "tcp", Family: "ipv6"}); err != nil {
		// The nonblocking append deliberately rejects an old minute if a clock
		// boundary passes between result admission and the store commit.
		if !errors.Is(err, metric.ErrWriteUnavailable) || base.Minute() == time.Now().UTC().Minute() {
			t.Fatal(err)
		}
		base = time.Now().UTC()
		if err := WritePingRecordSync(ctx, models.PingRecord{Client: "sync-node", TaskId: 19, Time: base, Value: 3, ValueUS: 3456, PingType: "tcp", Family: "ipv6"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{MetricPingLatency, MetricPingLoss, MetricPingSuccessLatency} {
		points, err := s.Query(ctx, metric.Query{MetricName: name, EntityID: "sync-node", Start: base.Add(-time.Second), End: base.Add(time.Minute)})
		if err != nil || len(points) != 1 || points[0].Tags["family"] != "ipv6" {
			t.Fatalf("synchronous %s = %+v, error %v", name, points, err)
		}
	}
}

// Failed probes omit the success-only series, but a later successful probe
// still needs its baseline before entering the nonblocking admission fence.
func TestPinnedHistoryPreparationIncludesSuccessAfterLoss(t *testing.T) {
	ctx := t.Context()
	s := useReportTestStore(t, nil)
	task := models.PingTask{Id: 19, Type: "tcp", TargetFamily: "ipv6"}
	if err := PreparePinnedPingHistory(ctx, "sync-loss", task); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Millisecond)
	failure := models.PingRecord{Client: "sync-loss", TaskId: task.Id, Time: at, Value: -1, PingType: "tcp", Family: "ipv6"}
	if err := WritePingRecordSync(ctx, failure); err != nil {
		t.Fatalf("write failed probe: %v", err)
	}
	// Use a separate timestamp in the same minute; no real-time sleep or
	// production network probe is involved.
	if time.Until(at.Truncate(time.Minute).Add(time.Minute)) < 20*time.Millisecond {
		t.Skip("minute boundary too close to create two distinct samples")
	}
	for time.Now().UTC().UnixMilli() <= at.UnixMilli() {
		time.Sleep(time.Millisecond)
	}
	if err := PreparePinnedPingHistory(ctx, "sync-loss", task); err != nil {
		t.Fatal(err)
	}
	success := failure
	success.Time, success.Value, success.ValueUS = time.Now().UTC(), 2, 2345
	if err := WritePingRecordSync(ctx, success); err != nil {
		t.Fatalf("write successful probe after loss: %v", err)
	}
	for name, want := range map[string][]float64{MetricPingLatency: {-1, 2.345}, MetricPingLoss: {1, 0}, MetricPingSuccessLatency: {2.345}} {
		points, err := s.Query(ctx, metric.Query{MetricName: name, EntityID: "sync-loss", Start: at.Add(-time.Second), End: success.Time.Add(time.Second)})
		if err != nil || len(points) != len(want) {
			t.Fatalf("%s points = %+v, %v; want %v", name, points, err, want)
		}
		for i, point := range points {
			if point.Value != want[i] {
				t.Fatalf("%s points = %+v, want %v", name, points, want)
			}
		}
	}
}

func TestPinnedPingSyncWriteRefusesBusyStoreAndCancellation(t *testing.T) {
	s := useReportTestStore(t, nil)
	at := time.Now().UTC()
	rec := models.PingRecord{Client: "sync-busy", TaskId: 19, Time: at, Value: 2, ValueUS: 2345, PingType: "tcp", Family: "ipv6"}
	if !storeOperations.TryAcquire() {
		t.Fatal("could not hold store operation gate")
	}
	done := make(chan error, 1)
	go func() { done <- WritePingRecordSync(context.Background(), rec) }()
	select {
	case err := <-done:
		storeOperations.Release()
		if !errors.Is(err, metric.ErrWriteUnavailable) {
			t.Fatalf("busy store error = %v", err)
		}
	case <-time.After(time.Second):
		storeOperations.Release()
		<-done
		t.Fatal("fenced write waited for store maintenance")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := WritePingRecordSync(ctx, rec); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled write = %v", err)
	}
	for _, name := range []string{MetricPingLatency, MetricPingLoss, MetricPingSuccessLatency} {
		points, err := s.Query(context.Background(), metric.Query{MetricName: name, EntityID: rec.Client, Start: at.Add(-time.Second), End: at.Add(time.Second)})
		if err != nil || len(points) != 0 {
			t.Fatalf("failed write left %s points %+v: %v", name, points, err)
		}
	}
}

func TestReportBatchDiscardsDuplicateTimestamp(t *testing.T) {
	ctx := context.Background()
	s := useReportTestStore(t, nil)
	base := time.Now().UTC().Truncate(time.Second)
	pending := []v2.Report{
		{UUID: "node-a", UpdatedAt: base, CPU: v2.CPUReport{Usage: 10}, Network: v2.NetworkReport{TotalUp: 100}},
		{UUID: "node-a", UpdatedAt: base, CPU: v2.CPUReport{Usage: 20}, Network: v2.NetworkReport{TotalUp: 150}},
		{UUID: "node-b", UpdatedAt: base, CPU: v2.CPUReport{Usage: 30}, Network: v2.NetworkReport{TotalUp: 200}},
		{UUID: "node-b", UpdatedAt: base.Add(time.Second), CPU: v2.CPUReport{Usage: 40}, Network: v2.NetworkReport{TotalUp: 260}},
	}

	if err := writePendingReports(ctx, &pending); err != nil {
		t.Fatalf("write report batch: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending reports = %d, want 0", len(pending))
	}
	assertMetricValues(t, s, MetricCPU, "node-a", base.Add(-time.Second), base.Add(time.Minute), []float64{10})
	assertMetricValues(t, s, MetricCPU, "node-b", base.Add(-time.Second), base.Add(time.Minute), []float64{30, 40})
}

func TestReportQueueFullReturnsError(t *testing.T) {
	ctx := context.Background()
	useReportTestStore(t, nil)
	worker := &reportBatchWorker{
		queue:    make(chan v2.Report, 1),
		requests: make(chan reportBatchRequest, 1),
		done:     make(chan struct{}),
	}
	worker.queue <- v2.Report{UUID: "already-queued"}
	reportBatcherMu.Lock()
	reportBatcher = worker
	reportBatcherMu.Unlock()
	t.Cleanup(func() {
		reportBatcherMu.Lock()
		if reportBatcher == worker {
			reportBatcher = nil
		}
		reportBatcherMu.Unlock()
	})

	report := v2.Report{
		UUID:      "realtime-node",
		UpdatedAt: time.Now().UTC(),
	}
	_, err := WriteReport(ctx, report)
	if !errors.Is(err, ErrReportBatchQueueFull) {
		t.Fatalf("queue-full error = %v, want %v", err, ErrReportBatchQueueFull)
	}
}

func TestRecordReconstructionUsesMetricSpecificAggregation(t *testing.T) {
	ctx := context.Background()
	s := useReportTestStore(t, nil)
	base := time.Now().UTC().Truncate(time.Minute)
	entityID := "node-aggregation"
	points := []metric.Point{
		{MetricName: MetricCPU, EntityID: entityID, Timestamp: base.Add(time.Second), Value: 10},
		{MetricName: MetricCPU, EntityID: entityID, Timestamp: base.Add(2 * time.Second), Value: 30},
		{MetricName: MetricNetTotalUp, EntityID: entityID, Timestamp: base.Add(time.Second), Value: 100},
		{MetricName: MetricNetTotalUp, EntityID: entityID, Timestamp: base.Add(2 * time.Second), Value: 200},
		{MetricName: MetricTrafficUp, EntityID: entityID, Timestamp: base.Add(time.Second), Value: 10},
		{MetricName: MetricTrafficUp, EntityID: entityID, Timestamp: base.Add(2 * time.Second), Value: 20},
	}
	if err := s.WriteBatch(ctx, points); err != nil {
		t.Fatalf("write points: %v", err)
	}

	records, err := GetRecordsByClientAndTime(ctx, entityID, base, base.Add(time.Hour))
	if err != nil {
		t.Fatalf("reconstruct records: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %#v, want one bucket", records)
	}
	if records[0].Cpu != 20 || records[0].NetTotalUp != 200 || records[0].TrafficUp != 30 {
		t.Fatalf("unexpected aggregation result: %#v", records[0])
	}
}

func TestTrafficCounterDelta(t *testing.T) {
	const (
		oneGB = int64(1_000_000_000)
		oneTB = int64(1_000_000_000_000)
	)
	tests := []struct {
		name     string
		current  int64
		previous int64
		want     int64
	}{
		{name: "previous zero", current: 120, previous: 0, want: 120},
		{name: "monotonic counter", current: 250, previous: 200, want: 50},
		{name: "unchanged counter", current: 100, previous: 100, want: 0},
		{name: "counter reset", current: 15, previous: 250, want: 15},
		{name: "negative current", current: -1, previous: 100, want: 0},
		{name: "negative previous", current: 15, previous: -1, want: 0},
		{name: "tb-scale monotonic", current: 2*oneTB + 5*oneGB, previous: 2 * oneTB, want: 5 * oneGB},
		{name: "32-bit wrap leftover", current: 800_000_000, previous: 3*oneGB + 900_000_000, want: 800_000_000},
		{name: "reboot leftover below cap", current: 8 * oneGB, previous: 2 * oneTB, want: 8 * oneGB},
		{name: "tb-scale tiny dip", current: 2*oneTB - 100, previous: 2 * oneTB, want: 0},
		{name: "tb-scale false reset", current: 2*oneTB + 5*oneGB - 1000, previous: 2*oneTB + 5*oneGB, want: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := TrafficCounterDelta(test.current, test.previous); got != test.want {
				t.Fatalf("TrafficCounterDelta(%d, %d) = %d, want %d", test.current, test.previous, got, test.want)
			}
		})
	}
}

func TestWriteReportRebasesTrafficAfterAgentRestart(t *testing.T) {
	ctx := context.Background()
	s := useReportTestStore(t, nil)
	base := time.Now().UTC().Truncate(time.Minute).Add(5 * time.Second)
	report := v2.Report{
		UUID:      "restarted-node",
		UpdatedAt: base,
		Uptime:    1000,
		Network:   v2.NetworkReport{TotalUp: 100, TotalDown: 200, CycleUp: 40, CycleDown: 60},
	}
	if _, err := WriteReport(ctx, report); err != nil {
		t.Fatalf("write first report: %v", err)
	}

	report.UpdatedAt = base.Add(3 * time.Second)
	report.Uptime = 1003
	report.Network.TotalUp = 150
	report.Network.TotalDown = 260
	if _, err := WriteReport(ctx, report); err != nil {
		t.Fatalf("write continuous report: %v", err)
	}

	report.UpdatedAt = base.Add(6 * time.Second)
	report.Uptime = 1
	report.Network.TotalUp = 155
	report.Network.TotalDown = 265
	if _, err := WriteReport(ctx, report); err != nil {
		t.Fatalf("write report after agent restart: %v", err)
	}

	report.UpdatedAt = base.Add(9 * time.Second)
	report.Uptime = 4
	report.Network.TotalUp = 180
	report.Network.TotalDown = 300
	if _, err := WriteReport(ctx, report); err != nil {
		t.Fatalf("write report after new baseline: %v", err)
	}

	// The kernel counter drops at the restart (1 → 155 after the uptime was reset), and the raw value is what
	// is stored — the panel no longer derives anything from it here, so nothing has to be "rebased".
	assertMetricValues(t, s, MetricNetTotalUp, report.UUID, base.Add(-time.Second), base.Add(time.Minute), []float64{100, 150, 155, 180})
	assertMetricValues(t, s, MetricTrafficUp, report.UUID, base.Add(-time.Second), base.Add(time.Minute), []float64{40, 40, 40, 40})
	assertMetricValues(t, s, MetricNetTotalDown, report.UUID, base.Add(-time.Second), base.Add(time.Minute), []float64{200, 260, 265, 300})
	assertMetricValues(t, s, MetricTrafficDown, report.UUID, base.Add(-time.Second), base.Add(time.Minute), []float64{60, 60, 60, 60})
}

func TestWriteReportCountsTBScaleTrafficDeltas(t *testing.T) {
	const (
		oneGB = int64(1_000_000_000)
		oneTB = int64(1_000_000_000_000)
	)
	ctx := context.Background()
	s := useReportTestStore(t, nil)
	base := time.Now().UTC().Truncate(time.Minute).Add(5 * time.Second)
	report := v2.Report{
		UUID:      "tb-node",
		UpdatedAt: base,
		Uptime:    10_000,
		Network:   v2.NetworkReport{TotalUp: 2 * oneTB, TotalDown: 3 * oneTB},
	}
	if _, err := WriteReport(ctx, report); err != nil {
		t.Fatalf("write first report: %v", err)
	}

	report.UpdatedAt = base.Add(3 * time.Second)
	report.Uptime = 10_003
	report.Network.TotalUp = 2*oneTB + 5*oneGB
	report.Network.TotalDown = 3*oneTB + 7*oneGB
	if _, err := WriteReport(ctx, report); err != nil {
		t.Fatalf("write growing report: %v", err)
	}

	assertMetricValues(t, s, MetricNetTotalUp, report.UUID, base.Add(-time.Second), base.Add(time.Minute), []float64{float64(2 * oneTB), float64(2*oneTB + 5*oneGB)})
	assertMetricValues(t, s, MetricNetTotalDown, report.UUID, base.Add(-time.Second), base.Add(time.Minute), []float64{float64(3 * oneTB), float64(3*oneTB + 7*oneGB)})
}

func TestWriteReportRestoresTBScaleCountersFromStore(t *testing.T) {
	const (
		oneGB = int64(1_000_000_000)
		oneTB = int64(1_000_000_000_000)
	)
	ctx := context.Background()
	policy := defaultRollupPolicy()
	s := useReportTestStore(t, &policy)
	base := time.Now().UTC().Truncate(time.Minute).Add(5 * time.Second)
	now := base.Add(45 * time.Second)
	report := v2.Report{
		UUID:      "tb-restore-node",
		UpdatedAt: base,
		Uptime:    10_000,
		Network:   v2.NetworkReport{TotalUp: 2*oneTB + 123, TotalDown: 4*oneTB + 456},
	}
	if _, err := WriteReport(ctx, report); err != nil {
		t.Fatalf("write first report: %v", err)
	}

	restoredUp, hasUp, err := latestReportCounter(ctx, s, MetricNetTotalUp, report.UUID, now)
	if err != nil {
		t.Fatalf("restore upload counter: %v", err)
	}
	if !hasUp || restoredUp != report.Network.TotalUp {
		t.Fatalf("restored upload counter = %d (has=%v), want %d", restoredUp, hasUp, report.Network.TotalUp)
	}
	restoredDown, hasDown, err := latestReportCounter(ctx, s, MetricNetTotalDown, report.UUID, now)
	if err != nil {
		t.Fatalf("restore download counter: %v", err)
	}
	if !hasDown || restoredDown != report.Network.TotalDown {
		t.Fatalf("restored download counter = %d (has=%v), want %d", restoredDown, hasDown, report.Network.TotalDown)
	}

	if _, err := s.Compact(ctx, now); err != nil {
		t.Fatalf("compact reports: %v", err)
	}
	deleteReportTrafficState(report.UUID)
	report.UpdatedAt = now
	report.Uptime = 10_045
	report.Network.TotalUp = 2*oneTB + 123 + 5*oneGB
	report.Network.TotalDown = 4*oneTB + 456 + 7*oneGB
	if _, err := WriteReport(ctx, report); err != nil {
		t.Fatalf("write after restoring tb-scale baseline: %v", err)
	}
	assertMetricValues(t, s, MetricNetTotalUp, report.UUID, now.Add(-time.Second), now.Add(time.Second), []float64{float64(2*oneTB + 123 + 5*oneGB)})
	assertMetricValues(t, s, MetricNetTotalDown, report.UUID, now.Add(-time.Second), now.Add(time.Second), []float64{float64(4*oneTB + 456 + 7*oneGB)})
}

func TestWriteReportCountsTrafficAfterHighRateCounterWrap(t *testing.T) {
	const oneGB = int64(1_000_000_000)
	ctx := context.Background()
	s := useReportTestStore(t, nil)
	base := time.Now().UTC().Truncate(time.Minute).Add(5 * time.Second)
	report := v2.Report{
		UUID:      "wrap-node",
		UpdatedAt: base,
		Uptime:    10_000,
		Network:   v2.NetworkReport{TotalUp: 3*oneGB + 900_000_000, TotalDown: 3*oneGB + 800_000_000},
	}
	if _, err := WriteReport(ctx, report); err != nil {
		t.Fatalf("write first report: %v", err)
	}

	report.UpdatedAt = base.Add(3 * time.Second)
	report.Uptime = 10_003
	report.Network.TotalUp = 800_000_000
	report.Network.TotalDown = 900_000_000
	if _, err := WriteReport(ctx, report); err != nil {
		t.Fatalf("write wrapped report: %v", err)
	}

	// Both reports are stored raw, including the second whose counter is *lower* than the first: the kernel
	// counter wrapped, and the raw value is what `net.total.*` records. Nothing here fabricates a delta from
	// the wrap — that fabrication is what produced phantom traffic when the two meanings shared a field.
	assertMetricValues(t, s, MetricNetTotalUp, report.UUID, base.Add(-time.Second), base.Add(time.Minute), []float64{float64(3*oneGB + 900_000_000), 800_000_000})
	assertMetricValues(t, s, MetricNetTotalDown, report.UUID, base.Add(-time.Second), base.Add(time.Minute), []float64{float64(3*oneGB + 800_000_000), 900_000_000})
}

func TestWriteReportIgnoresTinyDipOfTBScaleCounter(t *testing.T) {
	const (
		oneGB = int64(1_000_000_000)
		oneTB = int64(1_000_000_000_000)
	)
	ctx := context.Background()
	s := useReportTestStore(t, nil)
	base := time.Now().UTC().Truncate(time.Minute).Add(5 * time.Second)
	report := v2.Report{
		UUID:      "tb-jitter-node",
		UpdatedAt: base,
		Uptime:    10_000,
		Network:   v2.NetworkReport{TotalUp: 2 * oneTB, TotalDown: 3 * oneTB},
	}
	if _, err := WriteReport(ctx, report); err != nil {
		t.Fatalf("write first report: %v", err)
	}

	report.UpdatedAt = base.Add(3 * time.Second)
	report.Uptime = 10_003
	report.Network.TotalUp = 2*oneTB + 5*oneGB
	report.Network.TotalDown = 3*oneTB + 7*oneGB
	if _, err := WriteReport(ctx, report); err != nil {
		t.Fatalf("write growing report: %v", err)
	}

	report.UpdatedAt = base.Add(6 * time.Second)
	report.Uptime = 10_006
	report.Network.TotalUp = 2*oneTB + 5*oneGB - 1000
	report.Network.TotalDown = 3*oneTB + 7*oneGB - 2000
	if _, err := WriteReport(ctx, report); err != nil {
		t.Fatalf("write jitter report: %v", err)
	}

	report.UpdatedAt = base.Add(9 * time.Second)
	report.Uptime = 10_009
	report.Network.TotalUp = 2*oneTB + 6*oneGB
	report.Network.TotalDown = 3*oneTB + 8*oneGB
	if _, err := WriteReport(ctx, report); err != nil {
		t.Fatalf("write recovered report: %v", err)
	}

	// Raw at every step, including the dip and the recovery. The point of the test is that a jittering counter
	// is now merely *stored*: the old code had to decide whether a dip was a reset or a wrap and got it wrong
	// in both directions (counting a dip as a jump, or discarding a real wrap), which is what the phantom
	// samples were made of. `traffic.*` no longer participates in that decision at all.
	assertMetricValues(t, s, MetricNetTotalUp, report.UUID, base.Add(-time.Second), base.Add(time.Minute), []float64{float64(2 * oneTB), float64(2*oneTB + 5*oneGB), float64(2*oneTB + 5*oneGB - 1000), float64(2*oneTB + 6*oneGB)})
	assertMetricValues(t, s, MetricNetTotalDown, report.UUID, base.Add(-time.Second), base.Add(time.Minute), []float64{float64(3 * oneTB), float64(3*oneTB + 7*oneGB), float64(3*oneTB + 7*oneGB - 2000), float64(3*oneTB + 8*oneGB)})
}

func TestWriteReportNormalizesReceiveTimeToUTC(t *testing.T) {
	ctx := context.Background()
	s := useReportTestStore(t, nil)
	local := time.FixedZone("UTC+8", 8*60*60)
	receiveTime := time.Now().In(local).Add(-10 * time.Second)
	report := v2.Report{
		UUID:      "utc-report",
		UpdatedAt: receiveTime,
		CPU:       v2.CPUReport{Usage: 10},
		Network:   v2.NetworkReport{TotalUp: 1, TotalDown: 2},
	}

	saved, err := WriteReport(ctx, report)
	if err != nil {
		t.Fatalf("write report: %v", err)
	}
	if !saved.UpdatedAt.Equal(receiveTime) || saved.UpdatedAt.Location() != time.UTC {
		t.Fatalf("saved receive time = %s (%s), want UTC", saved.UpdatedAt, saved.UpdatedAt.Location())
	}
	points, err := s.Query(ctx, metric.Query{
		MetricName: MetricCPU,
		EntityID:   report.UUID,
		Start:      receiveTime.Add(-time.Nanosecond),
		End:        receiveTime.Add(time.Nanosecond),
	})
	if err != nil {
		t.Fatalf("query stored point: %v", err)
	}
	if len(points) != 1 || points[0].Timestamp.Location() != time.UTC || points[0].Timestamp.UnixMilli() != receiveTime.UnixMilli() {
		t.Fatalf("stored points = %#v, want one UTC millisecond point", points)
	}
}

func assertMetricValues(t *testing.T, s *metric.Store, metricName, entityID string, start, end time.Time, want []float64) {
	t.Helper()
	points, err := s.Query(context.Background(), metric.Query{
		MetricName: metricName,
		EntityID:   entityID,
		Start:      start,
		End:        end,
		Order:      metric.OrderAsc,
	})
	if err != nil {
		t.Fatalf("query %s: %v", metricName, err)
	}
	if len(points) != len(want) {
		t.Fatalf("%s point count = %d, want %d: %#v", metricName, len(points), len(want), points)
	}
	for i := range want {
		if points[i].Value != want[i] {
			t.Fatalf("%s point %d = %v, want %v", metricName, i, points[i].Value, want[i])
		}
	}
}

func assertMetricAggregate(t *testing.T, s *metric.Store, metricName, entityID string, start, end time.Time, aggregation metric.Aggregation, want float64, wantCount int) {
	t.Helper()
	points, err := s.Series(context.Background(), metric.AggregateQuery{
		Query:       metric.Query{MetricName: metricName, EntityID: entityID, Start: start, End: end},
		Aggregation: aggregation, Interval: time.Minute, PreserveSeries: true,
	}, end)
	if err != nil {
		t.Fatalf("aggregate %s: %v", metricName, err)
	}
	if len(points) != 1 || points[0].Value != want || points[0].Count != wantCount {
		t.Fatalf("aggregate %s = %#v, want value=%v count=%d", metricName, points, want, wantCount)
	}
}
