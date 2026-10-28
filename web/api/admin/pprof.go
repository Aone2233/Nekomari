package admin

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	runtimepprof "runtime/pprof"
	"runtime/trace"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/internal/metricstore"
	"github.com/Aone2233/nekomari/web/api"
	"github.com/Aone2233/nekomari/web/public"
	"github.com/gin-gonic/gin"
)

const (
	defaultPprofDurationSeconds = 10
	minPprofDurationSeconds     = 1
	maxPprofDurationSeconds     = 30
	maxPprofPreviewBytes        = 1 << 20
)

var (
	cpuProfileMu   sync.Mutex
	traceProfileMu sync.Mutex

	errPprofBusy        = errors.New("pprof collection is already in progress")
	errPprofUnavailable = errors.New("pprof profile is not available")
	// There was an `errPprofPreviewTooLarge` here, and a handler case that turned it into HTTP 413 with no
	// body. Both are gone: exceeding the preview limit is the expected, designed case, and it is reported by
	// `pprofPreviewBuffer.Truncated` so the response can carry the part that fits plus a notice. Leaving the
	// sentinel behind would leave a path that answers "download the file instead" to a request that could have
	// been answered with content.
)

type pprofTarget struct {
	Name     string
	Duration time.Duration
}

// pprofPreviewBuffer keeps automatic browser previews bounded. Binary profile
// downloads remain available for profiles that exceed this compact view.
//
// Truncation is the designed behaviour, not a failure. The first version returned an error from `Write` when
// the limit was reached, that error travelled up through `collectPprof`, and the handler answered **HTTP 413
// with no body** — so the panel showed "profile text preview is too large; download the pprof file instead"
// and nothing else, which is not a preview. The comment already said the intent was to keep the view bounded;
// returning nothing was the opposite of it.
//
// This is the common case rather than an edge: `heap` and `allocs` are around 70 KB as binary profiles and
// exceed the limit as `debug=1` text, so the two profiles most worth looking at were the two that could not
// be shown. `Truncated` is how the handler knows to say the content is partial.
type pprofPreviewBuffer struct {
	bytes.Buffer
	// limit of zero means unlimited, which the binary download path relies on.
	limit     int
	truncated bool
}

func (buffer *pprofPreviewBuffer) Write(data []byte) (int, error) {
	if buffer.limit <= 0 {
		return buffer.Buffer.Write(data)
	}
	remaining := buffer.limit - buffer.Len()
	if remaining <= 0 {
		// **Reports every byte as accepted, deliberately, rather than a short write.**
		//
		// The `io.Writer` contract is "how much of my data did you consume", and the standard library's
		// helpers — including `WriteTo` implementations — treat a short write as `io.ErrShortWrite` and stop.
		// Returning `remaining` would therefore abort the collection at the first chunk past the limit and
		// discard everything after it, which is the opposite of keeping a bounded preview. Nothing downstream
		// uses the returned count to size a buffer, so over-reporting is safe; the truncation is recorded in
		// `truncated` instead, where the handler reports it.
		buffer.truncated = true
		return len(data), nil
	}
	if len(data) > remaining {
		_, _ = buffer.Buffer.Write(data[:remaining])
		buffer.truncated = true
		return len(data), nil
	}
	return buffer.Buffer.Write(data)
}

// Truncated reports whether anything was dropped, so the response can say so.
func (buffer *pprofPreviewBuffer) Truncated() bool { return buffer.truncated }

// pprofPreviewTruncationNotice is appended to a preview that did not fit.
//
// It states the limit and points at the download, because a partial profile dump truncated mid-sample-list is
// not analysable — the largest entries, which are the interesting ones, are exactly what is missing. Saying so
// is the difference between a preview that is useful-with-a-caveat and one that misleads.
func pprofPreviewTruncationNotice(limit int) string {
	return fmt.Sprintf(
		"\n\n---\n[preview truncated at %s] This is the beginning of the profile, not all of it, and the largest\n"+
			"entries may be missing. Use the download button for the complete profile.\n",
		humanizeBytes(limit))
}

// humanizeBytes renders a size for a message a person reads.
func humanizeBytes(size int) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	value := float64(size)
	for _, suffix := range []string{"KiB", "MiB", "GiB"} {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.0f %s", value, suffix)
		}
	}
	return fmt.Sprintf("%.0f GiB", value/unit)
}

