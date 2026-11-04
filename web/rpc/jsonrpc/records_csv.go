package jsonrpc

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Aone2233/nekomari/internal/metricstore"
	"github.com/Aone2233/nekomari/pkg/metric"
	"github.com/Aone2233/nekomari/pkg/rpc"
	"github.com/gin-gonic/gin"
)

const (
	defaultCsvMaxRows = 100_000
	hardCsvMaxRows    = 250_000
	csvFlushEvery     = 1024
	csvWriteDeadline  = 30 * time.Second
	csvExportCapacity = 2
)

// Exports share the chart memory budget but cannot occupy all chart slots.
var csvExportSlots = make(chan struct{}, csvExportCapacity)

// Each row is one actual metric-store bucket, not a reconstructed agent report.
// In particular, missing metrics do not become zero-valued columns. The bucket
// interval and aggregation travel with each row so callers can distinguish
// last ping readings and cumulative counters from averages. Values are numeric.
var recordCsvColumns = []string{
	"bucket_start_utc", "bucket_interval_seconds", "metric", "aggregation", "client",
	"task_id", "ping_type", "family", "role", "value", "sample_count", "tags_json",
}

type recordCsvMetric struct {
	name        string
	aggregation metric.Aggregation
}

var loadCsvMetrics = []recordCsvMetric{
	{metricstore.MetricCPU, metric.AggAvg},
	{metricstore.MetricGPU, metric.AggAvg},
	{metricstore.MetricRAM, metric.AggAvg},
	{metricstore.MetricSwap, metric.AggAvg},
	{metricstore.MetricLoad, metric.AggAvg},
	{metricstore.MetricDisk, metric.AggAvg},
	{metricstore.MetricNetIn, metric.AggAvg},
	{metricstore.MetricNetOut, metric.AggAvg},
	{metricstore.MetricNetTotalUp, metric.AggLast},
	{metricstore.MetricNetTotalDown, metric.AggLast},
	{metricstore.MetricTrafficUp, metric.AggLast},
	{metricstore.MetricTrafficDown, metric.AggLast},
	{metricstore.MetricProcess, metric.AggAvg},
	{metricstore.MetricConnections, metric.AggAvg},
	{metricstore.MetricConnectionsUDP, metric.AggAvg},
	{metricstore.MetricBackupAge, metric.AggLast},
	{metricstore.MetricBackupOK, metric.AggLast},
}

var loadCsvMetricsByType = map[string][]recordCsvMetric{
	"cpu":         {{metricstore.MetricCPU, metric.AggAvg}},
	"gpu":         {{metricstore.MetricGPU, metric.AggAvg}},
	"ram":         {{metricstore.MetricRAM, metric.AggAvg}},
	"swap":        {{metricstore.MetricSwap, metric.AggAvg}},
	"load":        {{metricstore.MetricLoad, metric.AggAvg}},
	"disk":        {{metricstore.MetricDisk, metric.AggAvg}},
	"network":     {{metricstore.MetricNetIn, metric.AggAvg}, {metricstore.MetricNetOut, metric.AggAvg}, {metricstore.MetricNetTotalUp, metric.AggLast}, {metricstore.MetricNetTotalDown, metric.AggLast}},
	"process":     {{metricstore.MetricProcess, metric.AggAvg}},
	"connections": {{metricstore.MetricConnections, metric.AggAvg}, {metricstore.MetricConnectionsUDP, metric.AggAvg}},
}

type csvWindow struct {
	start time.Time
	end   time.Time // exclusive
}

