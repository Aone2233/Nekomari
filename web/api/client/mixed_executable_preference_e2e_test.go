//go:build linux

package client

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
)

// Actual legacy and candidate Agents, ordinary scheduler and authenticated WebSockets.
// The caller supplies trusted binaries built from the two source revisions.
func TestMixedExecutablePreference(t *testing.T) {
	if os.Getenv("NEKOMARI_MIXED_EXEC_E2E") != "1" {
		t.Skip("requires supplied Agent binaries and isolated namespaces")
	}
	const marker = "NEKOMARI_MIXED_CHILD"
	if os.Getenv(marker) != "1" {
		for _, key := range []string{"NEKOMARI_MIXED_OLD", "NEKOMARI_MIXED_NEW"} {
			if !strings.HasPrefix(os.Getenv(key), "/") {
				t.Fatalf("%s must be an absolute binary path", key)
			}
		}
		cmd := exec.CommandContext(t.Context(), "unshare", "--net", "--mount", "--", os.Args[0], "-test.run=^TestMixedExecutablePreference$", "-test.v", "-test.timeout=60s")
		cmd.Dir = t.TempDir()
		cmd.Env = append(os.Environ(), marker+"=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated fixture: %v\n%s", err, out)
		} else {
			t.Logf("%s", out)
		}
		return
	}
	if out, err := exec.Command("mount", "--make-rprivate", "/").CombinedOutput(); err != nil {
		t.Fatalf("private mounts: %v %s", err, out)
	}
	hosts := filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(hosts, []byte("127.0.0.1 localhost\n::1 localhost\n::1 mixed.fixture\n127.0.0.1 mixed.fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("mount", "--bind", hosts, "/etc/hosts").CombinedOutput(); err != nil {
		t.Fatalf("isolated hosts: %v %s", err, out)
	}
	if out, err := exec.Command("ip", "link", "set", "lo", "up").CombinedOutput(); err != nil {
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

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(api.IdentityMiddleware())
	group := router.Group("/api/clients", api.RequireRole(api.RoleClient))
	group.POST("/v2/rpc", UploadV2RPC)
	var sockets sync.WaitGroup
	group.GET("/v2/rpc", func(c *gin.Context) { sockets.Add(1); defer sockets.Done(); WebSocketV2RPC(c) })
	panel := httptest.NewServer(router)
	defer func() { panel.Close(); sockets.Wait() }()
	var processes []*exec.Cmd
	var outputs []*bytes.Buffer
	var dones []chan error
	defer func() {
		for i, cmd := range processes {
			_ = cmd.Process.Signal(os.Interrupt)
			select {
			case <-dones[i]:
			case <-time.After(5 * time.Second):
				_ = cmd.Process.Kill()
				<-dones[i]
			}
			if t.Failed() {
				t.Logf("Agent %d output: %s", i, outputs[i].String())
			}
		}
		ResetClientState("old")
		ResetClientState("new")
	}()
	for _, info := range []struct{ id, env string }{{"old", "NEKOMARI_MIXED_OLD"}, {"new", "NEKOMARI_MIXED_NEW"}} {
		var random [24]byte
		if _, err := rand.Read(random[:]); err != nil {
			t.Fatal(err)
		}
		token := hex.EncodeToString(random[:])
		if err := db.Create(&models.Client{UUID: info.id, Token: token, Name: info.id}).Error; err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(os.Getenv(info.env), "--endpoint", panel.URL, "--disable-auto-update", "--disable-web-ssh", "--interval", "1")
		cmd.Dir = t.TempDir()
		cmd.Env = append(os.Environ(), "AGENT_TOKEN="+token, "GODEBUG=netdns=go", "HTTP_PROXY=", "HTTPS_PROXY=", "ALL_PROXY=", "NO_PROXY=*")
		output := &bytes.Buffer{}
		cmd.Stdout = output
		cmd.Stderr = output
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		processes = append(processes, cmd)
		outputs = append(outputs, output)
		dones = append(dones, done)
	}
	ln4, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln4.Close()
	port := ln4.Addr().(*net.TCPAddr).Port
	ln6, err := net.Listen("tcp6", fmt.Sprintf("[::1]:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	defer ln6.Close()
	for _, ln := range []net.Listener{ln4, ln6} {
		go func(l net.Listener) {
			for {
				c, e := l.Accept()
				if e != nil {
					return
				}
				c.Close()
			}
		}(ln)
	}

	task := models.PingTask{Name: "mixed fixture", Type: "tcp", Target: fmt.Sprintf("mixed.fixture:%d", port), IPVersionPreference: "4", Interval: 2, Clients: models.StringArray{"old", "new"}}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for !agentRuntime.IsAgentOnline("old") || !agentRuntime.IsAgentOnline("new") {
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
	query := func(id string) []metric.Point {
		p, err := store.Query(t.Context(), metric.Query{MetricName: metricstore.MetricPingSuccessLatency, EntityID: id, Tags: map[string]string{"task_id": fmt.Sprint(task.Id)}, Start: time.Now().Add(-time.Minute), End: time.Now().Add(time.Minute)})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	for len(query("old")) == 0 || len(query("new")) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("missing results old=%+v new=%+v", query("old"), query("new"))
		}
		time.Sleep(100 * time.Millisecond)
	}
	for _, test := range []struct{ id, family string }{{"old", "ipv6"}, {"new", "ipv4"}} {
		p := query(test.id)
		for _, v := range p {
			if v.Tags["family"] != test.family {
				t.Fatalf("%s unexpected family: %+v", test.id, p)
			}
		}
		t.Logf("%s family=%s", test.id, test.family)
	}
}
