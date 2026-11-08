package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/internal/metricstore"
	"github.com/Aone2233/nekomari/internal/scheduler"
	"github.com/Aone2233/nekomari/pkg/metric"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
	"github.com/Aone2233/nekomari/utils/notifier"
	"github.com/Aone2233/nekomari/web/api"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// Run in a child process because the production scheduler and quiescence fence
// are intentionally terminal for the process, not resettable test fixtures.
func TestShutdownDrainsScheduledAndQueuedMetricsToDisk(t *testing.T) {
	const childDir = "NEKOMARI_SHUTDOWN_TEST_DIR"
	dir := os.Getenv(childDir)
	if dir == "" {
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestShutdownDrainsScheduledAndQueuedMetricsToDisk$")
		cmd.Env = append(os.Environ(), childDir+"="+t.TempDir())
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("shutdown subprocess: %v\n%s", err, out)
		}
		return
	}

	ctx := t.Context()
	db, err := gorm.Open(sqlite.Open(filepath.Join(dir, "main.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	config.SetDb(db)
	if err := db.AutoMigrate(&models.PingTask{}, &models.PingTaskDeletion{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.PingTask{Id: 42, Name: "queued task", Type: "tcp", Target: "synthetic.test:80", Interval: 60}).Error; err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "metrics.db")
	if err := config.SetMany(map[string]any{metricstore.MetricDBDriverKey: "sqlite", metricstore.MetricDBDSNKey: path}); err != nil {
		t.Fatal(err)
	}
	if err := metricstore.InitializeStore(); err != nil {
		t.Fatal(err)
	}
	if err := metricstore.StartReportBatcher(); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	if err := metricstore.WritePingRecord(ctx, models.PingRecord{TaskId: 42, Client: "queued", Time: at, Value: 7}); err != nil {
		t.Fatal(err)
	}
	if _, err := metricstore.WriteReport(ctx, v2.Report{UUID: "queued", UpdatedAt: at, CPU: v2.CPUReport{Usage: 42}}); err != nil {
		t.Fatal(err)
	}
	started, scheduledResult := make(chan struct{}), make(chan error, 1)
	if err := scheduler.AddContextFunc("test:shutdown", "@every 1h", true, func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		scheduledResult <- metricstore.WritePingRecord(context.Background(), models.PingRecord{TaskId: 43, Client: "scheduled", Time: at, Value: 9})
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("scheduled callback did not start")
	}
	websocketStarted, websocketResult := make(chan struct{}), make(chan error, 1)
	router := gin.New()
	router.Use(api.TrackWebSocketHandlers())
	router.GET("/socket", func(c *gin.Context) {
		conn, err := api.UpgradeSafeConn(c)
		if err != nil {
			websocketResult <- err
			return
		}
		defer conn.Close()
		close(websocketStarted)
		_, _, _ = conn.ReadMessage()
		websocketResult <- metricstore.WritePingRecord(context.Background(), models.PingRecord{TaskId: 44, Client: "websocket", Time: at, Value: 11})
	})
	httpServer := httptest.NewServer(router)
	defer httpServer.Close()
	websocketConn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(httpServer.URL, "http")+"/socket", http.Header{"Origin": {httpServer.URL}})
	if err != nil {
		t.Fatal(err)
	}
	defer websocketConn.Close()
	<-websocketStarted
	notifierStarted, notifierResult := make(chan struct{}), make(chan error, 1)
	if !notifier.StartBackground(func(ctx context.Context) {
		close(notifierStarted)
		<-ctx.Done()
		notifierResult <- db.Exec(`UPDATE ping_tasks SET name = ? WHERE id = ?`, "notifier drained", 42).Error
	}) {
		t.Fatal("notifier fixture not admitted")
	}
	<-notifierStarted
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	app := New(Options{})
	app.addCleanup("database", func(context.Context) error { return sqlDB.Close() })
	app.addCriticalCleanup("metric-store", metricstore.CloseStoreContext)
	app.addCriticalCleanup("metric-report-batcher", metricstore.QuiesceReportBatcher)
	app.addCriticalCleanup("metric-async-cleanup", metricstore.QuiesceAsyncCleanup)
	app.addCriticalCleanup("metric-store-migration", metricstore.StopStoreMigrationForShutdown)
	app.addCriticalCleanup("scheduler", scheduler.StopAndWait)
	if err := app.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if err := <-scheduledResult; err != nil {
		t.Fatalf("scheduled write before quiescence: %v", err)
	}
	if err := <-websocketResult; err != nil {
		t.Fatalf("websocket write before quiescence: %v", err)
	}
	if err := <-notifierResult; err != nil {
		t.Fatalf("notifier write before database close: %v", err)
	}

	reopened, err := metric.Open(ctx, metric.SQLite(path))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	seen := make(map[string]int)
	for _, name := range []string{metricstore.MetricPingLoss, metricstore.MetricCPU} {
		if _, err := reopened.ExportRollups(ctx, name, 10, func(rows []metric.PersistedRollup) error {
			for _, row := range rows {
				seen[name+":"+row.EntityID] += int(row.Count)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range []string{metricstore.MetricPingLoss + ":queued", metricstore.MetricPingLoss + ":scheduled", metricstore.MetricPingLoss + ":websocket", metricstore.MetricCPU + ":queued"} {
		if seen[key] != 1 {
			t.Fatalf("shutdown did not persist %s exactly once: %v", key, seen)
		}
	}

	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	mainDB, err := gorm.Open(sqlite.Open(filepath.Join(dir, "main.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	var task models.PingTask
	if err := mainDB.First(&task, 42).Error; err != nil || task.Name != "notifier drained" {
		t.Fatal(fmt.Errorf("notifier main-database write missing after close: task=%+v err=%w", task, err))
	}
	mainSQL, err := mainDB.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := mainSQL.Close(); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("NEKOMARI_SHUTDOWN_ENCRYPTED_E2E") == "1" {
		if err := exerciseSyntheticEncryptedHandoff(ctx, dir); err != nil {
			t.Fatal(err)
		}
	}
}

// This copies only the already-stopped synthetic stores. It exercises the
// real encrypted format, not the unimplemented production cutover protocol.
func exerciseSyntheticEncryptedHandoff(ctx context.Context, dir string) error {
	if _, err := exec.LookPath("age"); err != nil {
		return errors.New("synthetic handoff requires age and age-keygen")
	}
	keygen, err := exec.LookPath("age-keygen")
	if err != nil {
		return err
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		return err
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		return err
	}
	data, work, archives := filepath.Join(dir, "data"), filepath.Join(dir, "work"), filepath.Join(dir, "archives")
	for _, path := range []string{data, work, archives} {
		if err := os.Mkdir(path, 0700); err != nil {
			return err
		}
	}
	for _, pair := range [][2]string{{"main.db", "komari.db"}, {"metrics.db", "metrics.db"}} {
		bytes, err := os.ReadFile(filepath.Join(dir, pair[0]))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(data, pair[1]), bytes, 0600); err != nil {
			return err
		}
	}
	// The archive validates the main database's metric path against its own
	// snapshot; change only the detached scratch copy, never the source.
	scratch, err := gorm.Open(sqlite.Open(filepath.Join(data, "komari.db")), &gorm.Config{})
	if err != nil {
		return err
	}
	if err := scratch.Exec("UPDATE configs SET value = ? WHERE key = ?", `"./data/metrics.db"`, metricstore.MetricDBDSNKey).Error; err != nil {
		return err
	}
	sqlDB, err := scratch.DB()
	if err != nil {
		return err
	}
	if err := sqlDB.Close(); err != nil {
		return err
	}
	identity, recipients := filepath.Join(dir, "identity"), filepath.Join(dir, "recipients")
	if out, err := exec.CommandContext(ctx, keygen, "-o", identity).CombinedOutput(); err != nil {
		return errors.New("generate synthetic age identity: " + string(out))
	}
	recipient, err := exec.CommandContext(ctx, keygen, "-y", identity).Output()
	if err != nil {
		return err
	}
	if err := os.WriteFile(recipients, recipient, 0600); err != nil {
		return err
	}
	script, archive := filepath.Join(root, "deploy", "encrypted_backup.py"), filepath.Join(archives, "handoff.tar.gz.age")
	run := func(args ...string) error {
		out, err := exec.CommandContext(ctx, python, append([]string{script}, args...)...).CombinedOutput()
		if err != nil {
			return errors.New("synthetic encrypted handoff " + args[0] + " failed: " + strings.TrimSpace(string(out)))
		}
		return nil
	}
	if err := run("create", "--data-dir", data, "--panel-dir", dir, "--recipients-file", recipients, "--work-dir", work, "--output", archive); err != nil {
		return err
	}
	if err := run("verify", "--archive", archive, "--identity-file", identity, "--work-dir", work); err != nil {
		return err
	}
	restored := filepath.Join(archives, "restored")
	if err := run("extract", "--archive", archive, "--identity-file", identity, "--work-dir", work, "--destination", restored); err != nil {
		return err
	}
	manifestBytes, err := os.ReadFile(filepath.Join(restored, "manifest.json"))
	if err != nil {
		return err
	}
	var manifest struct {
		Files []struct{ Name string } `json:"files"`
	}
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return err
	}
	if len(manifest.Files) != 2 {
		return errors.New("synthetic handoff did not restore both databases")
	}
	restoredMain, err := gorm.Open(sqlite.Open(filepath.Join(restored, "komari.db")), &gorm.Config{})
	if err != nil {
		return err
	}
	var task models.PingTask
	if err := restoredMain.First(&task, 42).Error; err != nil {
		return err
	}
	if task.Name != "notifier drained" || task.Target != "synthetic.test:80" {
		return errors.New("encrypted restore changed the synthetic ping task")
	}
	restoredSQL, err := restoredMain.DB()
	if err != nil {
		return err
	}
	if err := restoredSQL.Close(); err != nil {
		return err
	}
	reopened, err := metric.Open(ctx, metric.SQLite(filepath.Join(restored, "metrics.db")))
	if err != nil {
		return err
	}
	defer reopened.Close()
	seen := make(map[string]int)
	for _, name := range []string{metricstore.MetricPingLoss, metricstore.MetricCPU} {
		if _, err := reopened.ExportRollups(ctx, name, 10, func(rows []metric.PersistedRollup) error {
			for _, row := range rows {
				seen[name+":"+row.EntityID] += int(row.Count)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	for _, key := range []string{metricstore.MetricPingLoss + ":queued", metricstore.MetricPingLoss + ":scheduled", metricstore.MetricPingLoss + ":websocket", metricstore.MetricCPU + ":queued"} {
		if seen[key] != 1 {
			return errors.New("encrypted restore lost or duplicated synthetic accepted history: " + key)
		}
	}
	return nil
}