// csvQueryWindow accepts either an explicit RFC3339 window or hours, never both.
// Rollups cannot be split at arbitrary boundaries: the export contains complete
// buckets wholly inside this half-open window, excluding partial edge buckets.
func csvQueryWindow(c *gin.Context, now time.Time) (csvWindow, error) {
	query := c.Request.URL.Query()
	_, hasHours := query["hours"]
	_, hasStart := query["start"]
	_, hasEnd := query["end"]
	for _, key := range []string{"hours", "start", "end"} {
		if values, ok := query[key]; ok && len(values) != 1 {
			return csvWindow{}, fmt.Errorf("%s must appear once", key)
		}
	}
	var window csvWindow
	if hasStart || hasEnd {
		if hasHours || !hasStart || !hasEnd {
			return csvWindow{}, fmt.Errorf("provide start and end, or hours, not both")
		}
		start, err := time.Parse(time.RFC3339Nano, c.Query("start"))
		if err != nil {
			return csvWindow{}, fmt.Errorf("invalid start; expected RFC3339 with timezone")
		}
		end, err := time.Parse(time.RFC3339Nano, c.Query("end"))
		if err != nil {
			return csvWindow{}, fmt.Errorf("invalid end; expected RFC3339 with timezone")
		}
		window = csvWindow{start.UTC(), end.UTC()}
	} else {
		hours := 4
		if hasHours {
			var err error
			hours, err = strconv.Atoi(c.Query("hours"))
			if err != nil || hours <= 0 || hours > maxMetricQueryHours {
				return csvWindow{}, fmt.Errorf("hours must be between 1 and %d", maxMetricQueryHours)
			}
		}
		window = csvWindow{now.Add(-time.Duration(hours) * time.Hour).UTC(), now.UTC()}
	}
	if err := validateMetricWindow(window.start, window.end); err != nil {
		return csvWindow{}, err
	}
	return window, nil
}

func csvMaxRows(c *gin.Context) (int, bool) {
	values, found := c.Request.URL.Query()["max_rows"]
	if !found {
		return defaultCsvMaxRows, true
	}
	if len(values) != 1 {
		return 0, false
	}
	n, err := strconv.Atoi(values[0])
	return n, err == nil && n >= 1 && n <= hardCsvMaxRows
}

// LoadRecordsCSV exports retained rollups for a node or an authorized fleet.
func LoadRecordsCSV(c *gin.Context) {
	ctx := rpc.NewContextWithMeta(c.Request.Context(), buildContextMeta(c))
	uuid := c.Query("uuid")
	window, err := csvQueryWindow(c, time.Now().UTC())
	if err != nil {
		csvRespondError(c, http.StatusBadRequest, err.Error())
		return
	}
	maxRows, ok := csvMaxRows(c)
	if !ok {
		csvRespondError(c, http.StatusBadRequest, fmt.Sprintf("max_rows must be between 1 and %d", hardCsvMaxRows))
		return
	}
	loadType := c.Query("load_type")
	metrics := loadCsvMetrics
	if loadType != "" && loadType != "all" {
		var known bool
		metrics, known = loadCsvMetricsByType[loadType]
		if !known {
			csvRespondError(c, http.StatusBadRequest, "unsupported load_type")
			return
		}
	}
	loggedIn := isLoginFromCtx(ctx)
	if uuid == "" && !loggedIn {
		csvRespondError(c, http.StatusBadRequest, "UUID is required")
		return
	}
	hidden, err := clientVisibility()
	if err != nil {
		csvRespondError(c, http.StatusInternalServerError, "Failed to retrieve client information")
		return
	}
	if uuid != "" && !clientHistoryReadable(hidden, loggedIn, uuid) {
		writeRecordBucketsCSV(c, "load", nil, window)
		return
	}
	exportRecordBuckets(c, ctx, uuid, nil, metrics, hidden, loggedIn, maxRows, window, "load")
}