type pprofProfileInfo struct {
	Name     string `json:"name"`
	Endpoint string `json:"endpoint"`
	Samples  int    `json:"samples,omitempty"`
	Timed    bool   `json:"timed,omitempty"`
	Preview  string `json:"preview,omitempty"`
}

type pprofMemorySummary struct {
	HeapAlloc   uint64 `json:"heap_alloc"`
	HeapInuse   uint64 `json:"heap_inuse"`
	HeapObjects uint64 `json:"heap_objects"`
	Sys         uint64 `json:"sys"`
}

// pprofDatabasePool is a snapshot of one database/sql connection pool.
//
// 字段名直接用 sql.DBStats 的语义，前端不需要换算：open 是当前打开的连接（含空闲），
// in_use 是正被查询占用的，idle 是池里可复用的，wait_count 是「想要连接但池已满」的累计
// 次数，wait_duration_ms 是为这些等待累计花掉的毫秒数。wait_count 持续增长说明池太小、或
// 有慢查询长期占住连接 —— 诊断页要回答的正是这个问题，所以这五个字段都取累计值而不是瞬时值。
type pprofDatabasePool struct {
	Open           int   `json:"open"`
	InUse          int   `json:"in_use"`
	Idle           int   `json:"idle"`
	WaitCount      int64 `json:"wait_count"`
	WaitDurationMS int64 `json:"wait_duration_ms"`
}

// pprofMetricDatabasePool is the metric store's connection-pool snapshot. The write pool
// keeps the same five field names as db_pool — the frontend renders both rows with one
// renderer — and a dedicated read pool, when the store has one, is nested under "read".
//
// Nested rather than duplicated at the top for two reasons: the write pool must stay a
// complete, correctly-labelled reading for a reader that does not know about the read
// pool, and "no read pool" (the single-connection default) must be distinguishable from
// "a read pool with zero connections" rather than an object of zeroes.
type pprofMetricDatabasePool struct {
	pprofDatabasePool
	// Read is the dedicated read-only pool. Omitted when reads go through the write pool.
	Read *pprofDatabasePool `json:"read,omitempty"`
}

type pprofSummary struct {
	Profiles []pprofProfileInfo `json:"profiles"`
	Runtime  struct {
		Goroutines int                `json:"goroutines"`
		Memory     pprofMemorySummary `json:"memory"`
		// RSSBytes 是进程的常驻内存。读不到时是 0，含义是「未知」而不是「零字节」：
		// 目前只有 Linux 提供 /proc/self/statm，其它平台一律 0，前端据此隐藏该列。
		RSSBytes uint64 `json:"rss_bytes"`
		// UptimeSeconds 是进程启动至今的整秒数。
		UptimeSeconds int64 `json:"uptime_seconds"`
		// DBPool 是主库的连接池；主库尚未初始化（启动早期）时为 nil，字段整个省略 ——
		// 省略比给一组假的 0 更诚实，前端按「字段不存在」处理即可。
		DBPool *pprofDatabasePool `json:"db_pool,omitempty"`
		// MetricsDB is the metric store's connection pools: the serialized write pool plus,
		// when configured, the dedicated read pool under "read". Omitted when the store is
		// not initialized (or is closed), because a field of zeroes would read as "the pool
		// is empty" instead of "there is no pool".
		MetricsDB  *pprofMetricDatabasePool         `json:"metrics_db,omitempty"`
		ThemeCache public.DefaultThemeCacheSnapshot `json:"theme_cache"`
	} `json:"runtime"`
	Duration struct {
		DefaultSeconds int `json:"default_seconds"`
		MinSeconds     int `json:"min_seconds"`
		MaxSeconds     int `json:"max_seconds"`
	} `json:"duration"`
}

var runtimeProfileNames = []string{
	"allocs",
	"block",
	"goroutine",
	"heap",
	"mutex",
	"threadcreate",
}

// RegisterPprofRoutes adds diagnostic endpoints to an already administrator-
// protected route group. It deliberately uses runtime/pprof directly instead
// of net/http/pprof, which would register unauthenticated /debug/pprof routes
// on http.DefaultServeMux as a package side effect.
func RegisterPprofRoutes(group *gin.RouterGroup) {
	pprofGroup := group.Group("/pprof")
	pprofGroup.GET("/summary", pprofSummaryHandler)
	pprofGroup.GET("/profile", downloadCPUProfile)
	pprofGroup.GET("/trace", downloadTrace)
	for _, name := range runtimeProfileNames {
		pprofGroup.GET("/"+name, downloadRuntimeProfile(name))
	}
}

