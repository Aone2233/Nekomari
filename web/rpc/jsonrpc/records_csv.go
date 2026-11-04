package jsonrpc

// records_csv.go
//
// Raw-record CSV exports. These are REST handlers rather than RPC methods
// because a CSV download needs its own content type and an attachment
// disposition, which the JSON bridge does not model. The rows mirror the
// public JSON endpoints — GET /api/records/load (public:getRecordsByUUID)
// and GET /api/records/ping (public:getPingRecords) — so a CSV is the same
// retained data the panel already serves, bounded by the same visibility
// rules and the same metric read budget.

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/metricstore"
	"github.com/Aone2233/nekomari/pkg/rpc"
	"github.com/gin-gonic/gin"
)

const (
	defaultCsvMaxRows = 100_000
	hardCsvMaxRows    = 250_000
	csvFlushEvery     = 1024
)

// loadCsvColumnsAll is the full column set for load records. Raw values, one
// sample row per retained bucket; `client` is included so a fleet export for
// a logged-in administrator stays unambiguous. ram_total/swap_total/disk_total
// are deliberately absent: the metric store does not retain totals series, so
// the reconstruction always reads them as zero — exporting the columns would
// present zeros as measurements.
var loadCsvColumnsAll = []string{
	"time", "client",
	"cpu", "gpu", "ram", "swap", "load", "temp", "disk",
	"net_in", "net_out", "net_total_up", "net_total_down",
	"traffic_up", "traffic_down",
	"process", "connections", "connections_udp",
	"backup_age", "backup_ok",
}

// loadCsvColumnsByType projects load records like the load_type filter of the
// JSON endpoints. Omitted or "all" keeps every column.
var loadCsvColumnsByType = map[string][]string{
	"cpu":         {"cpu"},
	"gpu":         {"gpu"},
	"ram":         {"ram"},
	"swap":        {"swap"},
	"load":        {"load"},
	"temp":        {"temp"},
	"disk":        {"disk"},
	"network":     {"net_in", "net_out", "net_total_up", "net_total_down"},
	"process":     {"process"},
	"connections": {"connections", "connections_udp"},
}

var pingCsvColumns = []string{"time", "task_id", "client", "value", "ping_type", "family", "role"}

// LoadRecordsCSV streams raw load records as a CSV attachment. A logged-in
// administrator may omit uuid for a fleet-wide export; guests must name one
// readable node, and a hidden or unknown node yields the same header-only file
// as the JSON endpoint returns for the same probe.
func LoadRecordsCSV(c *gin.Context) {
	ctx := rpc.NewContextWithMeta(c.Request.Context(), buildContextMeta(c))
	uuid := c.Query("uuid")

	hours, ok := csvQueryHours(c)
	if !ok {
		csvRespondError(c, http.StatusBadRequest, "Invalid hours parameter")
		return
	}
	maxRows, ok := csvMaxRows(c)
	if !ok {
		csvRespondError(c, http.StatusBadRequest, fmt.Sprintf("Invalid max_rows parameter (1..%d)", hardCsvMaxRows))
		return
	}
	loadType := c.Query("load_type")
	if _, known := loadCsvColumnsByType[loadType]; !known && loadType != "" && loadType != "all" {
		csvRespondError(c, http.StatusBadRequest, "Invalid load_type parameter")
		return
	}

	hidden, err := clientVisibility()
	if err != nil {
		csvRespondError(c, http.StatusInternalServerError, "Failed to retrieve client information: "+err.Error())
		return
	}
	loggedIn := isLoginFromCtx(ctx)
	if uuid == "" {
		if !loggedIn {
			csvRespondError(c, http.StatusBadRequest, "UUID is required")
			return
		}
	} else if !clientHistoryReadable(hidden, loggedIn, uuid) {
		writeLoadRecordsCsv(c, nil, loadType, maxRows, uuid)
		return
	}

	budgetCtx, release, rpcErr := queryBudget(ctx, "csv:loadRecords")
	if rpcErr != nil {
		csvRespondError(c, rpcErrorHTTPStatus(rpcErr.Code), rpcErr.Message)
		return
	}
	defer release()

	now := time.Now().UTC()
	start := now.Add(-time.Duration(hours) * time.Hour)
	var recs []models.Record
	if uuid == "" {
		recs, err = metricstore.GetRecordsByTime(budgetCtx, start, now)
	} else {
		recs, err = metricstore.GetRecordsByClientAndTime(budgetCtx, uuid, start, now)
	}
	if err != nil {
		csvRespondError(c, http.StatusInternalServerError, "Failed to fetch records: "+err.Error())
		return
	}
	writeLoadRecordsCsv(c, recs, loadType, maxRows, uuid)
}

