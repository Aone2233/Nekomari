package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/cmd/flags"
	"github.com/Aone2233/nekomari/database/clients"
	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/database/tasks"
	"github.com/Aone2233/nekomari/internal/clientguard"
	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/internal/metricstore"
	"github.com/Aone2233/nekomari/internal/scheduler"
	"github.com/Aone2233/nekomari/pkg/metric"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
	"github.com/Aone2233/nekomari/utils"
	agentRuntime "github.com/Aone2233/nekomari/web/agent"
	"github.com/Aone2233/nekomari/web/api"
	"github.com/Aone2233/nekomari/web/connection"
	"github.com/gin-gonic/gin"
)

type pinnedTransportObserver struct {
	diagnostic     atomic.Int32
	disabledPermit atomic.Int32
	disabledReject atomic.Int32
	disabledProbe  atomic.Int32
	slowProbe      atomic.Int32
}

func (*pinnedTransportObserver) OnConnect(*connection.ConnInfo) (bool, string) { return false, "" }
func (o *pinnedTransportObserver) OnMessage(info *connection.ConnInfo, kind int, data []byte) (int, []byte, bool) {
	if info.ClientUUID == "real-transport-no-family" {
		var frame struct {
			Method string                    `json:"method"`
			Params v2.PingFamilyResultParams `json:"params"`
		}
		if json.Unmarshal(data, &frame) == nil && frame.Method == v2.MethodAgentPingFamilyResult && frame.Params.Diagnostic == "no_family_address" {
			o.diagnostic.Add(1)
		}
	}
	if info.ClientUUID == "real-transport-scheduled-nofamily" {
		var frame struct {
			Method string                    `json:"method"`
			Params v2.PingFamilyResultParams `json:"params"`
		}
		if json.Unmarshal(data, &frame) == nil && frame.Method == v2.MethodAgentPingFamilyResult && frame.Params.Diagnostic == "no_family_address" {
			o.diagnostic.Add(1)
		}
	}
	if info.ClientUUID == "real-transport-disabled" || info.ClientUUID == "real-transport-scheduled-disabled" {
		var frame struct {
			Method string       `json:"method"`
			Error  *v2.RPCError `json:"error"`
		}
		if json.Unmarshal(data, &frame) == nil && frame.Method == "" && frame.Error != nil && frame.Error.Code == -32602 {
			o.disabledReject.Add(1)
		}
	}
	return kind, data, false
}
func (o *pinnedTransportObserver) OnSend(info *connection.ConnInfo, kind int, data []byte) (int, []byte, bool) {
	if info.ClientUUID == "real-transport-scheduled-slow" {
		var frame struct {
			Method string `json:"method"`
		}
		if json.Unmarshal(data, &frame) == nil && frame.Method == v2.MethodAgentPingFamilyProbe {
			o.slowProbe.Add(1)
		}
	}
	if info.ClientUUID == "real-transport-disabled" || info.ClientUUID == "real-transport-scheduled-disabled" {
		var frame struct {
			Method string `json:"method"`
		}
		if json.Unmarshal(data, &frame) == nil {
			switch frame.Method {
			case v2.MethodAgentPingFamilyPermit:
				o.disabledPermit.Add(1)
			case v2.MethodAgentPingFamilyProbe:
				o.disabledProbe.Add(1)
			}
		}
	}
	return kind, data, false
}
func (*pinnedTransportObserver) OnClose(*connection.ConnInfo) {}

