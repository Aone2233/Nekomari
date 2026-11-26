package metricstore

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	logger "github.com/Aone2233/nekomari/utils/log"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/pkg/metric"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
)

type reportTrafficState struct {
	mu sync.Mutex

	reportTrafficValues
}

type reportTrafficValues struct {
	initialized bool
	timestamp   time.Time
	hasUp       bool
	totalUp     int64
	hasDown     bool
	totalDown   int64
	epoch       string
	uptime      int64
	uptimeLast  float64
	hasUptime   bool
	restarts    float64
	bootIDHash  string
}

var reportTrafficStates sync.Map
var reportBatchWriteMu sync.Mutex

const (
	reportBatchInterval     = 3 * time.Second
	reportBatchQueueSize    = 4096
	pingBatchMaxRecords     = 512
	reportBatchWriteTimeout = 10 * time.Second
	reportBatchMaxRecords   = 256
	// restartDetectDecreaseSeconds is the smallest uptime decrease treated as
	// a node restart. Reports arrive once a minute, so five minutes of
	// regression tolerates reordered or delayed samples without swallowing a
	// real reboot, whose reading drops to roughly the elapsed time.
	restartDetectDecreaseSeconds = 300.0
)

var (
	reportBatcherMu sync.Mutex
	reportBatcher   *reportBatchWorker
	reportClosing   atomic.Bool
)

var (
	ErrReportBatchQueueFull = errors.New("metric report batch queue is full")
	ErrReportBatchStopped   = errors.New("metric report batcher is stopped")
	ErrPingBatchQueueFull   = errors.New("ping batch queue is full")
)

type reportBatchRequest struct {
	ctx  context.Context
	done chan error
	stop bool
}

type reportBatchWorker struct {
	mu        sync.Mutex
	queue     chan v2.Report
	pingQueue chan models.PingRecord
	requests  chan reportBatchRequest
	done      chan struct{}
	stopping  bool
	// Includes queued AND retrying records, so failed flushes cannot grow RAM.
	reportCount int
	pingCount   int
}

// StartReportBatcher starts the shared report writer. Exact samples are kept in
// the short raw window while the same writes update in-memory minute buckets.
func StartReportBatcher() error {
	reportBatcherMu.Lock()
	defer reportBatcherMu.Unlock()
	if reportClosing.Load() {
		return ErrReportBatchStopped
	}
	if reportBatcher != nil {
		return nil
	}
	worker := &reportBatchWorker{
		queue:     make(chan v2.Report, reportBatchQueueSize),
		pingQueue: make(chan models.PingRecord, reportBatchQueueSize),
		requests:  make(chan reportBatchRequest, 1),
		done:      make(chan struct{}),
	}
	reportBatcher = worker
	go worker.run()
	return nil
}

// QuiesceReportBatcher fences new report admissions before stopping the
// worker. Unlike StopReportBatcher alone, successful quiescence also prevents
// later writes from falling back to synchronous ingest during shutdown.
// Once set, the fence is retained even if draining fails: shutdown is not a
// reversible operation, and an unsuccessful drain must not admit more work.
func QuiesceReportBatcher(ctx context.Context) error {
	if ctx == nil {
		return errors.New("quiesce report batcher requires a context")
	}
	if err := reportAdmissions.Acquire(ctx); err != nil {
		return fmt.Errorf("wait for report admissions during shutdown: %w", err)
	}
	defer reportAdmissions.Release()
	reportBatcherMu.Lock()
	reportClosing.Store(true)
	reportBatcherMu.Unlock()
	return StopReportBatcher(ctx)
}

// StopReportBatcher stops the report writer after flushing all queued reports.
func StopReportBatcher(ctx context.Context) error {
	if ctx == nil {
		return errors.New("stop report batcher requires a context")
	}
	reportBatcherMu.Lock()
	worker := reportBatcher
	if worker == nil {
		reportBatcherMu.Unlock()
		return nil
	}
	worker.mu.Lock()
	worker.stopping = true
	worker.mu.Unlock()
	reportBatcherMu.Unlock()

	request := reportBatchRequest{ctx: ctx, done: make(chan error, 1), stop: true}
	finish := func() error {
		reportBatcherMu.Lock()
		if reportBatcher == worker {
			reportBatcher = nil
		}
		reportBatcherMu.Unlock()
		return nil
	}
	select {
	case worker.requests <- request:
	case <-worker.done:
		return finish()
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-request.done:
		if err != nil {
			return err
		}
		return finish()
	case <-worker.done:
		return finish()
	case <-ctx.Done():
		return ctx.Err()
	}
}