// processStartTime is the moment this package was initialised, which is what "how long has this
// process been up" means for a diagnostic page. Reading the real start time differs per platform
// (/proc/self/stat on Linux, GetProcessTimes on Windows) and would buy at most the few
// milliseconds between process start and package init.
var processStartTime = time.Now()

// processRSSBytes returns the resident set size of this process, or 0 when it cannot be read.
//
// Only Linux's /proc/self/statm is read: its second field is the number of resident pages, and
// multiplying by the page size gives the same figure ps/top report. No new dependency and no
// platform API — the file simply does not exist elsewhere, so the function returns 0 there. In
// the response that 0 therefore means **unknown**, not zero bytes (see the field comment and the
// frontend contract in the task); the panel hides the reading instead of showing a false zero.
func processRSSBytes() uint64 {
	raw, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(raw))
	if len(fields) < 2 {
		return 0
	}
	pages, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0
	}
	return pages * uint64(os.Getpagesize())
}

// databasePoolSnapshot turns sql.DBStats into the shape the diagnostic page renders.
//
// Only Stats() is used — no Ping, no new connection. The page exists to be readable when a slow
// query has filled the pool, and anything that waits for a connection would block it exactly
// when it is needed.
func databasePoolSnapshot(db *sql.DB) *pprofDatabasePool {
	if db == nil {
		return nil
	}
	snapshot := databasePoolStatsSnapshot(db.Stats())
	return &snapshot
}

// databasePoolStatsSnapshot maps an already-read sql.DBStats onto the response shape.
//
// Split from databasePoolSnapshot so the metric store's pools can be rendered from
// metric.Store.PoolStats(), which has already copied the counters out and must not be
// asked for a *sql.DB: exposing one would let a diagnostic handler open connections on
// a store that deliberately keeps its pools private.
func databasePoolStatsSnapshot(stats sql.DBStats) pprofDatabasePool {
	return pprofDatabasePool{
		Open:           stats.OpenConnections,
		InUse:          stats.InUse,
		Idle:           stats.Idle,
		WaitCount:      stats.WaitCount,
		WaitDurationMS: stats.WaitDuration.Milliseconds(),
	}
}

// metricStoreForPprof is a seam for tests. Production reads the process-wide store, which
// is nil until the metric store is initialized and nil again after shutdown.
var metricStoreForPprof = metricstore.GetStore

// metricDatabasePools returns the metric store's connection-pool snapshot, or nil when no
// store is initialized.
//
// It reports both pools because the metric store's write pool is deliberately serialized
// (SQLite has one writer) while reads may fan out over a dedicated WAL pool: an operator
// diagnosing a slow chart needs to see whether the query is waiting on the writer or on
// the readers. metric.Store.PoolStats() only copies sql.DBStats — no Ping, no connection —
// so reading this cannot make the page wait for the thing it is measuring.
func metricDatabasePools() *pprofMetricDatabasePool {
	store := metricStoreForPprof()
	if store == nil {
		return nil
	}
	stats := store.PoolStats()
	snapshot := &pprofMetricDatabasePool{pprofDatabasePool: databasePoolStatsSnapshot(stats.Write)}
	if stats.HasRead {
		read := databasePoolStatsSnapshot(stats.Read)
		snapshot.Read = &read
	}
	return snapshot
}

// mainDatabasePool returns the main database's connection pool, or nil before it is initialised.
//
// ReadyDBInstance deliberately does not trigger initialisation (and never calls Fatalf), so an
// early diagnostic request omits the field instead of opening the database.
//
// The metric store's pools are reported separately as metrics_db (see metricDatabasePools):
// they live inside pkg/metric's Store, which exposes a stats-only accessor rather than its
// *sql.DB, so the diagnostic page cannot open a connection on a pool it is inspecting.
func mainDatabasePool() *pprofDatabasePool {
	db := dbcore.ReadyDBInstance()
	if db == nil {
		return nil
	}
	sqlDB, err := db.DB()
	if err != nil || sqlDB == nil {
		return nil
	}
	return databasePoolSnapshot(sqlDB)
}