// The Agent test binary has a test-only probe hook and fake DNS. Everything
// from HTTP authentication to WebSocket result admission is the panel's real
// handler; dispatch is invoked explicitly while the scheduler remains gated.
func TestPinnedRealAgentProductionTransport(t *testing.T) {
	if testing.Short() || runtime.GOOS != "linux" || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") || os.Getenv("CGO_ENABLED") == "0" {
		t.Skip("requires Linux race-enabled Agent build and IPv6 loopback")
	}
	const marker = "NEKOMARI_PINNED_PRODUCTION_TRANSPORT_CHILD"
	if os.Getenv(marker) != "1" {
		_, source, _, ok := runtime.Caller(0)
		if !ok {
			t.Fatal("cannot locate Agent module")
		}
		binary := filepath.Join(t.TempDir(), "agent-server.test")
		build := exec.CommandContext(t.Context(), "go", "test", "-race", "-c", "-o", binary, "./server")
		build.Dir = filepath.Join(filepath.Dir(source), "..", "..", "..", "agent")
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build Agent fixture: %v\n%s", err, out)
		}
		childPattern := "^TestPinnedRealAgentProductionTransport$"
		if selected := os.Getenv("NEKOMARI_PINNED_E2E_SUBTEST"); selected != "" {
			childPattern += "/^" + selected + "$"
		}
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run="+childPattern, "-test.v", "-test.timeout=4m")
		cmd.Dir = t.TempDir()
		cmd.Env = append(os.Environ(), marker+"=1", "NEKOMARI_PINNED_TEST_BINARY="+binary)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated real transport: %v\n%s", err, out)
		}
		return
	}
	listener, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback unavailable: %v", err)
	}
	var accepts sync.WaitGroup
	accepts.Add(1)
	go func() {
		defer accepts.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	defer func() { _ = listener.Close(); accepts.Wait() }()

	flags.DatabaseType = flags.DatabaseTypeSQLite
	flags.DatabaseFile = "file:pinned-transport-main?mode=memory&cache=shared"
	db := dbcore.GetDBInstance()
	if err := config.Set(metricstore.MetricDBDSNKey, "file:pinned-transport-metrics?mode=memory&cache=shared"); err != nil {
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
		if err := dbcore.Close(); err != nil {
			t.Errorf("close main database: %v", err)
		}
	}()
	store := metricstore.GetStore()
	if _, err := store.ListMetrics(t.Context()); err != nil {
		t.Fatal(err)
	}
	observer := &pinnedTransportObserver{}
	connection.SetFrameInterceptor(observer)
	defer connection.SetFrameInterceptor(nil)

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
	endpoint := server.URL + "/api/clients/v2/rpc"
	wsEndpoint := "ws" + strings.TrimPrefix(endpoint, "http")
	httpClient := &http.Client{Timeout: 4 * time.Second}

	type fixture struct {
		uuid, token, start, release, result, ack, ready string
		task                                            models.PingTask
		cmd                                             *exec.Cmd
		output                                          *bytes.Buffer
		done                                            chan struct{}
		waitErr                                         error
	}
	newFixture := func(t *testing.T, name string) *fixture {
		t.Helper()
		f := &fixture{uuid: "real-transport-" + name, token: "fixture-token-" + name}
		if err := db.Create(&models.Client{UUID: f.uuid, Token: f.token}).Error; err != nil {
			t.Fatal(err)
		}
		f.task = models.PingTask{Name: name, Target: "probe.test:80", Type: "auto", Interval: 60,
			TargetFamily: "ipv6", PinnedGeneration: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Clients: models.StringArray{f.uuid}}
		if err := db.Create(&f.task).Error; err != nil {
			t.Fatal(err)
		}
		return f
	}
	launch := func(owner *testing.T, f *fixture, mode string, barrier, disabled bool) {
		owner.Helper()
		f.cmd = exec.Command(os.Getenv("NEKOMARI_PINNED_TEST_BINARY"), "-test.run=^TestPinnedPanelIntegrationChild$", "-test.v", "-test.timeout=100s")
		dir := owner.TempDir()
		f.result, f.ack = filepath.Join(dir, "result"), filepath.Join(dir, "ack")
		f.cmd.Env = append(os.Environ(), "NEKOMARI_PINNED_TEST_WS="+wsEndpoint, "NEKOMARI_PINNED_TEST_MODE="+mode,
			"NEKOMARI_PINNED_TEST_TARGET="+listener.Addr().String(), "NEKOMARI_PINNED_TEST_TOKEN="+f.token,
			"NEKOMARI_PINNED_TEST_RESULT="+f.result, "NEKOMARI_PINNED_TEST_ACK="+f.ack)
		if barrier {
			f.start, f.release = filepath.Join(dir, "started"), filepath.Join(dir, "release")
			f.cmd.Env = append(f.cmd.Env, "NEKOMARI_PINNED_TEST_START="+f.start, "NEKOMARI_PINNED_TEST_RELEASE="+f.release)
		}
		if disabled {
			f.ready = filepath.Join(dir, "ready")
			f.cmd.Env = append(f.cmd.Env, "NEKOMARI_PINNED_TEST_DISABLE_PROBE=1", "NEKOMARI_PINNED_TEST_READY="+f.ready)
		}
		if owner.Name() == "TestPinnedRealAgentProductionTransport/scheduled_post_retirement_recovers_with_normal_heartbeat" {
			f.cmd.Env = append(f.cmd.Env, "NEKOMARI_PINNED_TEST_NORMAL_HEARTBEAT=1")
		}
		f.output = &bytes.Buffer{}
		f.cmd.Stdout, f.cmd.Stderr = f.output, f.output
		if err := f.cmd.Start(); err != nil {
			owner.Fatal(err)
		}
		f.done = make(chan struct{})
		go func() { f.waitErr = f.cmd.Wait(); close(f.done) }()
		waitUntilConnection := time.Now().Add(5 * time.Second)
		for agentRuntime.GetConnectedClient(f.uuid) == nil {
			if time.Now().After(waitUntilConnection) {
				_ = f.cmd.Process.Kill()
				<-f.done
				owner.Fatalf("Agent %s did not connect", f.uuid)
			}
			time.Sleep(10 * time.Millisecond)
		}
		ownedSocket := agentRuntime.GetConnectedClient(f.uuid)
		owner.Cleanup(func() {
			if ownedSocket != nil {
				ownedSocket.Abort()
			}
			select {
			case <-f.done:
				if f.waitErr != nil {
					owner.Errorf("Agent %s: %v\n%s", f.uuid, f.waitErr, f.output.String())
				}
			case <-time.After(5 * time.Second):
				_ = f.cmd.Process.Kill()
				<-f.done
				owner.Errorf("Agent %s did not stop", f.uuid)
			}
			ResetClientState(f.uuid)
		})
	}
	post := func(t *testing.T, token, method string, params any) (int, v2.Response) {
		t.Helper()
		payload, err := json.Marshal(v2.Request{JSONRPC: v2.Version, ID: 1, Method: method, Params: params})
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := httpClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var result v2.Response
		if err := json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&result); err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, result
	}
	points := func(t *testing.T, f *fixture) []metric.Point {
		t.Helper()
		out, err := store.Query(context.Background(), metric.Query{MetricName: metricstore.MetricPingLoss, EntityID: f.uuid,
			Tags: map[string]string{"task_id": fmt.Sprint(f.task.Id)}, Start: time.Now().Add(-time.Minute), End: time.Now().Add(time.Minute)})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	waitFor := func(t *testing.T, limit time.Duration, what string, check func() bool) {
		t.Helper()
		deadline := time.Now().Add(limit)
		for !check() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s", what)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	waitUntil := func(t *testing.T, what string, check func() bool) {
		t.Helper()
		waitFor(t, 8*time.Second, what, check)
	}
	dispatch := func(t *testing.T, f *fixture) {
		t.Helper()
		waitUntil(t, "Agent handshake and dispatch", func() bool {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			return agentRuntime.DispatchPinnedPing(ctx, f.uuid, f.task) == nil
		})
	}

	t.Run("ipv6_through_authenticated_handler", func(t *testing.T) {
		f := newFixture(t, "ipv6")
		launch(t, f, "ipv6", false, false)
		dispatch(t, f)
		waitUntil(t, "IPv6 measurement", func() bool { return len(points(t, f)) == 1 })
		got := points(t, f)
		if got[0].Value != 0 || got[0].Tags["family"] != "ipv6" || got[0].Tags["protocol"] != "tcp" {
			t.Fatalf("unexpected point: %+v", got[0])
		}
		if code, _ := post(t, "invalid-"+f.token, v2.MethodAgentBasicInfo, nil); code != http.StatusUnauthorized {
			t.Fatalf("invalid credential status=%d", code)
		}
		if code, resp := post(t, f.token, v2.MethodAgentPingFamilyResult, map[string]any{}); code != http.StatusBadRequest || resp.Error == nil || resp.Error.Code != -32601 {
			t.Fatalf("POST accepted WS-only result: status=%d response=%+v", code, resp)
		}
		// The POST retired the old session; this dispatch succeeds only after
		// the actual Agent's accelerated re-handshake completes.
		dispatch(t, f)
		waitUntil(t, "measurement after POST recovery", func() bool { return len(points(t, f)) == 2 })
	})
	t.Run("no_ipv6_answer_is_not_loss", func(t *testing.T) {
		f := newFixture(t, "no-family")
		launch(t, f, "no-family", false, false)
		dispatch(t, f)
		waitUntil(t, "diagnostic sent by real Agent", func() bool { _, err := os.Stat(f.result); return err == nil })
		waitUntil(t, "diagnostic received by production handler", func() bool { return observer.diagnostic.Load() == 1 })
		// A response on the same ordered socket establishes that the preceding
		// notification was fully handled before inspecting the metric store.
		waitUntil(t, "diagnostic admission barrier", func() bool { _, err := os.Stat(f.ack); return err == nil })
		if got := points(t, f); len(got) != 0 {
			t.Fatalf("non-probe produced loss points: %+v", got)
		}
	})
	t.Run("rotation_cancels_inflight_probe", func(t *testing.T) {
		f := newFixture(t, "rotation")
		launch(t, f, "ipv6", true, false)
		dispatch(t, f)
		waitUntil(t, "in-flight Agent dial", func() bool { _, err := os.Stat(f.start); return err == nil })
		if _, err := clients.RotateClientTokenAndReset(context.Background(), f.uuid, func() { ResetClientState(f.uuid) }); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f.release, []byte("release\n"), 0600); err != nil {
			t.Fatal(err)
		}
		waitUntil(t, "credential retirement", func() bool { return agentRuntime.GetConnectedClient(f.uuid) == nil })
		// The test Agent waits for its in-flight probe to finish after the
		// socket closes. Check the store only after that boundary.
		select {
		case <-f.done:
			if f.waitErr != nil {
				t.Fatalf("Agent %s: %v\n%s", f.uuid, f.waitErr, f.output.String())
			}
		case <-time.After(8 * time.Second):
			t.Fatal("retired Agent probe did not finish")
		}
		if got := points(t, f); len(got) != 0 {
			t.Fatalf("retired probe wrote points: %+v", got)
		}
		if code, _ := post(t, f.token, v2.MethodAgentPingResult, v2.PingResultParams{TaskID: f.task.Id}); code != http.StatusUnauthorized {
			t.Fatalf("old credential still accepted: %d", code)
		}
	})
	t.Run("post_retirement_fences_inflight_probe_and_recovers", func(t *testing.T) {
		f := newFixture(t, "post-retirement")
		launch(t, f, "ipv6", true, false)
		dispatch(t, f)
		waitUntil(t, "in-flight Agent dial before POST", func() bool { _, err := os.Stat(f.start); return err == nil })
		// The unsupported POST is still authenticated and retires the old
		// session. Its in-flight probe must not become a measurement.
		if code, resp := post(t, f.token, v2.MethodAgentPingFamilyResult, map[string]any{}); code != http.StatusBadRequest || resp.Error == nil || resp.Error.Code != -32601 {
			t.Fatalf("WS-only POST: status=%d response=%+v", code, resp)
		}
		if err := os.WriteFile(f.release, []byte("release\n"), 0600); err != nil {
			t.Fatal(err)
		}
		// A subsequent direct dispatch proves that the real transport can
		// recover after re-handshake; an old sample would leave two points.
		dispatch(t, f)
		waitUntil(t, "post-retirement sample", func() bool { return len(points(t, f)) > 0 })
		waitUntil(t, "post-retirement admission barrier", func() bool { _, err := os.Stat(f.ack); return err == nil })
		if got := points(t, f); len(got) != 1 || got[0].Value != 0 || got[0].Tags["family"] != "ipv6" {
			t.Fatalf("retired probe affected recovered measurements: %+v", got)
		}
	})
	t.Run("disabled_agent_refuses_capability", func(t *testing.T) {
		f := newFixture(t, "disabled")
		launch(t, f, "ipv6", false, true)
		waitUntil(t, "Agent handshake without probe hook", func() bool { _, err := os.Stat(f.ready); return err == nil })
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		if err := agentRuntime.DispatchPinnedPing(ctx, f.uuid, f.task); err == nil {
			t.Fatal("disabled Agent authorized a pinned dispatch")
		}
		if observer.disabledPermit.Load() != 1 || observer.disabledReject.Load() != 1 || observer.disabledProbe.Load() != 0 {
			t.Fatalf("permit=%d reject=%d probe=%d", observer.disabledPermit.Load(), observer.disabledReject.Load(), observer.disabledProbe.Load())
		}
		snapshot, err := agentRuntime.SnapshotPinnedDiagnostic(ctx, f.task.Id, f.uuid)
		if err != nil || snapshot.State != "session_active" || len(snapshot.Legs) != 1 || snapshot.Legs[0].Status != "permit_unavailable" {
			t.Fatalf("refused permit diagnostic: %+v err=%v", snapshot, err)
		}
		if got := points(t, f); len(got) != 0 {
			t.Fatalf("disabled Agent wrote points: %+v", got)
		}
	})
	t.Run("isolated_scheduler_three_agent_workflow", func(t *testing.T) {
		f6 := newFixture(t, "scheduled-ipv6")
		fNoFamily := newFixture(t, "scheduled-nofamily")
		fDisabled := newFixture(t, "scheduled-disabled")
		launch(t, f6, "ipv6", false, false)
		launch(t, fNoFamily, "no-family", false, false)
		launch(t, fDisabled, "ipv6", false, true)
		waitUntil(t, "disabled Agent handshake", func() bool { _, err := os.Stat(fDisabled.ready); return err == nil })

		beforeDiagnostic := observer.diagnostic.Load()
		beforeReject := observer.disabledReject.Load()
		beforeProbe := observer.disabledProbe.Load()
		mgr := utils.NewPingTaskManagerWithScheduler(scheduler.NewManager(), agentRuntime.DispatchPinnedPing)
		defer mgr.Stop()
		task := models.PingTask{Name: "scheduled-three-agents", Target: "probe.test:80", Type: "auto", Interval: 1,
			TargetFamily: "ipv6", Clients: models.StringArray{f6.uuid, fNoFamily.uuid, fDisabled.uuid}}
		// Inject an isolated scheduler into the same persisted draft-creation
		// path used by the separate RPC/REST fixture. Production stays gated.
		if err := tasks.AddPingTaskPinnedDraft(t.Context(), db, &task, func() error {
			return mgr.Reload([]models.PingTask{task})
		}); err != nil {
			t.Fatal(err)
		}
		if task.Id == 0 || len(task.PinnedGeneration) != 32 {
			t.Fatalf("draft creation did not persist identity: %+v", task)
		}
		f6.task, fNoFamily.task, fDisabled.task = task, task, task
		waitUntil(t, "scheduled IPv6 result", func() bool { return len(points(t, f6)) > 0 })
		waitUntil(t, "scheduled no-family diagnostic", func() bool { return observer.diagnostic.Load() > beforeDiagnostic })
		waitUntil(t, "scheduled disabled-Agent rejection", func() bool { return observer.disabledReject.Load() > beforeReject })
		waitUntil(t, "no-family admission barrier", func() bool { _, err := os.Stat(fNoFamily.ack); return err == nil })
		if got := observer.disabledProbe.Load(); got != beforeProbe {
			t.Fatalf("disabled Agent received %d pinned probe frames", got-beforeProbe)
		}
		if got := points(t, fNoFamily); len(got) != 0 {
			t.Fatalf("no-family Agent wrote loss points: %+v", got)
		}
		if got := points(t, fDisabled); len(got) != 0 {
			t.Fatalf("disabled Agent wrote loss points: %+v", got)
		}
		for _, point := range points(t, f6) {
			if point.Value != 0 || point.Tags["family"] != "ipv6" || point.Tags["protocol"] != "tcp" {
				t.Fatalf("scheduled result = %+v", point)
			}
		}
		for _, f := range []*fixture{f6, fNoFamily, fDisabled} {
			if events := agentRuntime.TakeV2Events(f.uuid, nil, 0); len(events) != 0 {
				t.Fatalf("pinned task reached legacy queue for %s: %+v", f.uuid, events)
			}
		}
	})
	t.Run("slow_scheduled_probe_survives_multiple_intervals", func(t *testing.T) {
		f := newFixture(t, "scheduled-slow")
		launch(t, f, "ipv6", true, false)
		mgr := utils.NewPingTaskManagerWithScheduler(scheduler.NewManager(), agentRuntime.DispatchPinnedPing)
		defer mgr.Stop()
		task := models.PingTask{Name: "scheduled-slow", Target: "probe.test:80", Type: "auto", Interval: 1,
			TargetFamily: "ipv6", Clients: models.StringArray{f.uuid}}
		if err := tasks.AddPingTaskPinnedDraft(t.Context(), db, &task, func() error {
			return mgr.Reload([]models.PingTask{task})
		}); err != nil {
			t.Fatal(err)
		}
		f.task = task
		waitUntil(t, "slow probe start", func() bool { _, err := os.Stat(f.start); return err == nil })
		time.Sleep(2250 * time.Millisecond)
		if got := observer.slowProbe.Load(); got != 1 {
			t.Fatalf("sent %d overlapping probes while the first was still running", got)
		}
		mgr.Stop()
		if err := os.WriteFile(f.release, []byte("release\n"), 0600); err != nil {
			t.Fatal(err)
		}
		waitUntil(t, "slow probe measurement", func() bool { return len(points(t, f)) > 0 })
		if got := points(t, f); len(got) != 1 || got[0].Value != 0 || got[0].Tags["family"] != "ipv6" {
			t.Fatalf("slow probe measurement = %+v", got)
		}
	})
	t.Run("scheduled_post_retirement_recovers_with_normal_heartbeat", func(t *testing.T) {
		f := newFixture(t, "scheduled-normal-heartbeat")
		launch(t, f, "ipv6", false, false)
		mgr := utils.NewPingTaskManagerWithScheduler(scheduler.NewManager(), agentRuntime.DispatchPinnedPing)
		defer mgr.Stop()
		task := models.PingTask{Name: "scheduled-normal-heartbeat", Target: "probe.test:80", Type: "auto", Interval: 1,
			TargetFamily: "ipv6", Clients: models.StringArray{f.uuid}}
		if err := tasks.AddPingTaskPinnedDraft(t.Context(), db, &task, func() error {
			return mgr.Reload([]models.PingTask{task})
		}); err != nil {
			t.Fatal(err)
		}
		f.task = task
		waitFor(t, 35*time.Second, "first normal-heartbeat scheduled sample", func() bool { return len(points(t, f)) > 0 })
		code, response := post(t, f.token, v2.MethodAgentPingFamilyResult, map[string]any{})
		if code != http.StatusBadRequest || response.Error == nil || response.Error.Code != -32601 {
			t.Fatalf("WS-only POST: status=%d response=%+v", code, response)
		}
		var baseline []metric.Point
		if err := clientguard.With(t.Context(), f.uuid, func() error {
			baseline = points(t, f)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		// A retired result cannot append after this credential-fence barrier.
		// An ack at the next 25-second tick discovers retirement. Observe the
		// gap and recovery after the fresh challenge and scheduler firing.
		time.Sleep(2 * time.Second)
		if got := points(t, f); !reflect.DeepEqual(got, baseline) {
			t.Fatalf("points changed before session recovery: before=%+v after=%+v", baseline, got)
		}
		waitFor(t, 35*time.Second, "scheduled sample after normal-heartbeat recovery", func() bool { return len(points(t, f)) > len(baseline) })
		for _, point := range points(t, f) {
			if point.Value != 0 || point.Tags["family"] != "ipv6" || point.Tags["protocol"] != "tcp" {
				t.Fatalf("scheduled result after POST = %+v", point)
			}
		}
	})
}
