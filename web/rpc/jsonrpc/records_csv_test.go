package jsonrpc

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/internal/metricstore"
	"github.com/Aone2233/nekomari/pkg/metric"
	"github.com/Aone2233/nekomari/pkg/rpc"
	"github.com/Aone2233/nekomari/web/api"
	"github.com/gin-gonic/gin"
)

func newCsvTestRouter(principal *rpc.Principal) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	if principal != nil {
		router.Use(func(c *gin.Context) { api.SetPrincipal(c, principal); c.Next() })
	}
	router.GET("/api/records/load.csv", LoadRecordsCSV)
	router.GET("/api/records/ping.csv", PingRecordsCSV)
	return router
}

func openCsvTestStore(t *testing.T) *metric.Store {
	t.Helper()
	previous, _ := config.GetAs[string](metricstore.MetricDBDSNKey)
	if err := config.Set(metricstore.MetricDBDSNKey, filepath.Join(t.TempDir(), "metrics.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = metricstore.CloseStoreContext(context.Background())
		_ = config.Set(metricstore.MetricDBDSNKey, previous)
	})
	if err := metricstore.InitializeStore(); err != nil {
		t.Fatal(err)
	}
	return metricstore.GetStore()
}

func csvGet(t *testing.T, router *gin.Engine, path string, query url.Values) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path+"?"+query.Encode(), nil))
	return recorder
}

func parseRecordCSV(t *testing.T, recorder *httptest.ResponseRecorder) [][]string {
	t.Helper()
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/csv; charset=utf-8" {
		t.Fatalf("content type = %q", got)
	}
	body := recorder.Body.String()
	if !strings.HasPrefix(body, "\uFEFF") || !strings.Contains(body, "\r\n") {
		t.Fatalf("CSV missing BOM or CRLF: %q", body)
	}
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(body, "\uFEFF"))).ReadAll()
	if err != nil && err != io.EOF {
		t.Fatalf("parse CSV: %v", err)
	}
	if len(rows) == 0 || !reflect.DeepEqual(rows[0], recordCsvColumns) {
		t.Fatalf("header = %v, want %v", rows, recordCsvColumns)
	}
	return rows
}

func explicitCsvWindow(uuid string, start, end time.Time) url.Values {
	return url.Values{"uuid": {uuid}, "start": {start.Format(time.RFC3339Nano)}, "end": {end.Format(time.RFC3339Nano)}}
}

func TestCSVExportsCompleteBucketsWithoutInventingMissingMetrics(t *testing.T) {
	const uuid = "csv-load-buckets"
	seedVisibilityClients(t, uuid, uuid+"-hidden")
	store := openCsvTestStore(t)
	base := time.Now().UTC().Truncate(time.Minute).Add(-8 * time.Minute)
	ctx := context.Background()
	// Two CPU measurements in one minute must remain an average, not raw rows.
	// No RAM point exists: it must not be invented as zero by reconstruction.
	for _, point := range []metric.Point{
		{MetricName: metricstore.MetricCPU, EntityID: uuid, Timestamp: base.Add(10 * time.Second), Value: 10},
		{MetricName: metricstore.MetricCPU, EntityID: uuid, Timestamp: base.Add(20 * time.Second), Value: 30},
		{MetricName: metricstore.MetricCPU, EntityID: uuid, Timestamp: base.Add(time.Minute + 10*time.Second), Value: 99},
	} {
		if err := store.Write(ctx, point); err != nil {
			t.Fatal(err)
		}
	}
	router := newCsvTestRouter(nil)
	start, end := base, base.Add(time.Minute)
	recorder := csvGet(t, router, "/api/records/load.csv", explicitCsvWindow(uuid, start, end))
	rows := parseRecordCSV(t, recorder)
	if len(rows) != 2 {
		t.Fatalf("expected header and one bucket, got %v", rows)
	}
	want := []string{base.Format(time.RFC3339Nano), "60", metricstore.MetricCPU, "avg", uuid, "", "", "", "", "20", "2", "{}"}
	if !reflect.DeepEqual(rows[1], want) {
		t.Fatalf("row = %v, want %v", rows[1], want)
	}
	if recorder.Header().Get("X-Nekomari-Window-Semantics") != "complete-buckets-only" ||
		recorder.Header().Get("X-Nekomari-Window-Start") != start.Format(time.RFC3339Nano) ||
		recorder.Header().Get("X-Nekomari-Window-End-Exclusive") != end.Format(time.RFC3339Nano) {
		t.Fatalf("window headers = %v", recorder.Header())
	}
	// The interval excludes partial edge buckets, even if they contain points.
	partial := csvGet(t, router, "/api/records/load.csv", explicitCsvWindow(uuid, start.Add(time.Second), end.Add(-time.Second)))
	if got := parseRecordCSV(t, partial); len(got) != 1 {
		t.Fatalf("partial bucket leaked: %v", got)
	}
	filtered := explicitCsvWindow(uuid, start, end)
	filtered.Set("load_type", "ram")
	if got := parseRecordCSV(t, csvGet(t, router, "/api/records/load.csv", filtered)); len(got) != 1 {
		t.Fatalf("missing RAM must stay missing, got %v", got)
	}
}