// FlushReportBatch synchronously flushes the current queue. It is useful for
// controlled handoff points and deterministic tests; normal operation uses the
// worker ticker and the active mode's flush interval.
func FlushReportBatch(ctx context.Context) error {
	reportBatcherMu.Lock()
	worker := reportBatcher
	reportBatcherMu.Unlock()
	if worker == nil {
		return nil
	}
	request := reportBatchRequest{ctx: ctx, done: make(chan error, 1)}
	worker.mu.Lock()
	stopping := worker.stopping
	worker.mu.Unlock()
	if stopping {
		return ErrReportBatchStopped
	}
	select {
	case worker.requests <- request:
	case <-worker.done:
		return ErrReportBatchStopped
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-request.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// WriteReport persists one agent report and adds it to in-memory minute
// summaries using the same server receive time. Traffic deltas remain summable
// after rollup.
func WriteReport(ctx context.Context, report v2.Report) (v2.Report, error) {
	if ctx == nil {
		return v2.Report{}, fmt.Errorf("report write requires a context")
	}
	// A transition or migration must not hold a credential guard open while
	// it drains the store; the Agent may retry a refused observation.
	if !reportAdmissions.TryAcquireShared() {
		return v2.Report{}, metric.ErrWriteUnavailable
	}
	defer reportAdmissions.ReleaseShared()
	if err := ctx.Err(); err != nil {
		return v2.Report{}, err
	}
	if reportClosing.Load() {
		return v2.Report{}, ErrReportBatchStopped
	}
	if !report.BoundedPayload() {
		return v2.Report{}, fmt.Errorf("report payload exceeds retained data limits")
	}
	if report.UUID == "" {
		return v2.Report{}, fmt.Errorf("report UUID is required")
	}
	if report.UpdatedAt.IsZero() {
		return v2.Report{}, fmt.Errorf("report receive time is required")
	}
	report.UpdatedAt = report.UpdatedAt.UTC()
	if GetStore() == nil {
		return v2.Report{}, fmt.Errorf("metric store not enabled")
	}

	reportBatcherMu.Lock()
	worker := reportBatcher
	reportBatcherMu.Unlock()
	if worker != nil {
		if err := worker.enqueue(ctx, report); err != nil {
			return v2.Report{}, err
		}
		return report, nil
	}

	saved, err := writeReportBatch(ctx, []v2.Report{report})
	if err != nil {
		return v2.Report{}, err
	}
	if len(saved) == 0 {
		return report, nil
	}
	return saved[0], nil
}

func (w *reportBatchWorker) enqueue(ctx context.Context, report v2.Report) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopping {
		return ErrReportBatchStopped
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if w.reportCount >= reportBatchQueueSize {
		return ErrReportBatchQueueFull
	}
	select {
	case w.queue <- report:
		w.reportCount++
		return nil
	default:
		return ErrReportBatchQueueFull
	}
}

func (w *reportBatchWorker) run() {
	ticker := time.NewTicker(reportBatchInterval)
	defer ticker.Stop()

	var pending []v2.Report
	var pendingPings []models.PingRecord
	var retryAt time.Time
	retryDelay := reportBatchInterval
	flush := func(ctx context.Context) error {
		reportsBefore, pingsBefore := len(pending), len(pendingPings)
		err := errors.Join(writePendingReports(ctx, &pending), writePendingPingRecords(ctx, &pendingPings))
		w.mu.Lock()
		w.reportCount -= reportsBefore - len(pending)
		w.pingCount -= pingsBefore - len(pendingPings)
		w.mu.Unlock()
		return err
	}
	for {
		select {
		case request := <-w.requests:
			pending = append(pending, drainReportQueue(w.queue, reportBatchQueueSize)...)
			pendingPings = append(pendingPings, drainPingQueue(w.pingQueue, reportBatchQueueSize)...)
			err := flush(request.ctx)
			if request.stop {
				if err != nil {
					logger.Errorf("metricstore", "failed to flush metric report batch during shutdown: %v", err)
					// Keep accepted records available for a later stop attempt; the
					// admission side remains stopped until a successful drain.
					request.done <- err
					continue
				}
				close(w.done)
				request.done <- err
				return
			}
			request.done <- err
		case <-ticker.C:
			w.mu.Lock()
			stopping := w.stopping
			w.mu.Unlock()
			if stopping {
				continue
			}
			if time.Now().Before(retryAt) {
				continue
			}
			pendingPings = append(pendingPings, drainPingQueue(w.pingQueue, reportBatchQueueSize)...)
			pending = append(pending, drainReportQueue(w.queue, reportBatchQueueSize)...)
			if err := flush(context.Background()); err != nil {
				retryAt = time.Now().Add(retryDelay)
				retryDelay = min(retryDelay*2, time.Minute)
				logger.Errorf("metricstore", "failed to flush metric report batch: %v", err)
			} else {
				retryAt = time.Time{}
				retryDelay = reportBatchInterval
			}
		}
	}
}

func (w *reportBatchWorker) enqueuePing(ctx context.Context, record models.PingRecord) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopping {
		return ErrReportBatchStopped
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if w.pingCount >= reportBatchQueueSize {
		return ErrPingBatchQueueFull
	}
	select {
	case w.pingQueue <- record:
		w.pingCount++
		return nil
	default:
		return ErrPingBatchQueueFull
	}
}

func drainReportQueue(queue <-chan v2.Report, limit int) []v2.Report {
	if limit <= 0 {
		return nil
	}
	capacity := len(queue)
	if capacity > limit {
		capacity = limit
	}
	if capacity == 0 {
		return nil
	}
	reports := make([]v2.Report, 0, capacity)
	for len(reports) < limit {
		select {
		case report := <-queue:
			reports = append(reports, report)
		default:
			return reports
		}
	}
	return reports
}

func drainPingQueue(queue <-chan models.PingRecord, limit int) []models.PingRecord {
	if limit <= 0 {
		return nil
	}
	capacity := len(queue)
	if capacity > limit {
		capacity = limit
	}
	if capacity == 0 {
		return nil
	}
	records := make([]models.PingRecord, 0, capacity)
	for len(records) < limit {
		select {
		case record := <-queue:
			records = append(records, record)
		default:
			return records
		}
	}
	return records
}

func writePendingReports(ctx context.Context, pending *[]v2.Report) error {
	if len(*pending) == 0 {
		return nil
	}
	batchSize := reportBatchMaxRecords
	for len(*pending) > 0 {
		if batchSize > len(*pending) {
			batchSize = len(*pending)
		}
		writeCtx, cancel := context.WithTimeout(ctx, reportBatchWriteTimeout)
		_, err := writeReportBatch(writeCtx, (*pending)[:batchSize])
		cancel()
		if err != nil {
			if errors.Is(err, metric.ErrRawHistoryUnavailable) {
				return isolatePendingReports(ctx, pending)
			}
			return err
		}
		clear((*pending)[:batchSize])
		*pending = (*pending)[batchSize:]
	}
	return nil
}

// A single ambiguous historical identity must not indefinitely block unrelated
// nodes. Keep it and later reports for the same node, preserving their order;
// neither the accepted record nor its traffic baseline may be silently skipped.
func isolatePendingReports(ctx context.Context, pending *[]v2.Report) error {
	input := *pending
	kept := make([]v2.Report, 0, len(input))
	blocked := make(map[string]struct{})
	var first error
	for i, report := range input {
		if _, found := blocked[report.UUID]; found {
			kept = append(kept, report)
			continue
		}
		writeCtx, cancel := context.WithTimeout(ctx, reportBatchWriteTimeout)
		_, err := writeReportBatch(writeCtx, []v2.Report{report})
		cancel()
		if errors.Is(err, metric.ErrRawHistoryUnavailable) {
			if first == nil {
				first = err
			}
			blocked[report.UUID] = struct{}{}
			kept = append(kept, report)
			continue
		}
		if err != nil {
			kept = append(kept, input[i:]...)
			clear(input)
			*pending = kept
			return errors.Join(first, err)
		}
	}
	clear(input)
	*pending = kept
	if first != nil {
		return fmt.Errorf("%d accepted reports waiting on ambiguous history for %d nodes: %w", len(kept), len(blocked), first)
	}
	return nil
}

func writePendingPingRecords(ctx context.Context, pending *[]models.PingRecord) error {
	for len(*pending) > 0 {
		batchSize := len(*pending)
		if batchSize > pingBatchMaxRecords {
			batchSize = pingBatchMaxRecords
		}
		writeCtx, cancel := context.WithTimeout(ctx, reportBatchWriteTimeout)
		err := writePingRecords(writeCtx, (*pending)[:batchSize])
		cancel()
		if err != nil {
			if errors.Is(err, metric.ErrRawHistoryUnavailable) {
				return isolatePendingPingRecords(ctx, pending)
			}
			return err
		}
		clear((*pending)[:batchSize])
		*pending = (*pending)[batchSize:]
	}
	return nil
}

func isolatePendingPingRecords(ctx context.Context, pending *[]models.PingRecord) error {
	type series struct {
		client, protocol, role, family string
		task                           uint
	}
	input := *pending
	kept := make([]models.PingRecord, 0, len(input))
	blocked := make(map[series]struct{})
	var first error
	for i, record := range input {
		key := series{client: record.Client, protocol: record.PingType, role: record.Role, family: record.Family, task: record.TaskId}
		if _, found := blocked[key]; found {
			kept = append(kept, record)
			continue
		}
		writeCtx, cancel := context.WithTimeout(ctx, reportBatchWriteTimeout)
		err := writePingRecords(writeCtx, []models.PingRecord{record})
		cancel()
		if errors.Is(err, metric.ErrRawHistoryUnavailable) {
			if first == nil {
				first = err
			}
			blocked[key] = struct{}{}
			kept = append(kept, record)
			continue
		}
		if err != nil {
			kept = append(kept, input[i:]...)
			clear(input)
			*pending = kept
			return errors.Join(first, err)
		}
	}
	clear(input)
	*pending = kept
	if first != nil {
		return fmt.Errorf("%d accepted ping records waiting on ambiguous history for %d series: %w", len(kept), len(blocked), first)
	}
	return nil
}

func writeReportBatch(ctx context.Context, reports []v2.Report) ([]v2.Report, error) {
	if len(reports) == 0 {
		return nil, nil
	}
	if err := storeOperations.AcquireShared(ctx); err != nil {
		return nil, fmt.Errorf("wait for metric store operation before writing reports: %w", err)
	}
	defer storeOperations.ReleaseShared()
	// Serialize baseline read/write/commit across synchronous and queued writers.
	reportBatchWriteMu.Lock()
	defer reportBatchWriteMu.Unlock()

	s := GetStore()
	if s == nil {
		return nil, fmt.Errorf("metric store not enabled")
	}

	prepared := make([]v2.Report, 0, len(reports))
	points := make([]metric.Point, 0, len(reports)*20)
	pendingStates := make(map[*reportTrafficState]reportTrafficValues)
	for _, report := range reports {
		stateValue, _ := reportTrafficStates.LoadOrStore(report.UUID, &reportTrafficState{})
		state := stateValue.(*reportTrafficState)
		values, ok := pendingStates[state]
		if !ok {
			state.mu.Lock()
			values = state.reportTrafficValues
			state.mu.Unlock()
		}
		if !values.initialized {
			totalUp, hasUp, err := latestReportCounter(ctx, s, MetricNetTotalUp, report.UUID, report.UpdatedAt)
			if err != nil {
				logger.Errorf("metricstore", "failed to restore previous upload counter for %s: %v", report.UUID, err)
			} else {
				values.totalUp = totalUp
				values.hasUp = hasUp
			}
			totalDown, hasDown, err := latestReportCounter(ctx, s, MetricNetTotalDown, report.UUID, report.UpdatedAt)
			if err != nil {
				logger.Errorf("metricstore", "failed to restore previous download counter for %s: %v", report.UUID, err)
			} else {
				values.totalDown = totalDown
				values.hasDown = hasDown
			}
			uptimeLast, hasUptime, err := latestReportValue(ctx, s, MetricUptime, report.UUID, report.UpdatedAt)
			if err != nil {
				logger.Errorf("metricstore", "failed to restore previous uptime for %s: %v", report.UUID, err)
			} else {
				values.uptimeLast = uptimeLast
				values.hasUptime = hasUptime
			}
			restarts, hasRestarts, err := latestReportValue(ctx, s, MetricRestarts, report.UUID, report.UpdatedAt)
			if err != nil {
				logger.Errorf("metricstore", "failed to restore restart count for %s: %v", report.UUID, err)
			} else if hasRestarts {
				values.restarts = restarts
			}
			values.initialized = true
			// Cache only the restored baseline before persistence. Advancing
			// counters on a failed write corrupts deltas when the batch retries.
			state.mu.Lock()
			state.reportTrafficValues = values
			state.mu.Unlock()
		}

		if !values.timestamp.IsZero() && !report.UpdatedAt.After(values.timestamp) {
			// Never invent a timestamp for a replay or an out-of-order sample.
			continue
		}
		// `traffic.*` 存的是**周期累计本身**，不再是从内核计数器推出来的增量。
		//
		// 这两个值原先由 `TrafficCounterDelta(report.Network.TotalUp, 基线)` 计算，而 `TotalUp`
		// 的语义随探针配置变化（`--month-rotate` 时是周期累计、否则是内核累计、netstatic 失败时
		// 又回退），于是"用一种口径的基线减去另一种口径的当前值"会命中重置分支，把整个累计值
		// 当成一次增量。现场：CLISP 记录过两次 41 GB 的单点读数，而该网卡自开机累计 0.29 GB。
		//
		// 探针现在分别上报两组字段，这里按各自的语义处理：
		//   * `net.total.*` —— 内核累计，单调，是唯一适合做差的对象（见下面对的基线推进）
		//   * `traffic.*`   —— 周期累计，直接存，读取方取最后值即可；求和或求差都是误用
		points = append(points, reportMetricPoints(report)...)
		points = append(points, reportIntervalPoints(report, values)...)
		// A large, durable uptime regression means the OS rebooted. Count it
		// once per detected event and rebase so the post-reboot climb does not
		// re-trigger. Skipped/out-of-order samples never reach this point.
		restartDetected := false
		hadBootEvidence := values.bootIDHash != ""
		if report.BootIDHash != "" && values.bootIDHash != "" && report.BootIDHash != values.bootIDHash {
			restartDetected = true
		}
		if report.BootIDHash != "" {
			values.bootIDHash = report.BootIDHash
		}
		if validReportUptime(report) {
			uptime := float64(report.Uptime)
			if !restartDetected && !hadBootEvidence && values.hasUptime && uptime < values.uptimeLast-restartDetectDecreaseSeconds {
				// Fallback only when boot-id evidence is unavailable.
				restartDetected = true
			}
			if restartDetected {
				values.restarts++
				points = append(points, metric.Point{
					MetricName: MetricRestarts,
					EntityID:   report.UUID,
					Timestamp:  report.UpdatedAt,
					Value:      values.restarts,
				})
			}
			if !values.hasUptime || uptime > values.uptimeLast || restartDetected {
				values.uptimeLast = uptime
			}
			values.hasUptime = true
		} else if restartDetected {
			values.restarts++
			points = append(points, metric.Point{
				MetricName: MetricRestarts,
				EntityID:   report.UUID,
				Timestamp:  report.UpdatedAt,
				Value:      values.restarts,
			})
		}
		values.timestamp = report.UpdatedAt
		values.hasUp = true
		values.totalUp = report.Network.TotalUp
		values.hasDown = true
		values.totalDown = report.Network.TotalDown
		values.epoch = report.CounterEpoch
		values.uptime = report.Uptime
		if quality, exists := report.Quality["network"]; exists && quality != "ok" {
			values.hasUp, values.hasDown = false, false
		}
		if quality, exists := report.Quality["uptime"]; exists && quality != "ok" {
			values.hasUp, values.hasDown = false, false
		}
		pendingStates[state] = values
		prepared = append(prepared, report)
	}

	if err := s.WriteBatch(ctx, points); err != nil {
		var accepted *metric.WriteAcceptedError
		if !errors.As(err, &accepted) {
			return nil, err
		}
		// Retrying would reapply a batch already owned by the store. Keep the
		// report counters in step with it and surface the maintenance failure;
		// compaction or shutdown will retry, or report an uncertain commit.
		logger.Errorf("metricstore", "report batch accepted but maintenance failed: %v", err)
	}
	for state, values := range pendingStates {
		state.mu.Lock()
		state.reportTrafficValues = values
		state.mu.Unlock()
	}

	return prepared, nil
}