// PingRecordsCSV streams raw ping records as a CSV attachment. Like
// public:getPingRecords, a uuid or a task_id (or both) scopes the export;
// hidden clients are filtered out of task-scoped exports for guests.
func PingRecordsCSV(c *gin.Context) {
	ctx := rpc.NewContextWithMeta(c.Request.Context(), buildContextMeta(c))
	uuid := c.Query("uuid")
	taskID := -1
	if raw := c.Query("task_id"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			csvRespondError(c, http.StatusBadRequest, "Invalid task_id parameter")
			return
		}
		taskID = n
	}
	if uuid == "" && taskID < 0 {
		csvRespondError(c, http.StatusBadRequest, "UUID or task_id is required")
		return
	}
	hours, ok := csvQueryHours(c)
	if !ok {
		csvRespondError(c, http.StatusBadRequest, "Invalid hours parameter")
		return
	}
	maxRows, ok := csvMaxRows(c)
	if !ok {
		csvRespondError(c, http.StatusBadRequest, fmt.Sprintf("Invalid max_rows parameter (1..%d)", hardCsvMaxRows))
		return
	}

	hidden, err := clientVisibility()
	if err != nil {
		csvRespondError(c, http.StatusInternalServerError, "Failed to retrieve client information: "+err.Error())
		return
	}
	loggedIn := isLoginFromCtx(ctx)
	if uuid != "" && !clientHistoryReadable(hidden, loggedIn, uuid) {
		writePingRecordsCsv(c, nil, maxRows)
		return
	}

	budgetCtx, release, rpcErr := queryBudget(ctx, "csv:pingRecords")
	if rpcErr != nil {
		csvRespondError(c, rpcErrorHTTPStatus(rpcErr.Code), rpcErr.Message)
		return
	}
	defer release()

	now := time.Now().UTC()
	start := now.Add(-time.Duration(hours) * time.Hour)
	recs, err := metricstore.GetPingRecords(budgetCtx, uuid, taskID, start, now)
	if err != nil {
		csvRespondError(c, http.StatusInternalServerError, "Failed to fetch ping records: "+err.Error())
		return
	}

	// A hidden node's latency must not leak into a guest's task-scoped export,
	// exactly like the JSON endpoint that shares this read path.
	visible := recs[:0]
	for _, rec := range recs {
		if rec.Client != "" && !clientHistoryReadable(hidden, loggedIn, rec.Client) {
			continue
		}
		visible = append(visible, rec)
	}
	sort.Slice(visible, func(i, j int) bool { return visible[i].Time.Before(visible[j].Time) })
	writePingRecordsCsv(c, visible, maxRows)
}

func csvQueryHours(c *gin.Context) (int, bool) {
	raw := c.Query("hours")
	if raw == "" {
		raw = "4"
	}
	hours, err := strconv.Atoi(raw)
	if err != nil || hours <= 0 || hours > maxMetricQueryHours {
		return 0, false
	}
	return hours, true
}