func TestCSVHistoricalAlignedBucketsAndCounterSemantics(t *testing.T) {
	const uuid = "csv-coarse-buckets"
	seedVisibilityClients(t, uuid, uuid+"-hidden")
	store := openCsvTestStore(t)
	base := time.Now().UTC().Add(-12 * time.Hour).Truncate(5 * time.Minute)
	ctx := context.Background()
	// Import retained historical rollups. Backfilled raw writes outside the
	// late-arrival grace intentionally do not construct old coarse buckets.
	if err := store.ReplaceRollupPoints(ctx, 5*time.Minute, []metric.Point{
		{MetricName: metricstore.MetricCPU, EntityID: uuid, Timestamp: base, Value: 999},
		{MetricName: metricstore.MetricCPU, EntityID: uuid, Timestamp: base.Add(5 * time.Minute), Value: 20},
		{MetricName: metricstore.MetricCPU, EntityID: uuid, Timestamp: base.Add(10 * time.Minute), Value: 999},
		{MetricName: metricstore.MetricTrafficUp, EntityID: uuid, Timestamp: base.Add(5 * time.Minute), Value: 150},
		{MetricName: metricstore.MetricBackupAge, EntityID: uuid, Timestamp: base.Add(5 * time.Minute), Value: 30},
	}); err != nil {
		t.Fatal(err)
	}
	query := explicitCsvWindow(uuid, base.Add(time.Minute), base.Add(14*time.Minute))
	rows := parseRecordCSV(t, csvGet(t, newCsvTestRouter(nil), "/api/records/load.csv", query))
	if len(rows) != 4 {
		t.Fatalf("expected three fully contained coarse buckets, got %v", rows)
	}
	byMetric := make(map[string][]string)
	for _, row := range rows[1:] {
		byMetric[row[2]] = row
		if row[0] != base.Add(5*time.Minute).Format(time.RFC3339Nano) || row[1] != "300" {
			t.Fatalf("partial edge or wrong resolution: %v", row)
		}
	}
	for _, tc := range []struct{ metric, aggregation, value, count string }{
		{metricstore.MetricCPU, "avg", "20", "1"},
		{metricstore.MetricTrafficUp, "last", "150", "1"},
		{metricstore.MetricBackupAge, "last", "30", "1"},
	} {
		row := byMetric[tc.metric]
		if row == nil || row[3] != tc.aggregation || row[9] != tc.value || row[10] != tc.count {
			t.Errorf("%s aggregate = %v", tc.metric, row)
		}
	}
}

func TestCSVHistoricalBucketAtTierRetentionBoundary(t *testing.T) {
	const uuid = "csv-retention-boundary"
	seedVisibilityClients(t, uuid, uuid+"-hidden")
	store := openCsvTestStore(t)
	now := time.Now().UTC()
	// The unaligned request begins just outside the 10-hour minute-tier
	// retention, but its first whole 5-minute bucket begins inside it.
	cutoff := now.Add(-10 * time.Hour)
	firstBucket := cutoff.Truncate(5 * time.Minute).Add(5 * time.Minute)
	start := firstBucket.Add(-5*time.Minute + time.Second)
	if !start.Before(cutoff) {
		start = cutoff.Add(-time.Nanosecond)
	}
	ctx := context.Background()
	if err := store.ImportRollups(ctx, []metric.PersistedRollup{{
		MetricName: metricstore.MetricCPU, EntityID: uuid, Resolution: 5 * time.Minute,
		Bucket: firstBucket, Count: 1, Sum: 20, SumSq: 400, Min: 20, Max: 20,
		FirstValue: 20, FirstTime: firstBucket.Add(time.Second), LastValue: 20,
		LastTime: firstBucket.Add(time.Second), CreatedAt: now,
	}}); err != nil {
		t.Fatal(err)
	}
	rows := parseRecordCSV(t, csvGet(t, newCsvTestRouter(nil), "/api/records/load.csv",
		explicitCsvWindow(uuid, start, firstBucket.Add(5*time.Minute))))
	if len(rows) != 2 || rows[1][1] != "300" || rows[1][9] != "20" {
		t.Fatalf("retention boundary lost coarse bucket: %v", rows)
	}
}

