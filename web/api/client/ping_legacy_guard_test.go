package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/cmd/flags"
	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/database/tasks"
	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/internal/metricstore"
	"github.com/Aone2233/nekomari/pkg/metric"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
	"github.com/Aone2233/nekomari/web/api"
	"github.com/gin-gonic/gin"
)

func TestLegacyPingResultCannotBypassFamilyPin(t *testing.T) {
	const marker = "NEKOMARI_PING_LEGACY_FENCE_CHILD"
	if os.Getenv(marker) != "1" {
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestLegacyPingResultCannotBypassFamilyPin$")
		cmd.Dir = t.TempDir()
		cmd.Env = append(os.Environ(), marker+"=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated legacy fence: %v\n%s", err, out)
		}
		return
	}
	db := dbcore.GetDBInstance()
	task := models.PingTask{Name: "legacy-result-fence", Type: "tcp", Target: "example.com:80", TargetFamily: "ipv6", Interval: 60}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Delete(&task).Error })
	params, err := json.Marshal(v2.PingResultParams{TaskID: task.Id, PingType: "tcp", Family: "ipv6", Value: 2})
	if err != nil {
		t.Fatal(err)
	}
	response := handleV2RPC("legacy-result-node", v2.RawRequest{JSONRPC: v2.Version, Method: v2.MethodAgentPingResult, Params: params, ID: 1}, false)
	if response.Error == nil || response.Error.Message != "failed to save ping result" {
		t.Fatalf("legacy result bypassed the pin: %+v", response)
	}
}

// This uses the actual authenticated POST handler, task deletion and metric
// batcher. A subprocess keeps their process-wide stores isolated from other tests.
func TestPingDeletionAndLegacyResultLifecycle(t *testing.T) {
	const marker = "NEKOMARI_PING_LIFECYCLE_CHILD"
	if os.Getenv(marker) != "1" {
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestPingDeletionAndLegacyResultLifecycle$", "-test.v")
		cmd.Dir = t.TempDir()
		cmd.Env = append(os.Environ(), marker+"=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("ping lifecycle fixture: %v\n%s", err, out)
		}
		return
	}
	flags.DatabaseType = flags.DatabaseTypeSQLite
	flags.DatabaseFile = "file:ping-lifecycle-main?mode=memory&cache=shared"
	db := dbcore.GetDBInstance()
	if err := config.Set(metricstore.MetricDBDSNKey, "file:ping-lifecycle-metrics?mode=memory&cache=shared"); err != nil {
		t.Fatal(err)
	}
	if err := metricstore.InitializeStore(); err != nil {
		t.Fatal(err)
	}
	metricstore.StartReportBatcher()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := metricstore.StopReportBatcher(ctx); err != nil {
			t.Errorf("stop batcher: %v", err)
		}
		if err := metricstore.CloseStoreContext(ctx); err != nil {
			t.Errorf("close metric store: %v", err)
		}
	})
	const uuid, token = "ping-lifecycle-node", "ping-lifecycle-token"
	if err := db.Create(&models.Client{UUID: uuid, Token: token}).Error; err != nil {
		t.Fatal(err)
	}
	task := models.PingTask{Name: "legacy", Type: "tcp", Target: "127.0.0.1:80", Interval: 60, Clients: models.StringArray{uuid}}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(api.IdentityMiddleware())
	router.POST("/api/clients/v2/rpc", api.RequireRole(api.RoleClient), UploadV2RPC)
	post := func() v2.Response {
		t.Helper()
		body, err := json.Marshal(v2.Request{JSONRPC: v2.Version, ID: 1, Method: v2.MethodAgentPingResult,
			Params: v2.PingResultParams{TaskID: task.Id, PingType: "tcp", Family: "ipv6", Value: 2}})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, "/api/clients/v2/rpc", bytes.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		var response v2.Response
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || (recorder.Code != http.StatusOK && recorder.Code != http.StatusBadRequest) {
			t.Fatalf("POST status=%d body=%q err=%v", recorder.Code, recorder.Body.String(), err)
		}
		return response
	}
	countPoints := func() int {
		t.Helper()
		points, err := metricstore.GetStore().Query(t.Context(), metric.Query{MetricName: metricstore.MetricPingLoss,
			EntityID: uuid, Tags: map[string]string{"task_id": fmt.Sprint(task.Id)},
			Start: time.Now().Add(-time.Minute), End: time.Now().Add(time.Minute)})
		if err != nil {
			t.Fatal(err)
		}
		return len(points)
	}
	if response := post(); response.Error != nil {
		t.Fatalf("legacy result was not accepted: %+v", response)
	}
	if err := metricstore.FlushReportBatch(t.Context()); err != nil || countPoints() != 1 {
		t.Fatalf("initial result not persisted: err=%v points=%d", err, countPoints())
	}
	// Another accepted result may still be queued when deletion begins.
	if response := post(); response.Error != nil {
		t.Fatalf("second legacy result was not accepted: %+v", response)
	}
	if err := tasks.DeletePingTask([]uint{task.Id}); err != nil {
		t.Fatal(err)
	}
	if err := metricstore.FlushReportBatch(t.Context()); err != nil || countPoints() != 0 {
		t.Fatalf("deleted task retained history: err=%v points=%d", err, countPoints())
	}
	if response := post(); response.Error == nil {
		t.Fatal("a result for the deleted task was accepted")
	}
	// Even an unsupported explicit ID reuse cannot reopen the old result
	// stream while the durable deletion reservation exists.
	replacement := models.PingTask{Id: task.Id, Name: "pinned", Type: "tcp", Target: "host.test:80", TargetFamily: "ipv6", Interval: 60, Clients: models.StringArray{uuid}}
	if err := db.Create(&replacement).Error; err != nil {
		t.Fatal(err)
	}
	if response := post(); response.Error == nil || !strings.Contains(fmt.Sprint(response.Error.Data), "reserved") {
		t.Fatalf("reserved pinned task accepted old result: %+v", response)
	}
	if err := db.Delete(&replacement).Error; err != nil {
		t.Fatal(err)
	}
	replacement = models.PingTask{Id: task.Id, Name: "legacy replacement", Type: "tcp", Target: "127.0.0.1:80", Interval: 60, Clients: models.StringArray{uuid}}
	if err := db.Create(&replacement).Error; err != nil {
		t.Fatal(err)
	}
	if response := post(); response.Error == nil {
		t.Fatal("reserved legacy task ID accepted an old result")
	}
	if err := metricstore.FlushReportBatch(t.Context()); err != nil || countPoints() != 0 {
		t.Fatalf("reserved legacy ID received history: err=%v points=%d", err, countPoints())
	}
	// The supported SQLite/GORM auto-ID path does not reuse that ID, even
	// after deleting the highest row; this does not cover explicit inserts or
	// restore/rollback to an older sequence.
	if err := db.Delete(&replacement).Error; err != nil {
		t.Fatal(err)
	}
	next := models.PingTask{Name: "auto-id replacement", Type: "tcp", Target: "127.0.0.1:80", Interval: 60}
	if err := db.Create(&next).Error; err != nil || next.Id <= task.Id {
		t.Fatalf("auto ID reused: old=%d new=%d err=%v", task.Id, next.Id, err)
	}
}

