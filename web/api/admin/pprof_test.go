package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// A preview that exceeds the size limit must still return the part that fits, with a notice that says so.
//
// ## The defect
//
// `pprofPreviewBuffer` carried a comment saying it exists to "keep automatic browser previews bounded", and
// its `Write` returned an error when the limit was reached. That error travelled up through `collectPprof`
// and the handler turned it into **HTTP 413 with no body at all** — so the panel showed
//
//     profile text preview is too large; download the pprof file instead
//
// and nothing else. The intent was a bounded preview; the behaviour was no preview.
//
// It is not a rare edge either. Measured on a running panel, `heap` and `allocs` are ~70 KB as binary
// profiles and over the 1 MiB limit as `debug=1` text, so the two profiles an operator most wants to look at
// were the two that could not be viewed. The text form is a debug dump of the serialised protobuf, and
// truncating it mid-sample-list loses the largest entries — which is why the notice has to be explicit and
// why the binary download remains the answer for real analysis.
//
// So: truncation is the *designed* behaviour and must not be an error. What must be visible is that the
// content is incomplete.

func TestPreviewBufferKeepsThePartThatFits(t *testing.T) {
	buffer := &pprofPreviewBuffer{limit: 10}

	written, err := buffer.Write([]byte("0123456789abcdef"))
	if err != nil {
		t.Fatalf("writing more than the limit returned an error: %v\n"+
			"The buffer's purpose is to bound the preview; exceeding the bound is expected, not a failure.", err)
	}
	// The full length is reported **on purpose**, and this assertion exists so it is not "corrected" later. A
	// short write would be treated as `io.ErrShortWrite` by the profile writer's own `WriteTo` and abort the
	// collection at the first chunk past the limit, discarding everything after it. What was dropped is
	// recorded in `Truncated` instead.
	if written != 16 {
		t.Errorf("Write reported %d bytes accepted, want the full 16: a short write makes the collector stop "+
			"rather than continue into a bounded buffer, which is what truncation is for", written)
	}
	if got := buffer.String(); got != "0123456789" {
		t.Errorf("buffer holds %q, want the first 10 bytes", got)
	}
	if !buffer.Truncated() {
		t.Error("Truncated() is false after dropping 6 bytes, so the caller cannot say the preview is partial")
	}
}

// A preview that fits is not marked truncated, or every preview would carry a warning.
func TestPreviewBufferKnowsWhenItFits(t *testing.T) {
	buffer := &pprofPreviewBuffer{limit: 10}
	if _, err := buffer.Write([]byte("short")); err != nil {
		t.Fatalf("a write within the limit failed: %v", err)
	}
	if buffer.Truncated() {
		t.Error("Truncated() is true for content that fits")
	}
}

// Exactly filling the limit is not truncation. The boundary is where an off-by-one would hide: content of
// exactly `limit` bytes fits, and only the next byte is dropped.
func TestPreviewBufferBoundary(t *testing.T) {
	for _, tc := range []struct {
		name        string
		size        int
		wantTrunc   bool
		wantContent int
	}{
		{"one below", 9, false, 9},
		{"exactly", 10, false, 10},
		{"one above", 11, true, 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			buffer := &pprofPreviewBuffer{limit: 10}
			if _, err := buffer.Write(bytes.Repeat([]byte("x"), tc.size)); err != nil {
				t.Fatalf("write of %d bytes failed: %v", tc.size, err)
			}
			if got := buffer.Len(); got != tc.wantContent {
				t.Errorf("stored %d bytes, want %d", got, tc.wantContent)
			}
			if buffer.Truncated() != tc.wantTrunc {
				t.Errorf("Truncated() = %v, want %v", buffer.Truncated(), tc.wantTrunc)
			}
		})
	}
}

// Truncation written in several chunks is still detected, because the profile writer does not emit one large
// buffer — `WriteTo` streams, and a chunk boundary is where a naive check would miss.
func TestPreviewBufferDetectsTruncationAcrossWrites(t *testing.T) {
	buffer := &pprofPreviewBuffer{limit: 6}
	for _, chunk := range []string{"ab", "cd", "ef", "gh", "ij"} {
		if _, err := buffer.Write([]byte(chunk)); err != nil {
			t.Fatalf("chunk %q failed: %v", chunk, err)
		}
	}
	if got := buffer.String(); got != "abcdef" {
		t.Errorf("buffer holds %q, want abcdef", got)
	}
	if !buffer.Truncated() {
		t.Error("Truncated() is false although four bytes were dropped across later chunks")
	}
}

// A zero limit means "no limit", which is how the binary download path reuses this type. Treating zero as
// "truncate everything" would produce empty downloads.
func TestPreviewBufferWithNoLimitKeepsEverything(t *testing.T) {
	buffer := &pprofPreviewBuffer{}
	payload := bytes.Repeat([]byte("y"), 4096)
	if _, err := buffer.Write(payload); err != nil {
		t.Fatalf("unlimited write failed: %v", err)
	}
	if buffer.Len() != len(payload) {
		t.Errorf("stored %d of %d bytes; a zero limit must not truncate", buffer.Len(), len(payload))
	}
	if buffer.Truncated() {
		t.Error("Truncated() is true with no limit set")
	}
}

