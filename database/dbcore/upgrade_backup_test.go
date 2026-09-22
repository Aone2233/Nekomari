package dbcore

import (
	"archive/zip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"

	"github.com/Aone2233/nekomari/cmd/flags"
	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/internal/sqlitetune"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// The tests below pin the ordering of the pre-upgrade archive: it has to be
// written before migrations.Run rewrites the database it exists to protect.
// They run doInitialize against a throwaway ./data tree, so each one owns the
// process-wide initialization state for its duration.

// prepareUpgradeTestWorkspace makes the working directory a throwaway ./data
// tree and resets the one-shot initialization state so Initialize() can run.
func prepareUpgradeTestWorkspace(t *testing.T) string {
	t.Helper()

	previousType, previousFile, previousVersion := flags.DatabaseType, flags.DatabaseFile, versionID
	resetDatabaseState()

	dir := t.TempDir()
	t.Chdir(dir)
	// Registered after t.TempDir and t.Chdir, so it runs before them: the
	// database handle has to be closed before the tree it lives in is removed.
	t.Cleanup(func() {
		resetDatabaseState()
		flags.DatabaseType, flags.DatabaseFile = previousType, previousFile
		SetVersionID(previousVersion)
	})

	flags.DatabaseType = flags.DatabaseTypeSQLite
	flags.DatabaseFile = filepath.Join(".", "data", "komari.db")
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatalf("create data dir: %v", err)
	}
	return dir
}

// resetDatabaseState returns the package-level initialization to "not yet
// initialized", which is what Initialize's sync.Once would otherwise block.
func resetDatabaseState() {
	_ = Close()
	instance = nil
	once = sync.Once{}
	initErr = nil
	dbFileExistedAtStartup = false
	SetVersionID("")
}

// createUpgradeFixtureDatabase writes a database in the pre-migration shape the
// test needs, and closes it before Initialize opens the same file.
func createUpgradeFixtureDatabase(t *testing.T, path string, statements ...string) {
	t.Helper()
	sqlDB, err := sqlitetune.Open(buildSQLiteDSN(path), mainSQLiteOptions())
	if err != nil {
		t.Fatalf("open fixture database: %v", err)
	}
	db, err := gorm.Open(sqlite.New(sqlite.Config{Conn: sqlDB}), &gorm.Config{})
	if err != nil {
		_ = sqlDB.Close()
		t.Fatalf("open fixture gorm: %v", err)
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			_ = sqlDB.Close()
			t.Fatalf("exec %q: %v", statement, err)
		}
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close fixture database: %v", err)
	}
}