func TestUnknownProbeThroughAuthenticatedReport(t *testing.T) {
	const marker = "NEKOMARI_UNKNOWN_PROBE_CHILD"
	if os.Getenv(marker) != "1" {
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestUnknownProbeThroughAuthenticatedReport$")
		cmd.Dir = t.TempDir()
		cmd.Env = append(os.Environ(), marker+"=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated probe: %v\n%s", err, out)
		}
		return
	}
	flags.DatabaseType = flags.DatabaseTypeSQLite
	flags.DatabaseFile = "file:unknown-probe-main?mode=memory&cache=shared"
	db := dbcore.GetDBInstance()
	if err := config.Set(metricstore.MetricDBDSNKey, "file:unknown-probe-metrics?mode=memory&cache=shared"); err != nil {
		t.Fatal(err)
	}
	if err := metricstore.InitializeStore(); err != nil {
		t.Fatal(err)
	}
	defer metricstore.CloseStoreContext(context.Background())
	node := models.Client{UUID: "unknown-probe-node", Token: "unknown-probe-fixture-token"}
	if err := db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	task := models.PingTask{Name: "unknown", Type: "tcp", Target: "bad.invalid:80", Interval: 60, Clients: models.StringArray{node.UUID}}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(v2.Request{JSONRPC: v2.Version, ID: 1, Method: v2.MethodAgentPingResult, Params: v2.PingResultParams{TaskID: task.Id, PingType: "tcp", Value: -1, ProbeStatus: "unknown", ProbeReason: "dns_failure"}})
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(api.IdentityMiddleware())
	router.POST("/api/clients/v2/rpc", api.RequireRole(api.RoleClient), UploadV2RPC)
	request := httptest.NewRequest(http.MethodPost, "/api/clients/v2/rpc", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+node.Token)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("report: %d %s", recorder.Code, recorder.Body.String())
	}
	for _, check := range []struct {
		name  string
		count int
	}{{metricstore.MetricPingUnknown, 1}, {metricstore.MetricPingLoss, 0}, {metricstore.MetricPingLatency, 0}} {
		points, err := metricstore.GetStore().Query(t.Context(), metric.Query{MetricName: check.name, EntityID: node.UUID, Start: time.Now().Add(-time.Minute), End: time.Now().Add(time.Minute)})
		if err != nil || len(points) != check.count {
			t.Fatalf("%s: points=%+v err=%v", check.name, points, err)
		}
	}
}
