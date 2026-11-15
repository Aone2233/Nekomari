//go:build linux

package client

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/cmd/flags"
	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/internal/metricstore"
	"github.com/Aone2233/nekomari/internal/scheduler"
	"github.com/Aone2233/nekomari/internal/sla"
	"github.com/Aone2233/nekomari/pkg/metric"
	"github.com/Aone2233/nekomari/utils"
	agentRuntime "github.com/Aone2233/nekomari/web/agent"
	"github.com/Aone2233/nekomari/web/api"
	"github.com/gin-gonic/gin"
)

// Actual executable, scheduler, authenticated WebSocket and persistent metric
// store; no network access beyond isolated loopback. Its reserved invalid
// hostname cannot resolve in that namespace and must be reported as unknown
// rather than loss; the test makes no claim about a particular DNS server.
func TestOrdinaryAgentExecutableUnknownDoesNotBecomeSLAOutage(t *testing.T) {
	if os.Getenv("NEKOMARI_ORDINARY_EXEC_E2E") != "1" {
		t.Skip("requires Linux network namespace privileges")
	}
	const marker = "NEKOMARI_ORDINARY_EXEC_CHILD"
	if os.Getenv(marker) != "1" {
		_, source, _, ok := runtime.Caller(0)
		if !ok {
			t.Fatal("locate Agent module")
		}
		root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", ".."))
		binary := filepath.Join(t.TempDir(), "komari-agent")
		build := exec.CommandContext(t.Context(), "go", "build", "-race", "-o", binary, ".")
		build.Dir = filepath.Join(root, "agent")
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build Agent: %v\n%s", err, out)
		}
		cmd := exec.CommandContext(t.Context(), "unshare", "--net", "--", os.Args[0], "-test.run=^TestOrdinaryAgentExecutableUnknownDoesNotBecomeSLAOutage$", "-test.v", "-test.timeout=60s")
		cmd.Dir = t.TempDir()
		cmd.Env = append(os.Environ(), marker+"=1", "NEKOMARI_ORDINARY_EXEC_BINARY="+binary)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated executable fixture: %v\n%s", err, out)
		}
		return
	}
	if out, err := exec.CommandContext(t.Context(), "ip", "link", "set", "lo", "up").CombinedOutput(); err != nil {
		t.Fatalf("loopback: %v %s", err, out)
	}
	flags.DatabaseType = flags.DatabaseTypeSQLite
	flags.DatabaseFile = filepath.Join("data", "komari.db")
	if err := os.MkdirAll("data", 0o700); err != nil {
		t.Fatal(err)
	}
	db := dbcore.GetDBInstance()
	if err := config.Set(metricstore.MetricDBDSNKey, filepath.Join("data", "metrics.db")); err != nil {
		t.Fatal(err)
	}
	if err := metricstore.InitializeStore(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := metricstore.CloseStoreContext(ctx); err != nil {
			t.Error(err)
		}
		if err := dbcore.Close(); err != nil {
			t.Error(err)
		}
	}()
	const id = "ordinary-fixture-node"
	// This credential exists only inside the disposable database and process.
	var randomToken [24]byte
	if _, err := rand.Read(randomToken[:]); err != nil {
		t.Fatal(err)
	}
	token := hex.EncodeToString(randomToken[:])
	if err := db.Create(&models.Client{UUID: id, Token: token, Name: id}).Error; err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(api.IdentityMiddleware())
	group := router.Group("/api/clients", api.RequireRole(api.RoleClient))
	group.POST("/v2/rpc", UploadV2RPC)
	var sockets sync.WaitGroup
	group.GET("/v2/rpc", func(c *gin.Context) { sockets.Add(1); defer sockets.Done(); WebSocketV2RPC(c) })
	panel := httptest.NewServer(router)
	defer func() { panel.Close(); sockets.Wait() }()
	cmd := exec.Command(os.Getenv("NEKOMARI_ORDINARY_EXEC_BINARY"), "--endpoint", panel.URL, "--disable-auto-update", "--disable-web-ssh", "--interval", "1")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "AGENT_TOKEN="+token, "HTTP_PROXY=", "HTTPS_PROXY=", "ALL_PROXY=", "NO_PROXY=*")
	output := &bytes.Buffer{}
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer func() {
		_ = cmd.Process.Signal(os.Interrupt)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		ResetClientState(id)
		if t.Failed() {
			t.Logf("Agent output: %s", output.String())
		}
	}()
	task := models.PingTask{Name: "unknown fixture", Type: "tcp", Target: "invalid.invalid:80", Interval: 2, Clients: models.StringArray{id}}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for !agentRuntime.IsAgentOnline(id) {
		if time.Now().After(deadline) {
			t.Fatal("Agent did not connect")
		}
		time.Sleep(50 * time.Millisecond)
	}
	schedule := scheduler.NewManager()
	mgr := utils.NewPingTaskManagerWithScheduler(schedule, nil)
	if err := mgr.Reload([]models.PingTask{task}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		mgr.Stop()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := schedule.StopAndWait(ctx); err != nil {
			t.Errorf("drain scheduler: %v", err)
		}
	}()
	store := metricstore.GetStore()
	query := func(name string) []metric.Point {
		t.Helper()
		p, err := store.Query(t.Context(), metric.Query{MetricName: name, EntityID: id, Tags: map[string]string{"task_id": fmt.Sprint(task.Id)}, Start: time.Now().Add(-time.Minute), End: time.Now().Add(time.Minute)})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	for len(query(metricstore.MetricPingUnknown)) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("unknown result not received")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if points := query(metricstore.MetricPingUnknown); len(points) == 0 || points[0].Tags["reason"] != "dns_failure" {
		t.Fatalf("unknown points: %+v", points)
	}
	if points := query(metricstore.MetricPingLoss); len(points) != 0 {
		t.Fatalf("unknown produced loss: %+v", points)
	}
	if points := query(metricstore.MetricPingLatency); len(points) != 0 {
		t.Fatalf("unknown produced latency: %+v", points)
	}
	now := time.Now().UTC()
	report, err := sla.Build(t.Context(), sla.ReportRequest{Reader: store, EntityIDs: []string{id}, Start: now.Add(-time.Minute), End: now.Add(time.Minute), Interval: time.Minute, Now: now})
	if err != nil || len(report.Nodes) != 1 || report.Nodes[0].HasReport || len(report.Nodes[0].Tasks) != 0 {
		t.Fatalf("unknown became SLA outage: %+v err=%v", report, err)
	}
}
