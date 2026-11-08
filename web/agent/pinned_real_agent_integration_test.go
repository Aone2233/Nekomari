package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/clientguard"
	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/internal/metricstore"
	"github.com/Aone2233/nekomari/pkg/metric"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
	"github.com/Aone2233/nekomari/web/connection"
	"github.com/gorilla/websocket"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Exercise the real Agent controller over loopback against panel handshake,
// delivery and admission helpers. This is not a test of HTTP authentication or
// the scheduler; the latter does not dispatch pinned tasks yet.
func TestPinnedRealAgentPanelHelpers(t *testing.T) {
	if testing.Short() || runtime.GOOS != "linux" || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") || os.Getenv("CGO_ENABLED") == "0" {
		t.Skip("requires a Linux race-enabled Agent build and IPv6 loopback")
	}
	const isolated = "NEKOMARI_PINNED_PANEL_HELPERS_CHILD"
	if os.Getenv(isolated) != "1" {
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestPinnedRealAgentPanelHelpers$", "-test.v", "-test.timeout=5m")
		cmd.Dir = t.TempDir()
		cmd.Env = append(os.Environ(), isolated+"=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated panel fixture: %v\n%s", err, out)
		}
		return
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate Agent module")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	binary := filepath.Join(t.TempDir(), "agent-server.test")
	buildCtx, cancelBuild := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancelBuild()
	build := exec.CommandContext(buildCtx, "go", "test", "-race", "-c", "-o", binary, "./server")
	build.Dir = filepath.Join(root, "agent")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build Agent test: %v\n%s", err, out)
	}

	listener, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback unavailable: %v", err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "panel.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.Client{}, &models.PingTask{}, &models.PingTaskDeletion{}); err != nil {
		t.Fatal(err)
	}
	config.SetDb(db)
	if err := config.Set(metricstore.MetricDBDSNKey, filepath.Join(t.TempDir(), "metrics.db")); err != nil {
		t.Fatal(err)
	}
	if err := metricstore.InitializeStore(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := metricstore.CloseStoreContext(ctx); err != nil {
			t.Errorf("close metric store: %v", err)
		}
	}()
	store := metricstore.GetStore()

	for _, mode := range []string{"ipv6", "no-family"} {
		t.Run(mode, func(t *testing.T) {
			uuid := "real-agent-" + mode
			token := "fixture-token-" + mode
			if err := db.Create(&models.Client{UUID: uuid, Token: token}).Error; err != nil {
				t.Fatal(err)
			}
			task := models.PingTask{Name: "fixture", Target: "probe.test:80", Type: "auto", Interval: 60,
				TargetFamily: "ipv6", PinnedGeneration: pinnedTestInstanceA, Clients: models.StringArray{uuid}}
			if err := db.Create(&task).Error; err != nil {
				t.Fatal(err)
			}
			results := make(chan v2.PingFamilyResultParams, 1)
			errors := make(chan error, 1)
			done := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(done)
				if r.Header.Get("Authorization") != "Bearer "+token {
					http.Error(w, "invalid test credential", http.StatusUnauthorized)
					return
				}
				raw, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					errors <- err
					return
				}
				conn := connection.NewSafeConn(raw)
				defer conn.Close()
				SetConnectedClients(uuid, conn)
				defer DeleteClientConditionally(uuid, conn)
				for {
					_, wire, err := conn.ReadMessage()
					if err != nil {
						return
					}
					ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
					err = clientguard.With(ctx, uuid, func() error {
						if ConsumePinnedWSPermitResponse(uuid, conn, wire) {
							return nil
						}
						var req v2.RawRequest
						if err := json.Unmarshal(wire, &req); err != nil || req.JSONRPC != v2.Version {
							return fmt.Errorf("invalid test frame: %v", err)
						}
						switch req.Method {
						case "fixture.barrier":
							return conn.WriteJSON(v2.Success(req.ID, nil))
						case v2.MethodAgentPingFamilySession:
							var p v2.PingFamilySessionParams
							if err := req.DecodeParams(&p); err != nil || p.Version != 1 || req.ID == nil {
								return conn.WriteJSON(v2.Error(req.ID, -32602, "invalid handshake params", nil))
							}
							challenge := p.Challenge
							var ok bool
							if p.Action == "begin" && challenge == "" {
								challenge, ok = BeginPinnedWSHandshake(uuid, conn, p.Instance, p.Version)
							} else if p.Action == "ack" {
								ok = AckPinnedWSHandshake(uuid, conn, p.Instance, challenge)
							}
							if !ok {
								return conn.WriteJSON(v2.Error(req.ID, -32602, "invalid or expired handshake", nil))
							}
							return conn.WriteJSON(v2.Success(req.ID, v2.PingFamilySessionResult{Challenge: challenge}))
						case v2.MethodAgentPingFamilyResult:
							var result v2.PingFamilyResultParams
							if err := req.DecodeParams(&result); err != nil || req.ID != nil {
								return fmt.Errorf("invalid result: %v", err)
							}
							if err := admitPinnedResultWith(ctx, db, uuid, conn, result, metricstore.WritePingRecordSync); err != nil {
								return err
							}
							if err := admitPinnedResultWith(ctx, db, uuid, conn, result, metricstore.WritePingRecordSync); err == nil {
								return fmt.Errorf("duplicate Agent result was accepted")
							}
							results <- result
							return nil
						default:
							return fmt.Errorf("unexpected Agent method %q", req.Method)
						}
					})
					cancel()
					if err != nil {
						errors <- err
						return
					}
				}
			}))

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			cmd := exec.CommandContext(ctx, binary, "-test.run=^TestPinnedPanelIntegrationChild$", "-test.v")
			cmd.Env = append(os.Environ(), "NEKOMARI_PINNED_TEST_WS=ws"+strings.TrimPrefix(server.URL, "http"),
				"NEKOMARI_PINNED_TEST_MODE="+mode, "NEKOMARI_PINNED_TEST_TARGET="+listener.Addr().String(), "NEKOMARI_PINNED_TEST_TOKEN="+token)
			output := new(strings.Builder)
			cmd.Stdout, cmd.Stderr = output, output
			if err := cmd.Start(); err != nil {
				cancel()
				server.Close()
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if conn := GetConnectedClient(uuid); conn != nil {
					conn.Abort()
				}
				server.Close()
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Error("test WebSocket handler did not stop")
				}
				if err := cmd.Wait(); err != nil {
					t.Errorf("Agent process: %v\n%s", err, output.String())
				}
				cancel()
				ResetClient(uuid)
			})
			var lease pinnedPermitLease
			for {
				lease, err = requestPinnedWSPermit(ctx, uuid)
				if err == nil {
					break
				}
				select {
				case err := <-errors:
					t.Fatalf("panel harness: %v", err)
				case <-ctx.Done():
					t.Fatalf("Agent handshake: %v", err)
				case <-time.After(20 * time.Millisecond):
				}
			}
			if err := metricstore.PreparePinnedPingHistory(ctx, uuid, task); err != nil {
				t.Fatal(err)
			}
			if err := deliverPinnedPingWith(ctx, db, uuid, task, lease, func(ctx context.Context, conn *connection.SafeConn, req v2.Request) error {
				return conn.WriteJSONContext(ctx, req)
			}); err != nil {
				t.Fatal(err)
			}
			var result v2.PingFamilyResultParams
			select {
			case result = <-results:
			case err := <-errors:
				t.Fatalf("panel harness: %v", err)
			case <-ctx.Done():
				t.Fatal("Agent did not return a result")
			}
			points, err := store.Query(ctx, metric.Query{MetricName: metricstore.MetricPingLoss, EntityID: uuid,
				Tags: map[string]string{"task_id": fmt.Sprint(task.Id)}, Start: time.Now().Add(-time.Minute), End: time.Now().Add(time.Minute)})
			if err != nil {
				t.Fatal(err)
			}
			if mode == "no-family" {
				if result.Diagnostic != "no_family_address" || result.Value != nil || len(points) != 0 {
					t.Fatalf("non-probe became a loss: result=%+v points=%+v", result, points)
				}
			} else if result.Diagnostic != "" || result.Family != "ipv6" || result.Value == nil || *result.Value < 0 || len(points) != 1 || points[0].Value != 0 {
				t.Fatalf("IPv6 result was not stored: result=%+v points=%+v", result, points)
			}
			if mode == "no-family" {
				return
			}
			// An authenticated POST currently retires the WS capability, even
			// when the real Agent still has an open socket. Observe both the
			// fail-closed gap and its actual renewal/re-handshake recovery.
			if err := clientguard.With(ctx, uuid, func() error { RetirePinnedWSOnPOST(uuid); return nil }); err != nil {
				t.Fatal(err)
			}
			if _, err := requestPinnedWSPermit(ctx, uuid); err == nil {
				t.Fatal("retired session issued a permit")
			}
			oldSession := lease.session
			for {
				lease, err = requestPinnedWSPermit(ctx, uuid)
				if err == nil {
					break
				}
				select {
				case err := <-errors:
					t.Fatalf("panel harness during recovery: %v", err)
				case <-ctx.Done():
					t.Fatalf("Agent did not recover after POST: %v", err)
				case <-time.After(20 * time.Millisecond):
				}
			}
			if lease.session == oldSession {
				t.Fatal("retired session was reused")
			}
			if err := deliverPinnedPingWith(ctx, db, uuid, task, lease, func(ctx context.Context, conn *connection.SafeConn, req v2.Request) error {
				return conn.WriteJSONContext(ctx, req)
			}); err != nil {
				t.Fatal(err)
			}
			select {
			case result = <-results:
				if result.Diagnostic != "" || result.Family != "ipv6" || result.Value == nil || *result.Value < 0 {
					t.Fatalf("unexpected result after recovery: %+v", result)
				}
			case err := <-errors:
				t.Fatalf("panel harness after recovery: %v", err)
			case <-ctx.Done():
				t.Fatal("Agent did not probe after recovery")
			}
			points, err = store.Query(ctx, metric.Query{MetricName: metricstore.MetricPingLoss, EntityID: uuid,
				Tags: map[string]string{"task_id": fmt.Sprint(task.Id)}, Start: time.Now().Add(-time.Minute), End: time.Now().Add(time.Minute)})
			if err != nil || len(points) != 2 {
				t.Fatalf("recovered metric count=%d err=%v", len(points), err)
			}
		})
	}
}