func TestCSVCycleCountersAndBackupStatusUseLastSample(t *testing.T) {
	const uuid = "csv-last-counters"
	seedVisibilityClients(t, uuid, uuid+"-hidden")
	store := openCsvTestStore(t)
	base := time.Now().UTC().Truncate(time.Minute).Add(-4 * time.Minute)
	for _, point := range []metric.Point{
		{MetricName: metricstore.MetricTrafficUp, EntityID: uuid, Timestamp: base.Add(5 * time.Second), Value: 100},
		{MetricName: metricstore.MetricTrafficUp, EntityID: uuid, Timestamp: base.Add(10 * time.Second), Value: 150},
		{MetricName: metricstore.MetricBackupAge, EntityID: uuid, Timestamp: base.Add(5 * time.Second), Value: -1},
		{MetricName: metricstore.MetricBackupAge, EntityID: uuid, Timestamp: base.Add(10 * time.Second), Value: 30},
	} {
		if err := store.Write(context.Background(), point); err != nil {
			t.Fatal(err)
		}
	}
	rows := parseRecordCSV(t, csvGet(t, newCsvTestRouter(nil), "/api/records/load.csv", explicitCsvWindow(uuid, base, base.Add(time.Minute))))
	if len(rows) != 3 {
		t.Fatalf("expected two series: %v", rows)
	}
	for _, row := range rows[1:] {
		if row[3] != "last" || row[10] != "2" ||
			(row[2] == metricstore.MetricTrafficUp && row[9] != "150") ||
			(row[2] == metricstore.MetricBackupAge && row[9] != "30") {
			t.Fatalf("bad last-sample aggregate: %v", row)
		}
	}
}

func TestPingCSVLastReadingAndVisibility(t *testing.T) {
	const visible = "csv-ping-visible"
	hidden := visible + "-hidden"
	seedVisibilityClients(t, visible, hidden)
	openCsvTestStore(t)
	base := time.Now().UTC().Truncate(time.Minute).Add(-4 * time.Minute)
	for _, rec := range []models.PingRecord{
		{Client: visible, TaskId: 17, PingType: "tcp", Family: "ipv4", Time: base.Add(10 * time.Second), ValueUS: 420},
		{Client: visible, TaskId: 17, PingType: "tcp", Family: "ipv4", Time: base.Add(20 * time.Second), Value: -1},
		{Client: visible, TaskId: 17, PingType: "icmp", Family: "ipv6", Role: "reference", Time: base.Add(20 * time.Second), ValueUS: 1200},
		{Client: hidden, TaskId: 17, PingType: "tcp", Time: base.Add(20 * time.Second), Value: 7},
	} {
		if err := metricstore.WritePingRecord(context.Background(), rec); err != nil {
			t.Fatal(err)
		}
	}
	query := explicitCsvWindow("", base, base.Add(time.Minute))
	query.Del("uuid")
	query.Set("task_id", "17")
	guest := newCsvTestRouter(nil)
	rows := parseRecordCSV(t, csvGet(t, guest, "/api/records/ping.csv", query))
	if len(rows) != 3 {
		t.Fatalf("guest should see two visible series: %v", rows)
	}
	values := make(map[string][]string)
	for _, row := range rows[1:] {
		values[row[6]] = row
	}
	if values["tcp"][9] != "-1" || values["tcp"][10] != "2" || values["tcp"][3] != "last" {
		t.Fatalf("tcp loss/last/count = %v", values["tcp"])
	}
	if values["icmp"][9] != "1.2" || values["icmp"][7] != "ipv6" || values["icmp"][8] != "reference" {
		t.Fatalf("reference and fractional latency = %v", values["icmp"])
	}
	admin := newCsvTestRouter(rpc.NewUserPrincipal("admin"))
	if got := parseRecordCSV(t, csvGet(t, admin, "/api/records/ping.csv", query)); len(got) != 4 {
		t.Fatalf("administrator should see hidden node too: %v", got)
	}
	noScope := newCsvTestRouter(rpc.NewScopedAPIKeyPrincipal("key", []string{rpc.ScopeTasksRead}))
	if got := parseRecordCSV(t, csvGet(t, noScope, "/api/records/ping.csv", query)); len(got) != 3 {
		t.Fatalf("key without nodes:read must not see hidden node: %v", got)
	}
	for _, uuid := range []string{hidden, "unknown-node"} {
		q := explicitCsvWindow(uuid, base, base.Add(time.Minute))
		if got := parseRecordCSV(t, csvGet(t, guest, "/api/records/ping.csv", q)); len(got) != 1 {
			t.Fatalf("hidden/unknown node leaked: %v", got)
		}
	}
}

