package pingrollback

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/pkg/metric"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestOldPanelImage is opt-in: use an already installed immutable image ID and
// a local Docker daemon. It runs only against a newly created fixture database.
func TestOldPanelImage(t *testing.T) {
	image := os.Getenv("NEKOMARI_ROLLBACK_TEST_IMAGE")
	if image == "" {
		t.Skip("set NEKOMARI_ROLLBACK_TEST_IMAGE to a local old-panel image ID")
	}
	if !strings.HasPrefix(image, "sha256:") || len(image) != len("sha256:")+64 {
		t.Fatal("image must be an immutable sha256 image ID")
	}
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	if err := os.Mkdir(data, 0700); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(data, "komari.db")
	open := func() *gorm.DB {
		t.Helper()
		db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?_foreign_keys=1&_busy_timeout=5000", dbPath)), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		if err != nil {
			t.Fatal(err)
		}
		sqlDB, err := db.DB()
		if err != nil {
			t.Fatal(err)
		}
		sqlDB.SetMaxOpenConns(1)
		return db
	}
	closeDB := func(db *gorm.DB) {
		t.Helper()
		sqlDB, err := db.DB()
		if err != nil {
			t.Fatal(err)
		}
		if err := sqlDB.Close(); err != nil {
			t.Fatal(err)
		}
	}
	db := open()
	if err := db.AutoMigrate(&models.Client{}, &models.PingTask{}, &models.PingRecord{}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"node-a", "node-b"} {
		if err := db.Create(&models.Client{UUID: id, Token: "token-" + id}).Error; err != nil {
			t.Fatal(err)
		}
	}
	pinned := models.PingTask{Name: "pinned", Type: "tcp", Target: "example.test:80", Interval: 60,
		DefaultOn: true, Clients: models.StringArray{}, TargetFamily: "ipv6", PinnedGeneration: "0123456789abcdef0123456789abcdef"}
	legacy := models.PingTask{Name: "legacy", Type: "tcp", Target: "192.0.2.1:80", Interval: 60, Clients: models.StringArray{"node-a"}}
	if err := db.Create(&pinned).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.PingRecord{Client: "node-a", TaskId: pinned.Id, Value: 42, Time: time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	if n, err := Quarantine(db); err != nil || n != 1 {
		t.Fatalf("quarantine: %d, %v", n, err)
	}
	var beforeRoster []struct{ UUID, Created string }
	if err := db.Raw(`SELECT uuid, quote(created_at) AS created FROM clients ORDER BY uuid`).Scan(&beforeRoster).Error; err != nil {
		t.Fatal(err)
	}
	closeDB(db)

	// The old panel receives a distinct scratch store; parked history is
	// copied to an archive it cannot access. No real database is involved.
	policy := metric.RollupPolicy{Tiers: []metric.RollupTier{{Interval: time.Minute, Retention: 24 * time.Hour}}}
	openMetrics := func(path string) *metric.Store {
		t.Helper()
		s, err := metric.Open(context.Background(), metric.SQLite(path, metric.WithRollupPolicy(policy)))
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	source := openMetrics(filepath.Join(dir, "source.db"))
	oldMetricsPath := filepath.Join(data, "metrics.db")
	oldMetrics := openMetrics(oldMetricsPath)
	archivePath := filepath.Join(dir, "archive.db")
	archive := openMetrics(archivePath)
	if err := source.UpsertMetric(t.Context(), metric.Definition{Name: "ping.loss", Type: metric.TypeGauge, Unit: "ratio", RetentionDays: 30}); err != nil {
		t.Fatal(err)
	}
	bucket := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Minute)
	var original []metric.PersistedRollup
	for _, id := range []uint{pinned.Id, legacy.Id} {
		original = append(original, metric.PersistedRollup{MetricName: "ping.loss", EntityID: "node-a", Tags: map[string]string{"task_id": strconv.FormatUint(uint64(id), 10), "family": "ipv6"}, Resolution: time.Minute, Bucket: bucket, Count: 1, Sum: 0, SumSq: 0, Min: 0, Max: 0, FirstValue: 0, LastValue: 0, FirstTime: bucket, LastTime: bucket, CreatedAt: bucket})
	}
	if err := source.ImportRollups(t.Context(), original); err != nil {
		t.Fatal(err)
	}
	if err := splitMetricHistory(t.Context(), source, oldMetrics, archive, []uint{pinned.Id}); err != nil {
		t.Fatal(err)
	}
	// Seed a persisted late-write equivalent in the old store before boot so
	// its real delete operation has something tangible to erase.
	if err := oldMetrics.ImportRollups(t.Context(), original[:1]); err != nil {
		t.Fatal(err)
	}
	readRollups := func(s *metric.Store) []metric.PersistedRollup {
		t.Helper()
		var rows []metric.PersistedRollup
		if _, err := s.ExportRollups(context.Background(), "ping.loss", 100, func(batch []metric.PersistedRollup) error {
			rows = append(rows, batch...)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return rows
	}
	archivedBefore := readRollups(archive)
	if len(archivedBefore) != 1 || archivedBefore[0].Tags["task_id"] != strconv.FormatUint(uint64(pinned.Id), 10) {
		t.Fatalf("missing isolated history: %+v", archivedBefore)
	}
	for _, s := range []*metric.Store{source, oldMetrics, archive} {
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	name := fmt.Sprintf("nekomari-family-old-%d", time.Now().UnixNano())
	run := func(args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	}
	args := []string{"run", "--detach", "--pull=never", "--name", name, "--network=none", "--read-only", "--cap-drop=ALL",
		"--security-opt=no-new-privileges", "--pids-limit=128", "--memory=512m", "--cpus=1", "--tmpfs", "/tmp:rw,noexec,nosuid,size=64m",
		"--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), "--workdir=/app", "--entrypoint=/app/nekomari",
		"--mount", "type=bind,src=" + data + ",dst=/app/data", "--env", "KOMARI_LISTEN=127.0.0.1:25774", image,
		"server", "--listen", "127.0.0.1:25774"}
	out, err := run(args...)
	if err != nil {
		t.Fatalf("start isolated old panel: %v: %s", err, out)
	}
	defer func() {
		cleanup, err := exec.Command("docker", "rm", "--force", name).CombinedOutput()
		if err != nil {
			t.Errorf("remove scratch container: %v: %s", err, cleanup)
		}
	}()
	ready := false
	for !ready && ctx.Err() == nil {
		out, err = run("exec", name, "curl", "--fail", "--silent", "--max-time", "2", "--noproxy", "*", "http://127.0.0.1:25774/api/install/status")
		ready = err == nil && bytes.Contains(out, []byte(`"status":"success"`))
		if !ready {
			time.Sleep(200 * time.Millisecond)
		}
	}
	if !ready {
		logs, _ := run("logs", "--tail", "40", name)
		t.Fatalf("old panel did not reach install guide: %s", logs)
	}
	installation := `{"username":"fixture-admin","password":"Fixture-Only-Password-123","sitename":"Isolated fixture","metric_dsn":"./data/metrics.db"}`
	out, err = run("exec", name, "curl", "--fail", "--silent", "--max-time", "10", "--noproxy", "*", "-H", "Content-Type: application/json", "-d", installation, "http://127.0.0.1:25774/api/install/complete")
	if err != nil || !bytes.Contains(out, []byte(`"status":"success"`)) {
		t.Fatalf("complete scratch installation: %s, %v", out, err)
	}
	// This legacy-table fixture will correctly enter the legacy-monitoring
	// guide. Mark only the scratch fixture complete so the normal scheduler
	// also starts; this does not migrate or remove its retained history.
	db = open()
	if err := db.Exec(`INSERT OR REPLACE INTO configs (key, value) VALUES (?, ?)`, "migration_legacy_monitoring_to_metric_store_done", "true").Error; err != nil {
		t.Fatal(err)
	}
	closeDB(db)
	ready = false
	for !ready && ctx.Err() == nil {
		out, err = run("exec", name, "curl", "--fail", "--silent", "--max-time", "2", "--noproxy", "*", "http://127.0.0.1:25774/api/version")
		ready = err == nil && bytes.Contains(out, []byte(`"version":"v1.7.29"`))
		if !ready {
			time.Sleep(200 * time.Millisecond)
		}
	}
	if !ready {
		logs, _ := run("logs", "--tail", "40", name)
		t.Fatalf("old panel did not reach normal service: %s", logs)
	}
	countParkedPersisted := func() int64 {
		t.Helper()
		db := open()
		defer closeDB(db)
		if err := db.Exec(`ATTACH DATABASE ? AS scratch_metrics`, oldMetricsPath).Error; err != nil {
			t.Fatal(err)
		}
		var count int64
		if err := db.Raw(`SELECT count(*) FROM scratch_metrics.metric_rollups r JOIN scratch_metrics.metric_series s ON s.id = r.series_id WHERE s.tags LIKE ?`, fmt.Sprintf(`%%"task_id":"%d"%%`, pinned.Id)).Scan(&count).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec(`DETACH DATABASE scratch_metrics`).Error; err != nil {
			t.Fatal(err)
		}
		return count
	}
	if count := countParkedPersisted(); count == 0 {
		t.Fatal("scratch old store has no persisted parked history to test deletion")
	}
	// The old intake accepts delayed legacy results by task ID even though
	// scheduling is parked. Assert this known rollback blocker explicitly.
	post := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"agent.pingResult","params":{"task_id":%d,"ping_type":"tcp","family":"ipv4","value":123}}`, pinned.Id)
	out, err = run("exec", name, "curl", "--fail", "--silent", "--max-time", "5", "--noproxy", "*", "-H", "Authorization: Bearer token-node-a", "-H", "Content-Type: application/json", "-d", post, "http://127.0.0.1:25774/api/clients/v2/rpc")
	var reply struct {
		Error  json.RawMessage `json:"error"`
		Result json.RawMessage `json:"result"`
	}
	if err != nil || json.Unmarshal(out, &reply) != nil || len(reply.Result) == 0 || (len(reply.Error) != 0 && string(reply.Error) != "null") {
		t.Fatalf("old result intake behavior changed: %s, %v", out, err)
	}
	// An old admin deletion first removes persisted metrics, even though the
	// main-DB trigger blocks deletion. A queued result may be flushed later.
	login := `{"username":"fixture-admin","password":"Fixture-Only-Password-123"}`
	out, err = run("exec", name, "curl", "--fail", "--silent", "--max-time", "5", "--noproxy", "*", "-H", "Content-Type: application/json", "-d", login, "http://127.0.0.1:25774/api/login")
	var loginResult struct {
		Status string `json:"status"`
		Data   struct {
			Cookie struct {
				Session string `json:"session_token"`
			} `json:"set-cookie"`
		} `json:"data"`
	}
	if err != nil || json.Unmarshal(out, &loginResult) != nil || loginResult.Status != "success" || loginResult.Data.Cookie.Session == "" {
		t.Fatalf("scratch admin login failed: %v", err)
	}
	deletion := fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"admin:deletePingTask","params":{"id":[%d]}}`, pinned.Id)
	out, err = run("exec", name, "curl", "--fail", "--silent", "--max-time", "10", "--noproxy", "*", "-H", "Cookie: session_token="+loginResult.Data.Cookie.Session, "-H", "Content-Type: application/json", "-d", deletion, "http://127.0.0.1:25774/api/rpc2")
	var deleteReply struct {
		Error *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err != nil || json.Unmarshal(out, &deleteReply) != nil || deleteReply.Error == nil || deleteReply.Error.Code != -32603 {
		t.Fatalf("old admin delete did not report the trigger refusal: %s, %v", out, err)
	}
	// Check the separate metrics DB while the old process is running: its
	// accepted result can still be queued or hot and is not proof of deletion.
	if count := countParkedPersisted(); count != 0 {
		t.Fatalf("old delete left %d parked persisted rollups", count)
	}
	// The sealed parent remains. The archive is outside the container mount.
	db = open()
	var parkedCount int64
	if err := db.Model(&models.PingTask{}).Where("id = ?", pinned.Id).Count(&parkedCount).Error; err != nil || parkedCount != 1 {
		t.Fatalf("old delete changed parked parent: count=%d err=%v", parkedCount, err)
	}
	closeDB(db)
	if _, err := run("stop", "--time", "10", name); err != nil {
		t.Fatal(err)
	}
	archive = openMetrics(archivePath)
	if archivedAfter := readRollups(archive); !reflect.DeepEqual(archivedAfter, archivedBefore) {
		t.Fatalf("old binary changed isolated metrics: before=%+v after=%+v", archivedBefore, archivedAfter)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	oldMetrics = openMetrics(oldMetricsPath)
	oldRows := readRollups(oldMetrics)
	var sawPinned, sawLegacy bool
	for _, row := range oldRows {
		switch row.Tags["task_id"] {
		case strconv.FormatUint(uint64(pinned.Id), 10):
			sawPinned = true
		case strconv.FormatUint(uint64(legacy.Id), 10):
			sawLegacy = true
		}
	}
	if !sawPinned || !sawLegacy {
		t.Fatalf("old queued result did not survive deletion or unrelated history disappeared: %+v", oldRows)
	}
	archive = openMetrics(archivePath)
	merged := openMetrics(filepath.Join(dir, "merged.db"))
	if err := mergeMetricHistory(t.Context(), oldMetrics, archive, merged, []uint{pinned.Id}); err != nil {
		t.Fatal(err)
	}
	mergedRows := readRollups(merged)
	var mergedPinned []metric.PersistedRollup
	sawLegacy = false
	for _, row := range mergedRows {
		switch row.Tags["task_id"] {
		case strconv.FormatUint(uint64(pinned.Id), 10):
			mergedPinned = append(mergedPinned, row)
		case strconv.FormatUint(uint64(legacy.Id), 10):
			sawLegacy = true
		}
	}
	if !sawLegacy || !reflect.DeepEqual(mergedPinned, archivedBefore) {
		t.Fatalf("scratch merge lost history or retained old result: %+v", mergedRows)
	}
	for _, s := range []*metric.Store{oldMetrics, archive, merged} {
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
	db = open()
	defer closeDB(db)
	var parked models.PingTask
	if err := db.Take(&parked, pinned.Id).Error; err != nil {
		t.Fatal(err)
	}
	if parked.Interval != 0 || parked.DefaultOn || len(parked.Clients) != 1 || parked.Clients[0] != parkedClient {
		t.Fatalf("old startup changed parked task: %+v", parked)
	}
	var records int64
	if err := db.Model(&models.PingRecord{}).Where("task_id = ?", pinned.Id).Count(&records).Error; err != nil || records != 1 {
		t.Fatalf("old startup changed history: %d %v", records, err)
	}
	if err := checkSchema(db); err != nil {
		var columns []struct{ Name string }
		_ = db.Raw(`PRAGMA table_info('ping_tasks')`).Scan(&columns).Error
		t.Fatalf("old image changed task schema: %+v: %v", columns, err)
	}
	for _, sql := range triggerSQL {
		var found string
		if err := db.Raw(`SELECT coalesce((SELECT sql FROM sqlite_master WHERE type = 'trigger' AND name = ?), '')`, triggerName(sql)).Scan(&found).Error; err != nil || found != sql {
			t.Fatalf("old image changed trigger %s: %q, %v", triggerName(sql), found, err)
		}
	}
	var saved snapshot
	if err := db.Take(&saved, 1).Error; err != nil {
		t.Fatal(err)
	}
	roster, err := clientRosterDigest(db)
	if err != nil || roster != saved.RosterDigest {
		var afterRoster []struct{ UUID, Created string }
		_ = db.Raw(`SELECT uuid, quote(created_at) AS created FROM clients ORDER BY uuid`).Scan(&afterRoster).Error
		t.Fatalf("old image changed node roster: %+v -> %+v: %s vs %s, %v", beforeRoster, afterRoster, roster, saved.RosterDigest, err)
	}
	if n, err := Restore(db); err != nil || n != 1 {
		t.Fatalf("restore after old boot: %d, %v", n, err)
	}
	var restored models.PingTask
	if err := db.Take(&restored, pinned.Id).Error; err != nil {
		t.Fatal(err)
	}
	if restored.Interval != pinned.Interval || !restored.DefaultOn || len(restored.Clients) != 0 || restored.TargetFamily != pinned.TargetFamily || restored.PinnedGeneration == pinned.PinnedGeneration {
		pretty, _ := json.Marshal(restored)
		t.Fatalf("bad restore: %s", pretty)
	}
	var other models.PingTask
	if err := db.Take(&other, legacy.Id).Error; err != nil || other.Name != legacy.Name || other.Interval != legacy.Interval {
		t.Fatalf("legacy changed: %+v %v", other, err)
	}
}