func csvMaxRows(c *gin.Context) (int, bool) {
	raw := c.Query("max_rows")
	if raw == "" {
		return defaultCsvMaxRows, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > hardCsvMaxRows {
		return 0, false
	}
	return n, true
}

func csvRespondError(c *gin.Context, status int, message string) {
	c.JSON(status, gin.H{"status": "error", "message": message})
}

func writeLoadRecordsCsv(c *gin.Context, recs []models.Record, loadType string, maxRows int, uuid string) {
	if len(recs) > maxRows {
		csvRespondError(c, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("record count %d exceeds max_rows %d; narrow the window or raise max_rows", len(recs), maxRows))
		return
	}
	columns := loadCsvColumnsAll
	if subset, ok := loadCsvColumnsByType[loadType]; ok {
		columns = append([]string{"time", "client"}, subset...)
	}
	startCsvAttachment(c, csvFilename("load", uuid))
	writer := newCsvWriter(c)
	if err := writer.write(columns); err != nil {
		return
	}
	for i := range recs {
		rec := &recs[i]
		row := make([]string, len(columns))
		for j, column := range columns {
			row[j] = loadCsvCell(*rec, column)
		}
		if err := writer.write(row); err != nil {
			return
		}
	}
	writer.close()
}

func writePingRecordsCsv(c *gin.Context, recs []models.PingRecord, maxRows int) {
	if len(recs) > maxRows {
		csvRespondError(c, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("record count %d exceeds max_rows %d; narrow the window or raise max_rows", len(recs), maxRows))
		return
	}
	startCsvAttachment(c, csvFilename("ping", ""))
	writer := newCsvWriter(c)
	if err := writer.write(pingCsvColumns); err != nil {
		return
	}
	for i := range recs {
		rec := &recs[i]
		row := make([]string, len(pingCsvColumns))
		for j, column := range pingCsvColumns {
			row[j] = pingCsvCell(*rec, column)
		}
		if err := writer.write(row); err != nil {
			return
		}
	}
	writer.close()
}

func loadCsvCell(rec models.Record, column string) string {
	switch column {
	case "time":
		return rec.Time.UTC().Format(time.RFC3339Nano)
	case "client":
		return rec.Client
	case "cpu":
		return formatCsvFloat(float64(rec.Cpu), 32)
	case "gpu":
		return formatCsvFloat(float64(rec.Gpu), 32)
	case "ram":
		return strconv.FormatInt(rec.Ram, 10)
	case "ram_total":
		return strconv.FormatInt(rec.RamTotal, 10)
	case "swap":
		return strconv.FormatInt(rec.Swap, 10)
	case "swap_total":
		return strconv.FormatInt(rec.SwapTotal, 10)
	case "load":
		return formatCsvFloat(float64(rec.Load), 32)
	case "temp":
		return formatCsvFloat(float64(rec.Temp), 32)
	case "disk":
		return strconv.FormatInt(rec.Disk, 10)
	case "disk_total":
		return strconv.FormatInt(rec.DiskTotal, 10)
	case "net_in":
		return strconv.FormatInt(rec.NetIn, 10)
	case "net_out":
		return strconv.FormatInt(rec.NetOut, 10)
	case "net_total_up":
		return strconv.FormatInt(rec.NetTotalUp, 10)
	case "net_total_down":
		return strconv.FormatInt(rec.NetTotalDown, 10)
	case "traffic_up":
		return strconv.FormatInt(rec.TrafficUp, 10)
	case "traffic_down":
		return strconv.FormatInt(rec.TrafficDown, 10)
	case "process":
		return strconv.Itoa(rec.Process)
	case "connections":
		return strconv.Itoa(rec.Connections)
	case "connections_udp":
		return strconv.Itoa(rec.ConnectionsUdp)
	case "backup_age":
		return strconv.FormatInt(rec.BackupAge, 10)
	case "backup_ok":
		return strconv.Itoa(rec.BackupOk)
	}
	return ""
}

func pingCsvCell(rec models.PingRecord, column string) string {
	switch column {
	case "time":
		return rec.Time.UTC().Format(time.RFC3339Nano)
	case "task_id":
		return strconv.FormatUint(uint64(rec.TaskId), 10)
	case "client":
		return rec.Client
	case "value":
		// Fractional milliseconds survive here, matching public:getPingRecords;
		// a negative value is the loss marker and stays negative.
		return formatCsvFloat(pingRecordMilliseconds(rec), 64)
	case "ping_type":
		return rec.PingType
	case "family":
		return rec.Family
	case "role":
		return rec.Role
	}
	return ""
}

func formatCsvFloat(value float64, bitSize int) string {
	return strconv.FormatFloat(value, 'f', -1, bitSize)
}

func csvFilename(kind, uuid string) string {
	scope := uuid
	if scope == "" {
		scope = "fleet"
	}
	if len(scope) > 8 {
		scope = scope[:8]
	}
	return fmt.Sprintf("nekomari-%s-%s-%s.csv", kind, scope, time.Now().UTC().Format("20060102"))
}

func startCsvAttachment(c *gin.Context, filename string) {
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Header("Cache-Control", "no-store")
	c.Status(http.StatusOK)
	// UTF-8 BOM so Excel does not read the file as the local code page.
	_, _ = c.Writer.WriteString("\uFEFF")
}

type csvAttachmentWriter struct {
	w       *csv.Writer
	flusher http.Flusher
	rows    int
	closed  bool
}

func newCsvWriter(c *gin.Context) *csvAttachmentWriter {
	w := csv.NewWriter(c.Writer)
	w.UseCRLF = true
	flusher, _ := c.Writer.(http.Flusher)
	return &csvAttachmentWriter{w: w, flusher: flusher}
}

func (w *csvAttachmentWriter) write(row []string) error {
	w.rows++
	if err := w.w.Write(row); err != nil {
		return err
	}
	if w.rows%csvFlushEvery == 0 {
		w.w.Flush()
		if w.flusher != nil {
			w.flusher.Flush()
		}
	}
	return nil
}

func (w *csvAttachmentWriter) close() {
	if w.closed {
		return
	}
	w.closed = true
	w.w.Flush()
	if w.flusher != nil {
		w.flusher.Flush()
	}
}
