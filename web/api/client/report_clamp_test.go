package client

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/cmd/flags"
	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/internal/metricstore"
	"github.com/Aone2233/nekomari/pkg/metric"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
	agentrt "github.com/Aone2233/nekomari/web/agent"
	"github.com/Aone2233/nekomari/web/api"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

type reportClampLogs struct {
	sync.Mutex
	buffer bytes.Buffer
}

func (logs *reportClampLogs) Write(p []byte) (int, error) {
	logs.Lock()
	defer logs.Unlock()
	return logs.buffer.Write(p)
}

func (logs *reportClampLogs) warnings() int {
	logs.Lock()
	defer logs.Unlock()
	return strings.Count(logs.buffer.String(), "Clamped resource used to total")
}

// A separate test process owns DB/config, presence timers and notification
// workers, so global fixtures in unrelated tests cannot rebind its stores.
func TestReportClampThroughHTTPAndWebSocket(t *testing.T) {
	const marker = "NEKOMARI_REPORT_CLAMP_CHILD"
	if os.Getenv(marker) != "1" {
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(t.Context(), executable, "-test.run=^TestReportClampThroughHTTPAndWebSocket$", "-test.v")
		cmd.Dir = t.TempDir()
		cmd.Env = append(os.Environ(), marker+"=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("report transport fixture: %v\n%s", err, out)
		}
		return
	}
	flags.DatabaseType = flags.DatabaseTypeSQLite
	flags.DatabaseFile = "file:clamp-transport-main?mode=memory&cache=shared"
	db := dbcore.GetDBInstance()
	if err := config.Set(metricstore.MetricDBDSNKey, "file:clamp-transport-metrics?mode=memory&cache=shared"); err != nil {
		t.Fatal(err)
	}
	if err := metricstore.InitializeStore(); err != nil {
		t.Fatal(err)
	}
	logs := &reportClampLogs{}
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(api.IdentityMiddleware())
	group := router.Group("/api/clients", api.RequireRole(api.RoleAdmin, api.RoleClient))
	group.POST("/v2/rpc", UploadV2RPC)
	var sockets sync.WaitGroup
	group.GET("/v2/rpc", func(c *gin.Context) {
		sockets.Add(1)
		defer sockets.Done()
		WebSocketV2RPC(c)
	})
	server := httptest.NewServer(router)
	t.Cleanup(func() {
		server.Close()
		sockets.Wait()
		postPresenceMu.Lock()
		for uuid, entry := range postPresenceStates {
			entry.timer.Stop()
			agentrt.SetPresence(uuid, entry.connID, false)
			delete(postPresenceStates, uuid)
		}
		postPresenceMu.Unlock()
	})

	for _, transport := range []string{"http", "websocket"} {
		t.Run(transport, func(t *testing.T) {
			uuid, token := "clamp-"+transport, "test-only-clamp-token-"+transport
			if err := db.Create(&models.Client{UUID: uuid, Token: token}).Error; err != nil {
				t.Fatal(err)
			}
			var socket *websocket.Conn
			if transport == "websocket" {
				var err error
				socket, _, err = websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/api/clients/v2/rpc", http.Header{
					"Origin": {server.URL}, "Authorization": {"Bearer " + token},
				})
				if err != nil {
					t.Fatal(err)
				}
				defer socket.Close()
			}
			call := func(report v2.Report, wantError bool) {
				t.Helper()
				request := v2.Request{JSONRPC: v2.Version, ID: 1, Method: v2.MethodAgentReport, Params: v2.ReportParams{Report: report}}
				var response v2.Response
				if socket != nil {
					_ = socket.SetWriteDeadline(time.Now().Add(5 * time.Second))
					if err := socket.WriteJSON(request); err != nil {
						t.Fatal(err)
					}
					_ = socket.SetReadDeadline(time.Now().Add(5 * time.Second))
					if err := socket.ReadJSON(&response); err != nil {
						t.Fatal(err)
					}
				} else {
					raw, err := json.Marshal(request)
					if err != nil {
						t.Fatal(err)
					}
					req, err := http.NewRequest(http.MethodPost, server.URL+"/api/clients/v2/rpc", bytes.NewReader(raw))
					if err != nil {
						t.Fatal(err)
					}
					req.Header.Set("Authorization", "Bearer "+token)
					req.Header.Set("Content-Type", "application/json")
					client := &http.Client{Timeout: 5 * time.Second}
					resp, err := client.Do(req)
					if err != nil {
						t.Fatal(err)
					}
					defer resp.Body.Close()
					if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
						t.Fatal(err)
					}
					wantStatus := http.StatusOK
					if wantError {
						wantStatus = http.StatusBadRequest
					}
					if resp.StatusCode != wantStatus {
						t.Fatalf("HTTP status = %d, want %d", resp.StatusCode, wantStatus)
					}
				}
				if (response.Error != nil) != wantError {
					t.Fatalf("report error = %v, want error %t", response.Error, wantError)
				}
			}
			beforeWarnings := logs.warnings()
			report := v2.Report{UUID: "untrusted-body-uuid", SampledAt: time.Now().UTC().Add(-8 * time.Second),
				CPU: v2.CPUReport{Usage: 12}, Ram: v2.RamReport{Used: 30, Total: 20},
				Swap: v2.RamReport{Used: 1}, Disk: v2.DiskReport{Used: 9, Total: 5}, Uptime: 42}
			for i, cpu := range []float64{12, 23, 34, 45} {
				report.CPU.Usage = cpu
				report.SampledAt = report.SampledAt.Add(time.Second)
				if i == 2 {
					report.Ram.Used, report.Swap.Used, report.Disk.Used = 10, 0, 2
				} else {
					report.Ram.Used, report.Swap.Used, report.Disk.Used = 30, 1, 9
				}
				call(report, false)
				latest := agentrt.GetLatestReport(uuid)[uuid]
				if latest == nil || latest.CPU.Usage != cpu || latest.Uptime != 42 ||
					latest.Ram.Used > latest.Ram.Total || latest.Swap.Used > latest.Swap.Total || latest.Disk.Used > latest.Disk.Total ||
					!latest.UpdatedAt.Equal(report.SampledAt) || !agentrt.IsV2Client(uuid) {
					t.Fatal("accepted/clamped sample did not advance runtime state")
				}
			}
			if logs.warnings()-beforeWarnings != 2 {
				t.Fatal("clamp warnings did not follow recovery/anomaly episodes")
			}
			for _, field := range []string{"negative", "overflow", "oversize"} {
				invalid := report
				invalid.SampledAt = invalid.SampledAt.Add(time.Second)
				switch field {
				case "negative":
					invalid.Network.TotalUp = -1
				case "overflow":
					invalid.Disk.Used = math.MaxInt64
				case "oversize":
					invalid.Message = strings.Repeat("x", 5000)
				}
				call(invalid, true)
			}
			if latest := agentrt.GetLatestReport(uuid)[uuid]; latest == nil || latest.CPU.Usage != 45 || !latest.UpdatedAt.Equal(report.SampledAt) {
				t.Fatal("invalid report changed runtime state")
			}
			if logs.warnings()-beforeWarnings != 2 {
				t.Fatal("invalid reports changed clamp warning state")
			}
			for name, want := range map[string][]float64{
				metricstore.MetricCPU: {12, 23, 34, 45}, metricstore.MetricRAM: {20, 20, 10, 20},
				metricstore.MetricSwap: {0, 0, 0, 0}, metricstore.MetricDisk: {5, 5, 2, 5},
			} {
				points, err := metricstore.GetStore().Query(t.Context(), metric.Query{MetricName: name, EntityID: uuid,
					Start: report.SampledAt.Add(-time.Minute), End: report.SampledAt.Add(time.Minute), Order: metric.OrderAsc})
				if err != nil || len(points) != len(want) {
					t.Fatalf("persisted %s: %d points, error %v", name, len(points), err)
				}
				for i, point := range points {
					if point.Value != want[i] {
						t.Fatalf("persisted %s[%d] = %v, want %v", name, i, point.Value, want[i])
					}
				}
			}
		})
	}
	if err := metricstore.CloseStoreContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	warnings := logs.warnings()
	if err := ingestReport("failed-store", v2.Report{Ram: v2.RamReport{Used: 2, Total: 1}}, false); err == nil {
		t.Fatal("unavailable metric store accepted report")
	}
	if agentrt.GetLatestReport("failed-store")["failed-store"] != nil || agentrt.IsV2Client("failed-store") || logs.warnings() != warnings {
		t.Fatal("failed persistence changed runtime state or emitted clamp warning")
	}
	if !agentrt.NoteReportClamp("failed-store", "ram") {
		t.Fatal("failed persistence changed clamp suppression state")
	}
}
