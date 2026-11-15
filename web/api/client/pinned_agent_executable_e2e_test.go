//go:build linux

package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http/httptest"
	"net/netip"
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
	"github.com/Aone2233/nekomari/pkg/metric"
	"github.com/Aone2233/nekomari/utils"
	agentRuntime "github.com/Aone2233/nekomari/web/agent"
	"github.com/Aone2233/nekomari/web/api"
	"github.com/gin-gonic/gin"
	"golang.org/x/net/dns/dnsmessage"
)

// This opt-in fixture uses actual Agent executables and DNS sockets inside a
// loopback-only network namespace. The panel scheduler remains test-injected.
func TestPinnedActualAgentExecutablesWithDNS(t *testing.T) {
	if os.Getenv("NEKOMARI_PINNED_EXEC_E2E") != "1" {
		t.Skip("set NEKOMARI_PINNED_EXEC_E2E=1; requires Linux network namespace privileges")
	}
	const child = "NEKOMARI_PINNED_EXEC_CHILD"
	if os.Getenv(child) != "1" {
		_, source, _, ok := runtime.Caller(0)
		if !ok {
			t.Fatal("cannot locate Agent module")
		}
		root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", ".."))
		binary := filepath.Join(t.TempDir(), "komari-agent")
		build := exec.CommandContext(t.Context(), "go", "build", "-race", "-o", binary, ".")
		build.Dir = filepath.Join(root, "agent")
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build Agent executable: %v\n%s", err, out)
		}
		cmd := exec.CommandContext(t.Context(), "unshare", "--net", "--", os.Args[0],
			"-test.run=^TestPinnedActualAgentExecutablesWithDNS$", "-test.v", "-test.timeout=90s")
		cmd.Dir = t.TempDir()
		cmd.Env = append(os.Environ(), child+"=1", "NEKOMARI_PINNED_EXEC_BINARY="+binary)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated executable fixture: %v\n%s", err, out)
		}
		return
	}
	if out, err := exec.CommandContext(t.Context(), "ip", "link", "set", "lo", "up").CombinedOutput(); err != nil {
		t.Fatalf("enable isolated loopback: %v\n%s", err, out)
	}
	listener, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Fatalf("isolated IPv6 loopback unavailable: %v", err)
	}
	var accepted sync.WaitGroup
	accepted.Go(func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	})
	defer func() { _ = listener.Close(); accepted.Wait() }()

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
			t.Errorf("close scratch metrics: %v", err)
		}
		if err := dbcore.Close(); err != nil {
			t.Errorf("close scratch database: %v", err)
		}
	}()
	store := metricstore.GetStore()
	if _, err := store.ListMetrics(t.Context()); err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(api.IdentityMiddleware())
	group := router.Group("/api/clients", api.RequireRole(api.RoleClient))
	group.POST("/v2/rpc", UploadV2RPC)
	var sockets sync.WaitGroup
	group.GET("/v2/rpc", func(c *gin.Context) {
		sockets.Add(1)
		defer sockets.Done()
		WebSocketV2RPC(c)
	})
	server := httptest.NewServer(router)
	defer func() { server.Close(); sockets.Wait() }()

	type fixture struct {
		id      string
		cmd     *exec.Cmd
		output  *bytes.Buffer
		done    chan error
		dns     net.PacketConn
		dnsDone chan struct{}
	}
	nodes := make([]string, 0, 3)
	fixtures := make([]fixture, 0, 3)
	defer func() {
		for _, f := range fixtures {
			if f.cmd != nil && f.cmd.Process != nil {
				_ = f.cmd.Process.Signal(os.Interrupt)
				select {
				case err := <-f.done:
					if err != nil {
						t.Errorf("Agent %s exited: %v\n%s", f.id, err, f.output.String())
					}
				case <-time.After(5 * time.Second):
					_ = f.cmd.Process.Kill()
					<-f.done
					t.Errorf("Agent %s did not stop", f.id)
				}
			}
			_ = f.dns.Close()
			<-f.dnsDone
			ResetClientState(f.id)
			if t.Failed() && f.output != nil {
				t.Logf("Agent %s output:\n%s", f.id, f.output.String())
			}
		}
	}()
	for i := range 3 {
		id := fmt.Sprintf("executable-agent-%d", i+1)
		token := fmt.Sprintf("executable-fixture-token-%d", i+1)
		if err := db.Create(&models.Client{UUID: id, Token: token, Name: id}).Error; err != nil {
			t.Fatal(err)
		}
		dns, err := net.ListenPacket("udp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		dnsDone := make(chan struct{})
		respondIPv6 := i < 2
		go func() {
			defer close(dnsDone)
			buf := make([]byte, 4096)
			for {
				n, peer, err := dns.ReadFrom(buf)
				if err != nil {
					return
				}
				var request dnsmessage.Message
				if request.Unpack(buf[:n]) != nil || len(request.Questions) != 1 {
					continue
				}
				question := request.Questions[0]
				answer := dnsmessage.Message{Header: dnsmessage.Header{ID: request.ID, Response: true, Authoritative: true, RecursionAvailable: true}, Questions: request.Questions}
				if question.Name.String() == "probe.example.invalid." && question.Type == dnsmessage.TypeAAAA && respondIPv6 {
					answer.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: question.Name, Type: dnsmessage.TypeAAAA, Class: dnsmessage.ClassINET, TTL: 1},
						Body: &dnsmessage.AAAAResource{AAAA: netip.MustParseAddr("::1").As16()}}}
				}
				if wire, err := answer.Pack(); err == nil {
					_, _ = dns.WriteTo(wire, peer)
				}
			}
		}()
		fixtures = append(fixtures, fixture{id: id, dns: dns, dnsDone: dnsDone})
		cmd := exec.Command(os.Getenv("NEKOMARI_PINNED_EXEC_BINARY"), "--endpoint", server.URL,
			"--custom-dns", dns.LocalAddr().String(), "--disable-auto-update", "--disable-web-ssh", "--interval", "1")
		cmd.Dir = t.TempDir()
		cmd.Env = append(os.Environ(), "AGENT_TOKEN="+token, "HTTP_PROXY=", "HTTPS_PROXY=", "ALL_PROXY=", "NO_PROXY=*")
		output := &bytes.Buffer{}
		cmd.Stdout, cmd.Stderr = output, output
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		fixtures[len(fixtures)-1].cmd, fixtures[len(fixtures)-1].output, fixtures[len(fixtures)-1].done = cmd, output, done
		nodes = append(nodes, id)
	}

	task := models.PingTask{Name: "actual executables", Type: "auto", Target: net.JoinHostPort("probe.example.invalid.", fmt.Sprint(listener.Addr().(*net.TCPAddr).Port)),
		TargetFamily: "ipv6", PinnedGeneration: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Interval: 2, Clients: nodes}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	mgr := utils.NewPingTaskManagerWithScheduler(scheduler.NewManager(), agentRuntime.DispatchPinnedPing)
	if err := mgr.Reload([]models.PingTask{task}); err != nil {
		t.Fatal(err)
	}
	defer mgr.Stop()
	points := func(id string) []metric.Point {
		t.Helper()
		values, err := store.Query(t.Context(), metric.Query{MetricName: metricstore.MetricPingLoss, EntityID: id,
			Tags: map[string]string{"task_id": fmt.Sprint(task.Id)}, Start: time.Now().Add(-time.Minute), End: time.Now().Add(time.Minute)})
		if err != nil {
			t.Fatal(err)
		}
		return values
	}
	deadline := time.Now().Add(25 * time.Second)
	for {
		allConnected := true
		for _, id := range nodes {
			allConnected = allConnected && agentRuntime.GetConnectedClient(id) != nil
		}
		if allConnected && len(points(nodes[0])) > 0 && len(points(nodes[1])) > 0 {
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			snapshot, err := agentRuntime.SnapshotPinnedDiagnostic(ctx, task.Id, nodes[2])
			cancel()
			if err == nil && len(snapshot.Legs) == 1 && snapshot.Legs[0].Status == "dns_error" {
				break
			}
		}
		if time.Now().After(deadline) {
			for _, id := range nodes {
				ctx, cancel := context.WithTimeout(t.Context(), time.Second)
				snapshot, err := agentRuntime.SnapshotPinnedDiagnostic(ctx, task.Id, id)
				cancel()
				encoded, _ := json.Marshal(snapshot)
				t.Logf("%s diagnostic=%s error=%v points=%d", id, encoded, err, len(points(id)))
			}
			t.Fatal("timed out waiting for executable Agent observations")
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, id := range nodes[:2] {
		for _, point := range points(id) {
			if point.Value != 0 || point.Tags["family"] != "ipv6" || point.Tags["protocol"] != "tcp" {
				t.Fatalf("unexpected %s result: %+v", id, point)
			}
		}
	}
	if got := points(nodes[2]); len(got) != 0 {
		t.Fatalf("missing AAAA answer created false loss: %+v", got)
	}
	for _, id := range nodes {
		if events := agentRuntime.TakeV2Events(id, nil, 0); len(events) != 0 {
			t.Fatalf("pinned work entered legacy queue for %s: %+v", id, events)
		}
	}
}