// The notice is what makes a partial preview honest, so it must say both things: that this is not everything,
// and what to do instead.
func TestTruncationNoticeExplainsItself(t *testing.T) {
	notice := pprofPreviewTruncationNotice(1 << 20)

	for _, want := range []string{"truncat", "download"} {
		if !strings.Contains(strings.ToLower(notice), want) {
			t.Errorf("the notice does not mention %q: %q", want, notice)
		}
	}
	// The size is stated so an operator can tell "just over" from "far over" before deciding to look at it.
	if !strings.Contains(notice, "1 MiB") && !strings.Contains(notice, "1048576") {
		t.Errorf("the notice does not state the limit: %q", notice)
	}
	// It is appended to plain text served as UTF-8, so it must not assume a markup renderer.
	if strings.Contains(notice, "<") || strings.Contains(notice, "&nbsp;") {
		t.Errorf("the notice contains markup but the response is text/plain: %q", notice)
	}
}

// Truncation is a condition the response reports, not a failure it returns.
//
// The type of the record is what this pins: `Truncated()` is the only signal, so a future change that
// reintroduces a returned error would have to delete this test and read the comment above that explains why.
func TestTruncationIsAConditionNotAFailure(t *testing.T) {
	buffer := &pprofPreviewBuffer{limit: 4}
	if _, err := buffer.Write([]byte("0123456789")); err != nil {
		t.Fatalf("exceeding the limit returned an error: %v", err)
	}
	if !buffer.Truncated() {
		t.Fatal("the buffer did not record truncation, so the handler has nothing to report")
	}
	// The content that fits is still there — that is the whole point of reporting rather than failing.
	if got := buffer.String(); got != "0123" {
		t.Errorf("buffer holds %q, want the first 4 bytes", got)
	}
}

// ---------------------------------------------------------------------------
// The diagnostic summary's runtime block: process and connection-pool readings
// ---------------------------------------------------------------------------

