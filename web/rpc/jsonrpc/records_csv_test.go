package jsonrpc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/internal/metricstore"
	protocol "github.com/Aone2233/nekomari/protocol/v2"
	"github.com/gin-gonic/gin"
)

// The CSV endpoints are the tabular form of the public JSON record endpoints:
// same retained data, same visibility rules. These tests pin both sides of
// that contract — the file shape (BOM, CRLF, raw values) and the guest rules.

func newCsvTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/records/load.csv", LoadRecordsCSV)
	router.GET("/api/records/ping.csv", PingRecordsCSV)
	return router
}

func openCsvTestStore(t *testing.T) {
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
}

func TestLoadRecordsCSVExportsRawColumnsAndValues(t *testing.T) {
	const uuid = "csv-load-raw"
	seedVisibilityClients(t, uuid, uuid+"-hidden")
	openCsvTestStore(t)

	now := time.Now().UTC()
	if _, err := metricstore.WriteReport(context.Background(), protocol.Report{
		UUID:      uuid,
		UpdatedAt: now.Add(-2 * time.Minute),
		CPU:       protocol.CPUReport{Usage: 42.5},
		Ram:       protocol.RamReport{Used: 123456, Total: 999999},
		Connections: protocol.ConnectionsReport{
			TCP: 321,
			UDP: 7,
		},
	}); err != nil {
		t.Fatalf("write report: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/records/load.csv?uuid="+uuid+"&hours=1", nil)
	newCsvTestRouter().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/csv; charset=utf-8" {
		t.Fatalf("content type = %q", got)
	}
	if got := recorder.Header().Get("Content-Disposition"); !strings.HasPrefix(got, `attachment; filename="nekomari-load-`) {
		t.Fatalf("content disposition = %q", got)
	}
	body := recorder.Body.String()
	if !strings.HasPrefix(body, "\uFEFF") {
		t.Fatal("CSV must start with a UTF-8 BOM for Excel")
	}
	lines := strings.Split(strings.TrimSuffix(body[3:], "\r\n"), "\r\n")
	if len(lines) != 2 {
		t.Fatalf("expected header plus one data row, got %d lines: %q", len(lines), lines)
	}
	if !strings.Contains(lines[0], "time,client,cpu") {
		t.Fatalf("header = %q, want time,client,cpu columns", lines[0])
	}
	columns := strings.Split(lines[0], ",")
	values := strings.Split(lines[1], ",")
	byColumn := map[string]string{}
	for i, column := range columns {
		byColumn[column] = values[i]
	}
	if byColumn["cpu"] != "42.5" {
		t.Fatalf("cpu = %q, want 42.5 (raw value, not a percent label)", byColumn["cpu"])
	}
	if byColumn["ram"] != "123456" {
		t.Fatalf("ram = %q, want the raw byte count", byColumn["ram"])
	}
	if _, present := byColumn["ram_total"]; present {
		t.Fatalf("columns = %v: totals are not retained by the metric store and must not be exported", columns)
	}
	if byColumn["connections"] != "321" || byColumn["connections_udp"] != "7" {
		t.Fatalf("connections = %q/%q", byColumn["connections"], byColumn["connections_udp"])
	}
	if byColumn["client"] != uuid {
		t.Fatalf("client = %q, want %q", byColumn["client"], uuid)
	}
}

func TestPingRecordsCSVKeepsFractionalMilliseconds(t *testing.T) {
	const uuid = "csv-ping-decimal"
	seedVisibilityClients(t, uuid, uuid+"-hidden")
	openCsvTestStore(t)

	now := time.Now().UTC()
	// One task per probe: a shared series collapses to a single per-bucket
	// sample, which would hide two of the three readings.
	records := []models.PingRecord{
		{Client: uuid, TaskId: 501, PingType: "tcp", Family: "ipv4", Time: now.Add(-4 * time.Second), Value: 0, ValueUS: 420},
		{Client: uuid, TaskId: 502, PingType: "tcp", Family: "ipv4", Time: now.Add(-3 * time.Second), Value: 1, ValueUS: 1200},
		{Client: uuid, TaskId: 503, PingType: "tcp", Family: "ipv4", Time: now.Add(-2 * time.Second), Value: -1},
	}
	for _, record := range records {
		if err := metricstore.WritePingRecord(context.Background(), record); err != nil {
			t.Fatalf("write ping record: %v", err)
		}
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/records/ping.csv?uuid="+uuid+"&hours=1", nil)
	newCsvTestRouter().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	if !strings.HasPrefix(body, "\uFEFF") {
		t.Fatal("CSV must start with a UTF-8 BOM for Excel")
	}
	lines := strings.Split(strings.TrimSuffix(body[3:], "\r\n"), "\r\n")
	if len(lines) != 4 {
		t.Fatalf("expected header plus three rows, got %d lines: %q", len(lines), lines)
	}
	if lines[0] != strings.Join(pingCsvColumns, ",") {
		t.Fatalf("header = %q, want %v", lines[0], pingCsvColumns)
	}
	values := map[string]bool{}
	for _, line := range lines[1:] {
		parts := strings.Split(line, ",")
		values[parts[3]] = true
	}
	for _, want := range []string{"0.42", "1.2", "-1"} {
		if !values[want] {
			t.Fatalf("rows = %q, missing value %s: sub-millisecond probes must survive as decimals", lines[1:], want)
		}
	}
}

func TestLoadRecordsCSVHidesUnknownNodeWithHeaderOnly(t *testing.T) {
	const uuid = "csv-load-hidden"
	seedVisibilityClients(t, uuid, uuid+"-hidden")
	openCsvTestStore(t)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/records/load.csv?uuid="+uuid+"-hidden&hours=1", nil)
	newCsvTestRouter().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	body := strings.TrimPrefix(recorder.Body.String(), "\uFEFF")
	lines := strings.Split(strings.TrimSuffix(body, "\r\n"), "\r\n")
	if len(lines) != 1 || lines[0] == "" {
		t.Fatalf("hidden node must yield a header-only file, got %q", lines)
	}
	if !strings.Contains(lines[0], "time,client") {
		t.Fatalf("header = %q", lines[0])
	}
}

func TestLoadRecordsCSVRequiresUUIDForGuests(t *testing.T) {
	openCsvTestStore(t)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/records/load.csv?hours=1", nil)
	newCsvTestRouter().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestPingRecordsCSVHonorsMaxRows(t *testing.T) {
	const uuid = "csv-ping-cap"
	seedVisibilityClients(t, uuid, uuid+"-hidden")
	openCsvTestStore(t)

	now := time.Now().UTC()
	for i := 0; i < 3; i++ {
		if err := metricstore.WritePingRecord(context.Background(), models.PingRecord{
			Client: uuid, TaskId: 601 + uint(i), PingType: "icmp", Family: "ipv4",
			Time: now.Add(-time.Duration(4-i) * time.Second), Value: 10 + i,
		}); err != nil {
			t.Fatalf("write ping record: %v", err)
		}
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/records/ping.csv?uuid="+uuid+"&hours=1&max_rows=1", nil)
	newCsvTestRouter().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "max_rows") {
		t.Fatalf("body = %s, want a max_rows explanation", recorder.Body.String())
	}
}