// PingRecordsCSV preserves task/protocol/family/role as distinct series.
func PingRecordsCSV(c *gin.Context) {
	ctx := rpc.NewContextWithMeta(c.Request.Context(), buildContextMeta(c))
	uuid, task := c.Query("uuid"), c.Query("task_id")
	tags := map[string]string(nil)
	if task != "" {
		id, err := strconv.ParseUint(task, 10, 32)
		if err != nil || id == 0 {
			csvRespondError(c, http.StatusBadRequest, "task_id must be a positive integer")
			return
		}
		tags = map[string]string{"task_id": strconv.FormatUint(id, 10)}
	}
	if uuid == "" && len(tags) == 0 {
		csvRespondError(c, http.StatusBadRequest, "UUID or task_id is required")
		return
	}
	window, err := csvQueryWindow(c, time.Now().UTC())
	if err != nil {
		csvRespondError(c, http.StatusBadRequest, err.Error())
		return
	}
	maxRows, ok := csvMaxRows(c)
	if !ok {
		csvRespondError(c, http.StatusBadRequest, fmt.Sprintf("max_rows must be between 1 and %d", hardCsvMaxRows))
		return
	}
	hidden, err := clientVisibility()
	if err != nil {
		csvRespondError(c, http.StatusInternalServerError, "Failed to retrieve client information")
		return
	}
	loggedIn := isLoginFromCtx(ctx)
	if uuid != "" && !clientHistoryReadable(hidden, loggedIn, uuid) {
		writeRecordBucketsCSV(c, "ping", nil, window)
		return
	}
	exportRecordBuckets(c, ctx, uuid, tags,
		[]recordCsvMetric{{metricstore.MetricPingLatency, metric.AggLast}},
		hidden, loggedIn, maxRows, window, "ping")
}

type recordCsvRow struct {
	point       metric.AggregatePoint
	aggregation metric.Aggregation
	interval    time.Duration
	tagIdentity string
}

func exportRecordBuckets(c *gin.Context, ctx context.Context, uuid string, tags map[string]string, metrics []recordCsvMetric, hidden map[string]bool, loggedIn bool, maxRows int, window csvWindow, kind string) {
	if err := acquireQuerySlot(ctx, csvExportSlots, queryQueueWait); err != nil {
		csvRespondError(c, http.StatusServiceUnavailable, "export capacity is saturated; retry later")
		return
	}
	defer func() { <-csvExportSlots }()
	budgetCtx, release, rpcErr := queryBudget(ctx, "csv:"+kind+"Records")
	if rpcErr != nil {
		csvRespondError(c, rpcErrorHTTPStatus(rpcErr.Code), rpcErr.Message)
		return
	}
	defer release()
	store := metricstore.GetStore()
	if store == nil {
		csvRespondError(c, http.StatusServiceUnavailable, "metric store not initialized")
		return
	}
	now := time.Now().UTC()
	interval := store.CompatibleSeriesInterval(window.start, now, metricstore.DefaultRollupFinestTier)
	alignedStart := window.start.Truncate(interval)
	if alignedStart.Before(window.start) {
		alignedStart = alignedStart.Add(interval)
	}
	if alignedStart.Add(interval).After(window.end) {
		writeRecordBucketsCSV(c, kind, nil, window)
		return
	}
	specs := make([]metric.BatchSeriesSpec, 0, len(metrics))
	for _, item := range metrics {
		specs = append(specs, metric.BatchSeriesSpec{MetricName: item.name,
			Aggregations: []metric.Aggregation{item.aggregation}, Interval: interval,
			PreserveSeries: true, AlignedStart: true})
	}
	var entities []string
	if uuid != "" {
		entities = []string{uuid}
	}
	result, err := store.SeriesBatch(budgetCtx, metric.BatchSeriesQuery{
		Specs: specs, EntityIDs: entities, Tags: tags, Start: alignedStart,
		End: window.end, Order: metric.OrderAsc,
	}, now)
	if err != nil {
		if errors.Is(err, metric.ErrReadBudget) {
			csvRespondError(c, http.StatusRequestEntityTooLarge, "metric read budget exceeded; narrow the window")
			return
		}
		csvRespondError(c, http.StatusInternalServerError, "Failed to fetch record buckets: "+err.Error())
		return
	}
	rows := make([]recordCsvRow, 0)
	for _, item := range metrics {
		if result.Resolutions[item.name] <= 0 {
			continue
		}
		for _, point := range result.Values[item.name][item.aggregation] {
			// Read queries include overlapping end buckets; do not attribute
			// data outside the requested interval to the export.
			if point.Bucket.Before(window.start) || point.Bucket.Add(interval).After(window.end) ||
				!clientHistoryReadable(hidden, loggedIn, point.EntityID) {
				continue
			}
			tagIdentity, _ := json.Marshal(point.Tags)
			if point.Tags == nil {
				tagIdentity = []byte("{}")
			}
			rows = append(rows, recordCsvRow{point: point, aggregation: item.aggregation, interval: interval, tagIdentity: string(tagIdentity)})
			if len(rows) > maxRows {
				csvRespondError(c, http.StatusRequestEntityTooLarge, fmt.Sprintf("record count exceeds max_rows %d; narrow the window", maxRows))
				return
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i].point, rows[j].point
		if !a.Bucket.Equal(b.Bucket) {
			return a.Bucket.Before(b.Bucket)
		}
		if a.EntityID != b.EntityID {
			return a.EntityID < b.EntityID
		}
		if a.MetricName != b.MetricName {
			return a.MetricName < b.MetricName
		}
		if rows[i].tagIdentity != rows[j].tagIdentity {
			return rows[i].tagIdentity < rows[j].tagIdentity
		}
		return a.Value < b.Value
	})
	writeRecordBucketsCSV(c, kind, rows, window)
}