// pprofSummaryHandler returns the current profile inventory and inexpensive
// runtime counters for the administrator diagnostic page.
func pprofSummaryHandler(c *gin.Context) {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	response := pprofSummary{
		Profiles: make([]pprofProfileInfo, 0, len(runtimeProfileNames)+2),
	}
	response.Profiles = append(response.Profiles,
		pprofProfileInfo{
			Name:     "cpu",
			Endpoint: "/api/admin/pprof/profile",
			Timed:    true,
		},
		pprofProfileInfo{
			Name:     "trace",
			Endpoint: "/api/admin/pprof/trace",
			Timed:    true,
		},
	)
	for _, name := range runtimeProfileNames {
		profile := runtimepprof.Lookup(name)
		if profile == nil {
			continue
		}
		response.Profiles = append(response.Profiles, pprofProfileInfo{
			Name:     name,
			Endpoint: "/api/admin/pprof/" + name,
			Samples:  profile.Count(),
			Preview:  "/api/admin/pprof/" + name + "?format=text",
		})
	}

	response.Runtime.Goroutines = runtime.NumGoroutine()
	response.Runtime.RSSBytes = processRSSBytes()
	response.Runtime.UptimeSeconds = int64(time.Since(processStartTime) / time.Second)
	response.Runtime.DBPool = mainDatabasePool()
	response.Runtime.MetricsDB = metricDatabasePools()
	response.Runtime.ThemeCache = public.ThemeCacheSnapshot()
	response.Runtime.Memory = pprofMemorySummary{
		HeapAlloc:   mem.HeapAlloc,
		HeapInuse:   mem.HeapInuse,
		HeapObjects: mem.HeapObjects,
		Sys:         mem.Sys,
	}
	response.Duration.DefaultSeconds = defaultPprofDurationSeconds
	response.Duration.MinSeconds = minPprofDurationSeconds
	response.Duration.MaxSeconds = maxPprofDurationSeconds

	api.RespondSuccess(c, response)
}

// downloadCPUProfile records a bounded CPU profile. CPU profiling is process-
// global, so a concurrent collection is rejected instead of queued.
func downloadCPUProfile(c *gin.Context) {
	if !requireBinaryPprof(c) {
		return
	}
	duration, ok := pprofDuration(c)
	if !ok {
		return
	}
	downloadPprofTarget(c, pprofTarget{Name: "cpu", Duration: duration})
}

// downloadTrace records a bounded execution trace. Runtime tracing is also a
// process-global facility and therefore cannot run concurrently.
func downloadTrace(c *gin.Context) {
	if !requireBinaryPprof(c) {
		return
	}
	duration, ok := pprofDuration(c)
	if !ok {
		return
	}
	downloadPprofTarget(c, pprofTarget{Name: "trace", Duration: duration})
}

// downloadRuntimeProfile returns a runtime profile as a binary download by
// default, or as the safe debug=1 textual summary requested by the admin UI.
func downloadRuntimeProfile(name string) gin.HandlerFunc {
	return func(c *gin.Context) {
		debug, ok := pprofRuntimeDebug(c)
		if !ok {
			return
		}
		target := pprofTarget{Name: name}
		if debug > 0 {
			previewPprofTarget(c, target)
			return
		}
		downloadPprofTarget(c, target)
	}
}

func downloadPprofTarget(c *gin.Context, target pprofTarget) {
	preparePprofResponse(c, pprofFilename(target), 0)
	if err := collectPprof(c.Request.Context(), target, c.Writer, 0); err != nil && !c.Writer.Written() {
		respondPprofCollectionError(c, err)
	}
}

func previewPprofTarget(c *gin.Context, target pprofTarget) {
	buffer := &pprofPreviewBuffer{limit: maxPprofPreviewBytes}
	if err := collectPprof(c.Request.Context(), target, buffer, 1); err != nil {
		respondPprofCollectionError(c, err)
		return
	}
	preparePprofResponse(c, pprofFilename(target), 1)
	// The truncation notice goes inside the body rather than in a header: the panel renders this as plain
	// text, and a reader who sees a partial dump with no marker would reasonably treat it as complete.
	if buffer.Truncated() {
		buffer.WriteString(pprofPreviewTruncationNotice(maxPprofPreviewBytes))
	}
	c.Data(http.StatusOK, "text/plain; charset=utf-8", buffer.Bytes())
}

