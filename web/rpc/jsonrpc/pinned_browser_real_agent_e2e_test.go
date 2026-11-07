//go:build linux

package jsonrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/accounts"
	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/internal/metricstore"
	"github.com/Aone2233/nekomari/pkg/metric"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
	"github.com/Aone2233/nekomari/utils"
	agentRuntime "github.com/Aone2233/nekomari/web/agent"
	"github.com/Aone2233/nekomari/web/api"
	clientAPI "github.com/Aone2233/nekomari/web/api/client"
	"github.com/Aone2233/nekomari/web/connection"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type browserAgentObserver struct {
	noFamily      atomic.Int32
	disabledProbe atomic.Int32
	disabledDeny  atomic.Int32
}

func (*browserAgentObserver) OnConnect(*connection.ConnInfo) (bool, string) { return false, "" }
func (*browserAgentObserver) OnClose(*connection.ConnInfo)                  {}
func (o *browserAgentObserver) OnMessage(info *connection.ConnInfo, kind int, data []byte) (int, []byte, bool) {
	var frame struct {
		Method string                    `json:"method"`
		Params v2.PingFamilyResultParams `json:"params"`
		Error  *v2.RPCError              `json:"error"`
	}
	if json.Unmarshal(data, &frame) == nil {
		if info.ClientUUID == "browser-nofamily" && frame.Method == v2.MethodAgentPingFamilyResult && frame.Params.Diagnostic == "no_family_address" {
			o.noFamily.Add(1)
		}
		if info.ClientUUID == "browser-disabled" && frame.Method == "" && frame.Error != nil && frame.Error.Code == -32602 {
			o.disabledDeny.Add(1)
		}
	}
	return kind, data, false
}
func (o *browserAgentObserver) OnSend(info *connection.ConnInfo, kind int, data []byte) (int, []byte, bool) {
	var frame struct {
		Method string `json:"method"`
	}
	if info.ClientUUID == "browser-disabled" && json.Unmarshal(data, &frame) == nil && frame.Method == v2.MethodAgentPingFamilyProbe {
		o.disabledProbe.Add(1)
	}
	return kind, data, false
}