func csvRespondError(c *gin.Context, status int, message string) {
	c.Header("Cache-Control", "no-store")
	c.JSON(status, gin.H{"status": "error", "message": message})
}

func writeRecordBucketsCSV(c *gin.Context, kind string, rows []recordCsvRow, window csvWindow) {
	controller := http.NewResponseController(c.Writer)
	if controller.SetWriteDeadline(time.Now().Add(csvWriteDeadline)) == nil {
		defer controller.SetWriteDeadline(time.Time{})
	}
	filename := fmt.Sprintf("nekomari-%s-buckets-%s.csv", kind, time.Now().UTC().Format("20060102"))
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Header("Cache-Control", "no-store")
	c.Header("X-Nekomari-Window-Start", window.start.Format(time.RFC3339Nano))
	c.Header("X-Nekomari-Window-End-Exclusive", window.end.Format(time.RFC3339Nano))
	c.Header("X-Nekomari-Window-Semantics", "complete-buckets-only")
	c.Status(http.StatusOK)
	_, _ = c.Writer.WriteString("\uFEFF")
	w := csv.NewWriter(c.Writer)
	w.UseCRLF = true
	if err := w.Write(recordCsvColumns); err != nil {
		return
	}
	for i, row := range rows {
		p := row.point
		if err := w.Write([]string{
			p.Bucket.UTC().Format(time.RFC3339Nano), strconv.FormatFloat(row.interval.Seconds(), 'f', -1, 64),
			csvSpreadsheetText(p.MetricName), string(row.aggregation), csvSpreadsheetText(p.EntityID),
			csvSpreadsheetText(p.Tags["task_id"]), csvSpreadsheetText(p.Tags["protocol"]),
			csvSpreadsheetText(p.Tags["family"]), csvSpreadsheetText(p.Tags["role"]),
			strconv.FormatFloat(p.Value, 'f', -1, 64), strconv.Itoa(p.Count), row.tagIdentity,
		}); err != nil {
			return
		}
		if (i+1)%csvFlushEvery == 0 {
			w.Flush()
			if w.Error() != nil {
				return
			}
		}
	}
	w.Flush()
}

// Escaping quotes is not enough for spreadsheet applications, which evaluate
// formula-looking cells even when quoted. Numeric metric values remain numeric.
func csvSpreadsheetText(value string) string {
	trimmed := strings.TrimLeft(value, " ")
	if trimmed != "" && strings.ContainsRune("\t\r\n=+-@", rune(trimmed[0])) {
		return "'" + value
	}
	return value
}