func TestCSVPreservesAdditionalSeriesTags(t *testing.T) {
	const uuid = "csv-series-tags"
	seedVisibilityClients(t, uuid, uuid+"-hidden")
	store := openCsvTestStore(t)
	base := time.Now().UTC().Truncate(time.Minute).Add(-4 * time.Minute)
	for _, device := range []string{"a,b", "c=d"} {
		if err := store.Write(context.Background(), metric.Point{
			MetricName: metricstore.MetricCPU, EntityID: uuid, Timestamp: base.Add(time.Second), Value: 2,
			Tags: map[string]string{"device": device},
		}); err != nil {
			t.Fatal(err)
		}
	}
	rows := parseRecordCSV(t, csvGet(t, newCsvTestRouter(nil), "/api/records/load.csv", explicitCsvWindow(uuid, base, base.Add(time.Minute))))
	if len(rows) != 3 || rows[1][11] != `{"device":"a,b"}` || rows[2][11] != `{"device":"c=d"}` {
		t.Fatalf("additional tags lost or merged: %v", rows)
	}
}

func TestCSVWindowAndLimitValidation(t *testing.T) {
	openCsvTestStore(t)
	router := newCsvTestRouter(nil)
	base := time.Now().UTC().Truncate(time.Minute).Add(-4 * time.Minute)
	for _, query := range []url.Values{
		{"uuid": {"node"}, "hours": {"0"}},
		{"uuid": {"node"}, "hours": {""}},
		{"uuid": {"node"}, "hours": {"1", "2"}},
		{"uuid": {"node"}, "hours": {"1"}, "start": {base.Format(time.RFC3339)}},
		{"uuid": {"node"}, "start": {base.Format(time.RFC3339)}},
		{"uuid": {"node"}, "start": {"2026-01-01T00:00:00"}, "end": {base.Format(time.RFC3339)}},
		{"uuid": {"node"}, "start": {base.Format(time.RFC3339)}, "end": {base.Format(time.RFC3339)}},
		{"uuid": {"node"}, "start": {base.Add(-367 * 24 * time.Hour).Format(time.RFC3339)}, "end": {base.Format(time.RFC3339)}},
		{"uuid": {"node"}, "max_rows": {"0"}},
		{"uuid": {"node"}, "max_rows": {"250001"}},
		{"uuid": {"node"}, "load_type": {"temp"}},
	} {
		recorder := csvGet(t, router, "/api/records/load.csv", query)
		if recorder.Code != http.StatusBadRequest {
			t.Errorf("query %v: status = %d body=%s", query, recorder.Code, recorder.Body.String())
		}
	}
	if got := csvGet(t, router, "/api/records/load.csv", url.Values{"hours": {"1"}}); got.Code != http.StatusBadRequest {
		t.Fatalf("fleet export without privilege: %d", got.Code)
	}
}

func TestCSVRowLimitBeforeAttachment(t *testing.T) {
	const uuid = "csv-row-limit"
	seedVisibilityClients(t, uuid, uuid+"-hidden")
	store := openCsvTestStore(t)
	base := time.Now().UTC().Truncate(time.Minute).Add(-4 * time.Minute)
	for i := 0; i < 2; i++ {
		if err := store.Write(context.Background(), metric.Point{MetricName: metricstore.MetricCPU, EntityID: uuid, Timestamp: base.Add(time.Duration(i)*time.Minute + time.Second), Value: float64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	query := explicitCsvWindow(uuid, base, base.Add(2*time.Minute))
	query.Set("max_rows", "1")
	recorder := csvGet(t, newCsvTestRouter(nil), "/api/records/load.csv", query)
	if recorder.Code != http.StatusRequestEntityTooLarge || !strings.Contains(recorder.Body.String(), "max_rows") ||
		strings.Contains(recorder.Header().Get("Content-Type"), "text/csv") {
		t.Fatalf("row cap must fail before a CSV attachment: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestCSVSpreadsheetTextIsNotFormula(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"abc", "abc"}, {"=1+1", "'=1+1"}, {"  +1", "'  +1"}, {"\t@cmd", "'\t@cmd"},
		{"  \ttext", "'  \ttext"}, {"\rtext", "'\rtext"}, {"-1", "'-1"},
	} {
		if got := csvSpreadsheetText(tc.input); got != tc.want {
			t.Errorf("%q => %q, want %q", tc.input, got, tc.want)
		}
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	window := csvWindow{time.Now().Add(-time.Hour), time.Now()}
	writeRecordBucketsCSV(c, "ping", []recordCsvRow{{point: metric.AggregatePoint{
		MetricName: metricstore.MetricPingLatency, EntityID: "=cmd", Bucket: window.start, Value: -1, Count: 1,
		Tags: map[string]string{"protocol": "+cmd"},
	}, aggregation: metric.AggLast, interval: time.Minute}}, window)
	rows := parseRecordCSV(t, recorder)
	if got := fmt.Sprint(rows[1][4], "/", rows[1][6], "/", rows[1][9]); got != "'=cmd/'+cmd/-1" {
		t.Fatalf("escaped text / numeric loss: %s", got)
	}
}