// Opt-in only: a mounted browser sends a real, session-authenticated request
// through the production RPC and scheduler to a scratch DB. The Agent binaries
// use fake DNS and an IPv6 loopback server.
func TestPinnedBrowserThroughSchedulerAndRealAgents(t *testing.T) {
	if os.Getenv("NEKOMARI_PINNED_BROWSER_E2E") != "1" {
		t.Skip("set NEKOMARI_PINNED_BROWSER_E2E=1 with Playwright and frontend dependencies installed")
	}
	if testing.Short() || runtime.GOOS != "linux" || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") || os.Getenv("CGO_ENABLED") == "0" {
		t.Skip("requires Linux race-enabled Agent build and IPv6 loopback")
	}
	const child = "NEKOMARI_PINNED_BROWSER_CHILD"
	if os.Getenv(child) != "1" {
		_, source, _, ok := runtime.Caller(0)
		if !ok {
			t.Fatal("cannot locate repository")
		}
		root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", ".."))
		binary := filepath.Join(t.TempDir(), "agent-server.test")
		build := exec.CommandContext(t.Context(), "go", "test", "-race", "-c", "-o", binary, "./server")
		build.Dir = filepath.Join(root, "agent")
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build isolated Agent fixture: %v\n%s", err, out)
		}
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestPinnedBrowserThroughSchedulerAndRealAgents$", "-test.v", "-test.timeout=3m")
		cmd.Dir = t.TempDir()
		cmd.Env = append(os.Environ(), child+"=1", "NEKOMARI_PINNED_BROWSER_ROOT="+root, "NEKOMARI_PINNED_TEST_BINARY="+binary)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated browser and Agent workflow: %v\n%s", err, out)
		}
		return
	}

	root := os.Getenv("NEKOMARI_PINNED_BROWSER_ROOT")
	db := dbcore.GetDBInstance()
	defer func() {
		if err := dbcore.Close(); err != nil {
			t.Errorf("close scratch DB: %v", err)
		}
	}()
	if err := config.Set(metricstore.MetricDBDSNKey, "file:pinned-browser-metrics?mode=memory&cache=shared"); err != nil {
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
	}()
	store := metricstore.GetStore()
	listener, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Fatalf("IPv6 loopback required for opted-in acceptance: %v", err)
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

	// Vite serves source modules only; requests to /api go to the scratch panel.
	portListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := portListener.Addr().(*net.TCPAddr).Port
	_ = portListener.Close()
	viteURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	vite := exec.Command("node", filepath.Join(root, "frontend/node_modules/vite/bin/vite.js"), "--host", "127.0.0.1", "--port", fmt.Sprint(port), "--strictPort")
	vite.Dir = filepath.Join(root, "frontend")
	vite.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var viteLog bytes.Buffer
	vite.Stdout, vite.Stderr = &viteLog, &viteLog
	if err := vite.Start(); err != nil {
		t.Fatal(err)
	}
	viteDone := make(chan error, 1)
	go func() { viteDone <- vite.Wait() }()
	defer func() {
		_ = syscall.Kill(-vite.Process.Pid, syscall.SIGTERM)
		select {
		case <-viteDone:
		case <-time.After(5 * time.Second):
			_ = syscall.Kill(-vite.Process.Pid, syscall.SIGKILL)
			<-viteDone
		}
		if t.Failed() {
			t.Logf("Vite output:\n%s", viteLog.String())
		}
	}()
	wait := func(what string, timeout time.Duration, ready func() bool) {
		t.Helper()
		deadline := time.Now().Add(timeout)
		for !ready() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s", what)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	localHTTP := &http.Client{Timeout: time.Second}
	wait("Vite", 20*time.Second, func() bool {
		resp, err := localHTTP.Get(viteURL + "/script/ping-family-e2e.browser.html")
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	})

	observer := &browserAgentObserver{}
	connection.SetFrameInterceptor(observer)
	defer connection.SetFrameInterceptor(nil)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(api.ControlBodyLimit(), api.IdentityMiddleware())
	engine.POST("/api/admin/ping/add", api.RequireRole(api.RoleAdmin), Bind("admin:addPingTask"))
	var sockets sync.WaitGroup
	engine.GET("/api/clients/v2/rpc", api.RequireRole(api.RoleClient), func(c *gin.Context) {
		sockets.Add(1)
		defer sockets.Done()
		clientAPI.WebSocketV2RPC(c)
	})
	upstream, err := url.Parse(viteURL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			engine.ServeHTTP(w, r)
		} else {
			proxy.ServeHTTP(w, r)
		}
	}))
	defer func() { server.Close(); sockets.Wait() }()
	account, err := accounts.CreateAccount("browser-e2e-admin", uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	session, err := accounts.CreateSession(account.UUID, 3600, "browser-e2e", "127.0.0.1", "password")
	if err != nil {
		t.Fatal(err)
	}
	nodes := []string{"browser-ipv6", "browser-nofamily", "browser-disabled"}
	tokens := make(map[string]string, len(nodes))
	for _, id := range nodes {
		tokens[id] = uuid.NewString()
		if err := db.Create(&models.Client{UUID: id, Token: tokens[id]}).Error; err != nil {
			t.Fatal(err)
		}
	}
	const target = "tj-ct-v6.ip.zstaticcdn.com:80"
	body := `{"name":"gated control","target":"` + target + `","type":"auto","interval":1,"clients":["browser-ipv6","browser-nofamily","browser-disabled"],"target_family":"ipv6"}`
	post := func(path string, authenticated bool) int {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		if authenticated {
			req.AddCookie(&http.Cookie{Name: "session_token", Value: session})
		}
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode
	}
	if status := post("/api/admin/ping/add", false); status != http.StatusUnauthorized {
		t.Fatalf("anonymous fixture request: status %d", status)
	}
	var count int64
	if err := db.Model(&models.PingTask{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("negative controls created tasks: count=%d err=%v", count, err)
	}

	type fixture struct {
		id, result, ack, ready string
		cmd                    *exec.Cmd
		output                 bytes.Buffer
		done                   chan error
	}
	fixtures := make(map[string]*fixture, len(nodes))
	for _, id := range nodes {
		dir := t.TempDir()
		f := &fixture{id: id, result: filepath.Join(dir, "result"), ack: filepath.Join(dir, "ack"), ready: filepath.Join(dir, "ready"), done: make(chan error, 1)}
		mode := "ipv6"
		if id == "browser-nofamily" {
			mode = "no-family"
		}
		f.cmd = exec.Command(os.Getenv("NEKOMARI_PINNED_TEST_BINARY"), "-test.run=^TestPinnedPanelIntegrationChild$", "-test.v", "-test.timeout=100s")
		f.cmd.Env = append(os.Environ(), "NEKOMARI_PINNED_TEST_WS=ws"+strings.TrimPrefix(server.URL, "http")+"/api/clients/v2/rpc",
			"NEKOMARI_PINNED_TEST_TOKEN="+tokens[id], "NEKOMARI_PINNED_TEST_MODE="+mode, "NEKOMARI_PINNED_TEST_TARGET="+listener.Addr().String(),
			"NEKOMARI_PINNED_TEST_HOSTNAME=tj-ct-v6.ip.zstaticcdn.com", "NEKOMARI_PINNED_TEST_RESULT="+f.result, "NEKOMARI_PINNED_TEST_ACK="+f.ack,
			"NEKOMARI_PINNED_TEST_READY="+f.ready)
		if id == "browser-disabled" {
			f.cmd.Env = append(f.cmd.Env, "NEKOMARI_PINNED_TEST_DISABLE_PROBE=1")
		}
		f.cmd.Stdout, f.cmd.Stderr = &f.output, &f.output
		if err := f.cmd.Start(); err != nil {
			t.Fatal(err)
		}
		fixtures[id] = f
		go func() { f.done <- f.cmd.Wait() }()
		defer func() {
			if socket := agentRuntime.GetConnectedClient(f.id); socket != nil {
				socket.Abort()
			}
			select {
			case err := <-f.done:
				if err != nil {
					t.Errorf("Agent %s: %v\n%s", f.id, err, f.output.String())
				}
			case <-time.After(5 * time.Second):
				_ = f.cmd.Process.Kill()
				<-f.done
				t.Errorf("Agent %s did not stop\n%s", f.id, f.output.String())
			}
			clientAPI.ResetClientState(f.id)
		}()
	}
	defer utils.ReloadPingSchedule(nil)
	for _, id := range nodes {
		f := fixtures[id]
		wait("Agent handshake for "+id, 10*time.Second, func() bool {
			_, err := os.Stat(f.ready)
			return err == nil && agentRuntime.GetConnectedClient(id) != nil
		})
	}
	python := os.Getenv("NEKOMARI_PINNED_BROWSER_PYTHON")
	if python == "" {
		python = "python3"
	}
	browser := exec.CommandContext(t.Context(), python, filepath.Join(root, "frontend/script/ping-family-e2e.browser.spec.py"))
	browser.Dir = filepath.Join(root, "frontend")
	browser.Env = append(os.Environ(), "NEKOMARI_PINNED_BROWSER_URL="+server.URL+"/script/ping-family-e2e.browser.html", "NEKOMARI_PINNED_BROWSER_SESSION="+session)
	if out, err := browser.CombinedOutput(); err != nil {
		t.Fatalf("browser interaction: %v\n%s", err, out)
	}
	var task models.PingTask
	if err := db.Where("name = ?", "Browser IPv6 acceptance").Take(&task).Error; err != nil {
		t.Fatal(err)
	}
	if task.Target != target || task.TargetFamily != "ipv6" || task.Type != "auto" || task.Interval != 1 || task.DefaultOn ||
		!reflect.DeepEqual([]string(task.Clients), nodes) || len(task.PinnedGeneration) != 32 {
		t.Fatalf("unexpected persisted browser task: %+v", task)
	}
	points := func(id string) []metric.Point {
		t.Helper()
		out, err := store.Query(context.Background(), metric.Query{MetricName: metricstore.MetricPingLoss, EntityID: id,
			Tags: map[string]string{"task_id": fmt.Sprint(task.Id)}, Start: time.Now().Add(-time.Minute), End: time.Now().Add(time.Minute)})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	wait("scheduled IPv6 measurement", 15*time.Second, func() bool { return len(points("browser-ipv6")) > 0 })
	wait("no-family result", 15*time.Second, func() bool { return observer.noFamily.Load() > 0 })
	wait("disabled Agent rejection", 15*time.Second, func() bool { return observer.disabledDeny.Load() > 0 })
	wait("no-family admission barrier", 10*time.Second, func() bool { _, err := os.Stat(fixtures["browser-nofamily"].ack); return err == nil })
	for _, point := range points("browser-ipv6") {
		if point.Value != 0 || point.Tags["family"] != "ipv6" || point.Tags["protocol"] != "tcp" {
			t.Fatalf("unexpected IPv6 measurement: %+v", point)
		}
	}
	for _, id := range []string{"browser-nofamily", "browser-disabled"} {
		if got := points(id); len(got) != 0 {
			t.Fatalf("%s emitted false loss: %+v", id, got)
		}
	}
	if got := observer.disabledProbe.Load(); got != 0 {
		t.Fatalf("disabled Agent received %d probe frames", got)
	}
	for _, id := range nodes {
		if events := agentRuntime.TakeV2Events(id, nil, 0); len(events) != 0 {
			t.Fatalf("pinned task entered legacy queue for %s: %+v", id, events)
		}
	}
}