// pprofSummaryData runs the summary handler and returns the decoded "data" object.
func pprofSummaryData(t *testing.T) map[string]any {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/admin/pprof/summary", nil)

	pprofSummaryHandler(context)

	if recorder.Code != http.StatusOK {
		t.Fatalf("summary status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Status string         `json:"status"`
		Data   map[string]any `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("summary is not valid JSON: %v", err)
	}
	if envelope.Status != "success" {
		t.Fatalf("summary status field = %q, want success", envelope.Status)
	}
	return envelope.Data
}

// runtimeBlock pulls the runtime object out of the decoded summary.
func runtimeBlock(t *testing.T, data map[string]any) map[string]any {
	t.Helper()
	block, ok := data["runtime"].(map[string]any)
	if !ok {
		t.Fatalf("summary has no runtime object: %v", data)
	}
	return block
}

// The runtime block must carry the process readings with a stable JSON type, and the existing
// counters must still be there — the frontend renders one list, and a missing field means a blank
// row rather than an error.
func TestPprofSummaryReportsProcessReadings(t *testing.T) {
	runtimeValues := runtimeBlock(t, pprofSummaryData(t))

	if _, ok := runtimeValues["goroutines"].(float64); !ok {
		t.Errorf("runtime.goroutines is missing or not a number: %v", runtimeValues["goroutines"])
	}
	if _, ok := runtimeValues["memory"].(map[string]any); !ok {
		t.Errorf("runtime.memory is missing or not an object: %v", runtimeValues["memory"])
	}

	rss, ok := runtimeValues["rss_bytes"].(float64)
	if !ok {
		t.Fatalf("runtime.rss_bytes is missing or not a number: %v", runtimeValues["rss_bytes"])
	}
	// 0 means "unknown" on platforms without /proc/self/statm, so the assertion follows the
	// platform rather than pinning one value: on Linux this process must read its own RSS, and
	// nowhere else may it claim a reading it cannot produce.
	if runtime.GOOS == "linux" {
		if rss <= 0 {
			t.Errorf("runtime.rss_bytes = %v on linux, want the process's resident size", rss)
		}
	} else if rss != 0 {
		t.Errorf("runtime.rss_bytes = %v on %s, want 0 (unknown): only Linux provides /proc/self/statm", rss, runtime.GOOS)
	}

	uptime, ok := runtimeValues["uptime_seconds"].(float64)
	if !ok {
		t.Fatalf("runtime.uptime_seconds is missing or not a number: %v", runtimeValues["uptime_seconds"])
	}
	if uptime < 0 {
		t.Errorf("runtime.uptime_seconds = %v, want a non-negative number of seconds", uptime)
	}
}

// The pool block is reported when the main database is ready and omitted when it is not — a
// field carrying fake zeroes would tell the operator "the pool is empty" when the truth is
// "there is no pool yet".
func TestPprofSummaryReportsTheMainDatabasePool(t *testing.T) {
	runtimeValues := runtimeBlock(t, pprofSummaryData(t))

	pool, present := runtimeValues["db_pool"]
	ready := dbcore.ReadyDBInstance() != nil
	if !ready {
		if present {
			t.Errorf("runtime.db_pool is present (%v) although the main database is not initialised", pool)
		}
		return
	}
	poolValues, ok := pool.(map[string]any)
	if !ok {
		t.Fatalf("runtime.db_pool is missing or not an object while the main database is ready: %v", pool)
	}
	for _, field := range []string{"open", "in_use", "idle", "wait_count", "wait_duration_ms"} {
		value, ok := poolValues[field].(float64)
		if !ok {
			t.Errorf("db_pool.%s is missing or not a number: %v", field, poolValues[field])
			continue
		}
		if value < 0 {
			t.Errorf("db_pool.%s = %v, want a non-negative counter", field, value)
		}
	}
}

// processRSSBytes must report a reading only where it can read one.
func TestProcessRSSBytes(t *testing.T) {
	rss := processRSSBytes()
	if runtime.GOOS == "linux" {
		if rss == 0 {
			t.Error("processRSSBytes() = 0 on linux, where /proc/self/statm is available")
		}
		return
	}
	if rss != 0 {
		t.Errorf("processRSSBytes() = %d on %s, want 0 (unknown)", rss, runtime.GOOS)
	}
}

// databasePoolSnapshot maps sql.DBStats onto the response shape, and a nil handle stays nil.
func TestDatabasePoolSnapshotReadsConnectionStats(t *testing.T) {
	if got := databasePoolSnapshot(nil); got != nil {
		t.Errorf("databasePoolSnapshot(nil) = %+v, want nil", got)
	}

	db, err := gorm.Open(sqlite.Open("file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "pool.db"))), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	sqlDB.SetMaxOpenConns(2)
	sqlDB.SetMaxIdleConns(2)
	if err := sqlDB.Ping(); err != nil {
		t.Fatalf("ping test database: %v", err)
	}

	snapshot := databasePoolSnapshot(sqlDB)
	if snapshot == nil {
		t.Fatal("databasePoolSnapshot returned nil for a live pool")
	}
	if snapshot.Open < 1 {
		t.Errorf("open = %d after a query, want at least one connection", snapshot.Open)
	}
	if snapshot.InUse != 0 {
		t.Errorf("in_use = %d while no query is running", snapshot.InUse)
	}
	if snapshot.Idle < 0 || snapshot.WaitCount < 0 || snapshot.WaitDurationMS < 0 {
		t.Errorf("pool counters must be non-negative: %+v", snapshot)
	}
}

// The pool is omitted before the main database exists; once it does, the snapshot is real.
func TestMainDatabasePool(t *testing.T) {
	if dbcore.ReadyDBInstance() == nil {
		if got := mainDatabasePool(); got != nil {
			t.Errorf("mainDatabasePool() = %+v before the database is ready, want nil", got)
		}
		return
	}
	if got := mainDatabasePool(); got == nil {
		t.Fatal("mainDatabasePool() = nil although the main database is ready")
	}
}

// The JSON names are the contract frontend-nav renders against: pin them, so a rename here fails
// this test instead of silently blanking the diagnostic page.
func TestPprofDatabasePoolJSONContract(t *testing.T) {
	raw, err := json.Marshal(pprofDatabasePool{Open: 3, InUse: 1, Idle: 2, WaitCount: 4, WaitDurationMS: 5})
	if err != nil {
		t.Fatalf("marshal pool: %v", err)
	}
	var decoded map[string]float64
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("the pool object is not a flat number object: %v", err)
	}
	want := map[string]float64{
		"open":             3,
		"in_use":           1,
		"idle":             2,
		"wait_count":       4,
		"wait_duration_ms": 5,
	}
	if len(decoded) != len(want) {
		t.Errorf("db_pool has fields %v, want exactly %v", decoded, want)
	}
	for field, value := range want {
		if got, ok := decoded[field]; !ok || got != value {
			t.Errorf("db_pool.%s = %v (present: %v), want %v", field, got, ok, value)
		}
	}
}

// rss_bytes and uptime_seconds are always present (0 means unknown for RSS); db_pool exists only
// when the pool does.
func TestPprofSummaryPoolFieldIsOmittedWhenUnavailable(t *testing.T) {
	var summary pprofSummary
	summary.Runtime.RSSBytes = 123
	summary.Runtime.UptimeSeconds = 45

	raw, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("marshal summary: %v", err)
	}
	encoded := string(raw)
	if strings.Contains(encoded, "db_pool") {
		t.Errorf("a summary without a pool still encodes db_pool: %s", encoded)
	}
	for _, want := range []string{`"rss_bytes":123`, `"uptime_seconds":45`} {
		if !strings.Contains(encoded, want) {
			t.Errorf("summary does not encode %s: %s", want, encoded)
		}
	}

	summary.Runtime.DBPool = &pprofDatabasePool{Open: 1}
	raw, err = json.Marshal(summary)
	if err != nil {
		t.Fatalf("marshal summary with a pool: %v", err)
	}
	if !strings.Contains(string(raw), `"db_pool"`) {
		t.Errorf("a summary with a pool does not encode db_pool: %s", raw)
	}
}