func collectPprof(ctx context.Context, target pprofTarget, writer io.Writer, debug int) error {
	switch target.Name {
	case "cpu":
		if !cpuProfileMu.TryLock() {
			return errPprofBusy
		}
		defer cpuProfileMu.Unlock()
		if err := runtimepprof.StartCPUProfile(writer); err != nil {
			return errPprofBusy
		}
		defer runtimepprof.StopCPUProfile()
		waitForPprofDuration(ctx, target.Duration)
		return nil
	case "trace":
		if !traceProfileMu.TryLock() {
			return errPprofBusy
		}
		defer traceProfileMu.Unlock()
		if err := trace.Start(writer); err != nil {
			return errPprofBusy
		}
		defer trace.Stop()
		waitForPprofDuration(ctx, target.Duration)
		return nil
	default:
		if !isRuntimeProfile(target.Name) {
			return errPprofUnavailable
		}
		profile := runtimepprof.Lookup(target.Name)
		if profile == nil {
			return errPprofUnavailable
		}
		return profile.WriteTo(writer, debug)
	}
}

func pprofRuntimeDebug(c *gin.Context) (int, bool) {
	format, hasFormat, valid := singlePprofQuery(c, "format")
	if !valid || (hasFormat && format != "text") {
		api.RespondError(c, http.StatusBadRequest, "format must be text when provided")
		return 0, false
	}
	debug, hasDebug, valid := singlePprofQuery(c, "debug")
	if !valid || (hasDebug && debug != "1") {
		api.RespondError(c, http.StatusBadRequest, "debug must be 1 when provided")
		return 0, false
	}
	if hasFormat || hasDebug {
		return 1, true
	}
	return 0, true
}

func requireBinaryPprof(c *gin.Context) bool {
	for _, key := range []string{"format", "debug"} {
		_, present, valid := singlePprofQuery(c, key)
		if !valid || present {
			api.RespondError(c, http.StatusBadRequest, "CPU and trace profiles only support binary downloads")
			return false
		}
	}
	return true
}

func singlePprofQuery(c *gin.Context, key string) (string, bool, bool) {
	values, present := c.GetQueryArray(key)
	if !present {
		return "", false, true
	}
	if len(values) != 1 {
		return "", true, false
	}
	return values[0], true, true
}

func isRuntimeProfile(name string) bool {
	for _, profile := range runtimeProfileNames {
		if name == profile {
			return true
		}
	}
	return false
}

func pprofFilename(target pprofTarget) string {
	if target.Name == "trace" {
		return "trace.out"
	}
	return target.Name + ".pprof"
}

func respondPprofCollectionError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, errPprofBusy):
		api.RespondError(c, http.StatusConflict, "profile collection is already in progress")
	case errors.Is(err, errPprofUnavailable):
		api.RespondError(c, http.StatusNotFound, "profile is not available")
	default:
		api.RespondError(c, http.StatusInternalServerError, "failed to collect profile")
	}
}

func pprofDuration(c *gin.Context) (time.Duration, bool) {
	values, present := c.GetQueryArray("seconds")
	if !present {
		return time.Duration(defaultPprofDurationSeconds) * time.Second, true
	}
	if len(values) != 1 {
		api.RespondError(c, http.StatusBadRequest, "seconds must be provided once")
		return 0, false
	}

	seconds, err := strconv.Atoi(values[0])
	if err != nil {
		api.RespondError(c, http.StatusBadRequest, fmt.Sprintf(
			"seconds must be an integer between %d and %d",
			minPprofDurationSeconds,
			maxPprofDurationSeconds,
		))
		return 0, false
	}
	duration, err := pprofDurationFromSeconds(seconds)
	if err != nil {
		api.RespondError(c, http.StatusBadRequest, err.Error())
		return 0, false
	}
	return duration, true
}

func pprofDurationFromSeconds(seconds int) (time.Duration, error) {
	if seconds < minPprofDurationSeconds || seconds > maxPprofDurationSeconds {
		return 0, fmt.Errorf(
			"seconds must be an integer between %d and %d",
			minPprofDurationSeconds,
			maxPprofDurationSeconds,
		)
	}
	return time.Duration(seconds) * time.Second, nil
}

func preparePprofResponse(c *gin.Context, filename string, debug int) {
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	if debug > 0 {
		c.Header("Content-Type", "text/plain; charset=utf-8")
		return
	}
	c.Header("Content-Type", "application/octet-stream")
	c.Header("Content-Disposition", `attachment; filename="`+filename+`"`)
}

func waitForPprofDuration(ctx context.Context, duration time.Duration) {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
}
