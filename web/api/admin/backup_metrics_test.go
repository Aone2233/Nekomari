package admin

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/Aone2233/nekomari/pkg/metric"
)

// TestCopyWhitelistedFilesDoesNotRawCopyMetricsDB 钉住白名单不再按普通文件复制
// metrics.db。
//
// 背景：metrics.db 跑在 WAL 模式下，-wal/-shm 不在白名单里（恢复时 dbcore 还会
// 主动删除它们），所以白名单里的普通复制得到的是一个「缺了最近 rollup」的归档，
// 而归档本身看起来完整。它必须像 komari.db 一样走 VACUUM INTO 一致性快照。
func TestCopyWhitelistedFilesDoesNotRawCopyMetricsDB(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(filepath.Join("data"), 0o755); err != nil {
		t.Fatalf("create data directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join("data", "metrics.db"), []byte("live sqlite bytes"), 0o644); err != nil {
		t.Fatalf("write metrics.db: %v", err)
	}

	contentDir := t.TempDir()
	if err := copyWhitelistedFilesFrom(filepath.Join(".", "data"), contentDir); err != nil {
		t.Fatalf("copyWhitelistedFilesFrom: %v", err)
	}

	if _, err := os.Stat(filepath.Join(contentDir, "metrics.db")); !os.IsNotExist(err) {
		t.Fatalf("metrics.db was archived as a plain file copy (stat err = %v); it must be snapshotted instead", err)
	}
}

// TestBackupMetricStoreSnapshotIncludesWALFrames 钉住归档里的 metrics.db 是一致性
// 快照：已提交但还留在 WAL 里的数据必须出现在快照中，而普通复制会丢掉它。
func TestBackupMetricStoreSnapshotIncludesWALFrames(t *testing.T) {
	t.Chdir(t.TempDir())
	ctx := context.Background()

	if err := os.MkdirAll(filepath.Join("data"), 0o755); err != nil {
		t.Fatalf("create data directory: %v", err)
	}
	sourcePath := filepath.Join("data", "metrics.db")
	store, err := metric.Open(ctx, metric.SQLite(sourcePath))
	if err != nil {
		t.Fatalf("open metric store: %v", err)
	}
	defer store.Close()

	if _, err := store.ExecContext(ctx, "CREATE TABLE backup_marker (value TEXT NOT NULL)"); err != nil {
		t.Fatalf("create marker table: %v", err)
	}
	if _, err := store.ExecContext(ctx, "INSERT INTO backup_marker (value) VALUES ('wal-only')"); err != nil {
		t.Fatalf("insert marker row: %v", err)
	}

	// 前提：这一行只存在于 WAL 中 —— 主文件里没有它，普通复制必然丢掉。
	walPath := sourcePath + "-wal"
	if info, err := os.Stat(walPath); err != nil || info.Size() == 0 {
		t.Fatalf("expected a non-empty WAL at %s (stat err = %v)", walPath, err)
	}
	plainCopy := filepath.Join(t.TempDir(), "plain.db")
	if err := copyFile(sourcePath, plainCopy); err != nil {
		t.Fatalf("plain copy of metrics.db: %v", err)
	}
	if rows, err := markerRows(plainCopy); err == nil && rows > 0 {
		t.Fatalf("premise broken: the raw main-file copy already contains %d marker rows", rows)
	}

	snapshot := filepath.Join(t.TempDir(), "content", "metrics.db")
	if err := backupMetricStoreSnapshot(ctx, store, snapshot); err != nil {
		t.Fatalf("backupMetricStoreSnapshot: %v", err)
	}
	rows, err := markerRows(snapshot)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if rows != 1 {
		t.Fatalf("snapshot marker rows = %d, want 1 (committed WAL frames were lost)", rows)
	}
}

func TestBackupMetricStoreSnapshotIncludesUnflushedMinute(t *testing.T) {
	ctx := context.Background()
	source := filepath.Join(t.TempDir(), "metrics.db")
	store, err := metric.Open(ctx, metric.SQLite(source, metric.WithMaxOpenConns(1)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.CreateMetric(ctx, metric.Definition{Name: "backup-hot", RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	point := metric.Point{MetricName: "backup-hot", EntityID: "node", Timestamp: time.Now().UTC().Add(30 * time.Second), Value: 42}
	if err := store.Write(ctx, point); err != nil {
		t.Fatal(err)
	}

	// This bucket has not yet been flushed to SQLite.
	var persisted int
	rows, err := store.QueryContext(ctx, "SELECT COUNT(*) FROM metric_rollups")
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() || rows.Scan(&persisted) != nil {
		_ = rows.Close()
		t.Fatal("failed to count source rollups")
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if persisted != 0 {
		t.Fatalf("premise broken: %d persisted rollups", persisted)
	}

	snapshot := filepath.Join(t.TempDir(), "metrics.db")
	if err := backupMetricStoreSnapshot(ctx, store, snapshot); err != nil {
		t.Fatalf("backup metric store: %v", err)
	}
	db, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(snapshot)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	var sum sql.NullFloat64
	if err := db.QueryRow(`SELECT COALESCE(SUM(r.count), 0), SUM(r.sum) FROM metric_rollups r
		JOIN metric_series s ON s.id = r.series_id
		JOIN metric_resolutions d ON d.id = r.resolution_id
		WHERE s.metric_name = 'backup-hot' AND d.resolution_milli = 60000`).Scan(&count, &sum); err != nil {
		t.Fatal(err)
	}
	if count != 1 || !sum.Valid || sum.Float64 != 42 {
		t.Fatalf("snapshot minute count=%d sum=%v, want 1 and 42", count, sum)
	}
}

func TestBackupMetricStoreSnapshotRejectsMissingActiveStore(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.MkdirAll("data", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("data", "metrics.db"), []byte("stale database"), 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "metrics.db")
	if err := backupMetricStoreSnapshot(t.Context(), nil, destination); err == nil {
		t.Fatal("missing active store was reported as a successful backup")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("stale default database was copied: %v", err)
	}
}

// markerRows 以只读方式打开一个 SQLite 文件并返回 backup_marker 的行数。
// 表不存在时返回错误，调用方据此区分「表在 WAL 里没被复制过来」。
func markerRows(path string) (int, error) {
	dsn := "file:" + filepath.ToSlash(path) + "?mode=ro"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return 0, err
	}
	defer db.Close()

	var rows int
	if err := db.QueryRow("SELECT COUNT(*) FROM backup_marker").Scan(&rows); err != nil {
		return 0, err
	}
	return rows, nil
}