// openDatabaseFile opens a database file for assertions. The archive is opened
// through the same tuned connector as the live database so a copied WAL is
// recovered the same way.
func openDatabaseFile(t *testing.T, path string) *gorm.DB {
	t.Helper()
	sqlDB, err := sqlitetune.Open(buildSQLiteDSN(path), mainSQLiteOptions())
	if err != nil {
		t.Fatalf("open database %s: %v", path, err)
	}
	db, err := gorm.Open(sqlite.New(sqlite.Config{Conn: sqlDB}), &gorm.Config{})
	if err != nil {
		_ = sqlDB.Close()
		t.Fatalf("open gorm for %s: %v", path, err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func upgradeArchives(t *testing.T) []string {
	t.Helper()
	archives, err := filepath.Glob(filepath.Join("data", "backup", "upgrade-*.zip"))
	if err != nil {
		t.Fatalf("glob upgrade archives: %v", err)
	}
	sort.Strings(archives)
	return archives
}

func extractArchive(t *testing.T, zipPath, dstDir string) {
	t.Helper()
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("open archive %s: %v", zipPath, err)
	}
	defer reader.Close()
	for _, entry := range reader.File {
		target := filepath.Join(dstDir, filepath.FromSlash(entry.Name))
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				t.Fatalf("create %s: %v", target, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatalf("create %s: %v", filepath.Dir(target), err)
		}
		source, err := entry.Open()
		if err != nil {
			t.Fatalf("open entry %s: %v", entry.Name, err)
		}
		destination, err := os.Create(target)
		if err != nil {
			source.Close()
			t.Fatalf("create %s: %v", target, err)
		}
		if _, err := io.Copy(destination, source); err != nil {
			source.Close()
			destination.Close()
			t.Fatalf("extract %s: %v", entry.Name, err)
		}
		source.Close()
		destination.Close()
	}
}

func archiveEntryNames(t *testing.T, zipPath string) []string {
	t.Helper()
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("open archive %s: %v", zipPath, err)
	}
	defer reader.Close()
	names := make([]string, 0, len(reader.File))
	for _, entry := range reader.File {
		names = append(names, entry.Name)
	}
	sort.Strings(names)
	return names
}

// legacyClientInfoTable is the pre-0.0.5 shape that migrateLegacyClientInfo
// renames to client_infos_backup — an artifact migrations destroy, which makes
// it the marker for "this archive predates migrations".
const legacyClientInfoTable = `CREATE TABLE client_infos (
	uuid TEXT PRIMARY KEY, name TEXT NOT NULL, cpu_name TEXT, virtualization TEXT, arch TEXT,
	cpu_cores INTEGER, os TEXT, gpu_name TEXT, ipv4 TEXT, ipv6 TEXT, region TEXT, remark TEXT,
	public_remark TEXT, mem_total INTEGER, swap_total INTEGER, disk_total INTEGER, version TEXT,
	weight INTEGER, price REAL, billing_cycle INTEGER, expired_at TIMESTAMP,
	created_at TIMESTAMP, updated_at TIMESTAMP
)`

func TestUpgradeBackupSnapshotPrecedesMigrations(t *testing.T) {
	dir := prepareUpgradeTestWorkspace(t)
	dataDir := filepath.Join(dir, "data")

	createUpgradeFixtureDatabase(t, filepath.Join(dataDir, "komari.db"),
		// The config store in its current key/value shape, holding the version
		// the data was produced by. config.Set stores a string as JSON text.
		`CREATE TABLE configs (key TEXT PRIMARY KEY, value TEXT)`,
		`INSERT INTO configs (key, value) VALUES ('system_version', '"v-old"')`,
		legacyClientInfoTable,
	)
	// The local metric store: large, and not rewritten by any startup migration.
	if err := os.WriteFile(filepath.Join(dataDir, "metrics.db"), make([]byte, 64*1024), 0o600); err != nil {
		t.Fatalf("write metrics.db: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "metrics.db-wal"), make([]byte, 4096), 0o600); err != nil {
		t.Fatalf("write metrics.db-wal: %v", err)
	}

	SetVersionID("v-new")
	if err := Initialize(); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	archives := upgradeArchives(t)
	if len(archives) != 1 {
		t.Fatalf("upgrade archives = %v, want exactly one", archives)
	}

	// The metric store stays out of the archive and stays on disk.
	entries := archiveEntryNames(t, archives[0])
	for _, entry := range entries {
		if filepath.Base(entry) == "metrics.db" || filepath.Base(entry) == "metrics.db-wal" || filepath.Base(entry) == "metrics.db-shm" {
			t.Fatalf("archive still carries the metric store: %v", entries)
		}
	}
	if _, err := os.Stat(filepath.Join(dataDir, "metrics.db")); err != nil {
		t.Fatalf("metric store was removed from ./data: %v", err)
	}

	// The archive is the pre-migration state: the legacy table is still there
	// and the marker still names the old version.
	extracted := filepath.Join(t.TempDir(), "archive")
	extractArchive(t, archives[0], extracted)
	archived := openDatabaseFile(t, filepath.Join(extracted, "komari.db"))
	if !archived.Migrator().HasTable("client_infos") {
		t.Fatal("archive is missing the pre-migration client_infos table: it was written after migrations.Run")
	}
	if archived.Migrator().HasTable("client_infos_backup") {
		t.Fatal("archive contains the post-migration client_infos_backup table")
	}
	if got := versionMarkerValue(t, archived); got != "v-old" {
		t.Fatalf("archived version marker = %q, want %q", got, "v-old")
	}

	// The live database went through migrations and recorded the new version.
	live := ReadyDBInstance()
	if live == nil {
		t.Fatal("no live database instance")
	}
	if live.Migrator().HasTable("client_infos") {
		t.Fatal("live database still has the legacy client_infos table: migrations did not run")
	}
	if !live.Migrator().HasTable("client_infos_backup") {
		t.Fatal("live database is missing client_infos_backup")
	}
	recorded, err := config.GetAs[string](SystemVersionKey)
	if err != nil || recorded != "v-new" {
		t.Fatalf("recorded version marker = %q (err %v), want %q", recorded, err, "v-new")
	}
}

func TestUpgradeBackupSkippedWhenVersionUnchanged(t *testing.T) {
	dir := prepareUpgradeTestWorkspace(t)
	dataDir := filepath.Join(dir, "data")

	// The marker is read before config.SetDb is allowed to run, so this also
	// pins that read: if it failed, the existing database file would look like
	// an unmarked old installation and a backup would be taken.
	createUpgradeFixtureDatabase(t, filepath.Join(dataDir, "komari.db"),
		`CREATE TABLE configs (key TEXT PRIMARY KEY, value TEXT)`,
		`INSERT INTO configs (key, value) VALUES ('system_version', '"v-same"')`,
	)

	SetVersionID("v-same")
	if err := Initialize(); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if archives := upgradeArchives(t); len(archives) != 0 {
		t.Fatalf("unexpected upgrade archives: %v", archives)
	}
}

func TestUpgradeBackupSkippedOnFreshInstall(t *testing.T) {
	prepareUpgradeTestWorkspace(t)

	SetVersionID("v-first")
	if err := Initialize(); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if archives := upgradeArchives(t); len(archives) != 0 {
		t.Fatalf("fresh install produced upgrade archives: %v", archives)
	}
	recorded, err := config.GetAs[string](SystemVersionKey)
	if err != nil || recorded != "v-first" {
		t.Fatalf("recorded version marker = %q (err %v), want %q", recorded, err, "v-first")
	}
}

func TestUpgradeBackupSkippedWithoutVersionID(t *testing.T) {
	dir := prepareUpgradeTestWorkspace(t)
	createUpgradeFixtureDatabase(t, filepath.Join(dir, "data", "komari.db"), legacyClientInfoTable)

	// No version injected (some test and CLI paths): nothing to compare, no
	// snapshot, and no marker written.
	if err := Initialize(); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if archives := upgradeArchives(t); len(archives) != 0 {
		t.Fatalf("unexpected upgrade archives: %v", archives)
	}
}

func versionMarkerValue(t *testing.T, db *gorm.DB) string {
	t.Helper()
	var raw string
	if err := db.Raw("SELECT value FROM configs WHERE key = ? LIMIT 1", SystemVersionKey).Row().Scan(&raw); err != nil {
		t.Fatalf("read version marker: %v", err)
	}
	var value string
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		t.Fatalf("decode version marker %q: %v", raw, err)
	}
	return value
}
