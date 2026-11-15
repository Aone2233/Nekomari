package server

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/internal/panellock"
	"github.com/Aone2233/nekomari/web/backup"
	_ "github.com/mattn/go-sqlite3"
)

// The child isolates terminal process-wide shutdown fences and uses only new
// synthetic data. It exercises a real cleanup and startup-restore sequence.
func TestBackupRestorePublishedOnlyAfterCleanShutdown(t *testing.T) {
	const child = "NEKOMARI_RESTORE_HANDOFF_TEST_DIR"
	dir := os.Getenv(child)
	if dir == "" {
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestBackupRestorePublishedOnlyAfterCleanShutdown$")
		cmd.Env = append(os.Environ(), child+"="+t.TempDir())
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("restore handoff subprocess: %v\n%s", err, out)
		}
		return
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	app := New(Options{})
	if err := app.Bootstrap(); err != nil {
		t.Fatalf("bootstrap scratch panel: %v", err)
	}
	archive := filepath.Join(dir, "source.zip")
	writeRestoreFixture(t, archive)
	file, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	if err := backup.StageUploadedBackup(file, "source.zip"); err != nil {
		_ = file.Close()
		t.Fatalf("stage scratch restore: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join("data", "backup.zip")); !os.IsNotExist(err) {
		t.Fatalf("archive became eligible before shutdown: %v", err)
	}
	if _, err := panellock.Acquire("data"); err == nil {
		t.Fatal("panel data lease was not held")
	}
	if err := app.Shutdown(); err != nil {
		t.Fatalf("clean shutdown: %v", err)
	}
	if err := backup.ValidateArchive(filepath.Join("data", "backup.zip")); err != nil {
		t.Fatalf("shutdown did not publish a valid archive: %v", err)
	}
	lease, err := panellock.Acquire("data")
	if err != nil {
		t.Fatalf("data lease not released: %v", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}

	// dbcore initializes once per process; a fresh child performs real startup.
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestBackupRestoreHandoffStartupChild$")
	cmd.Env = append(os.Environ(), "NEKOMARI_RESTORE_STARTUP_TEST_DIR="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("restore startup subprocess: %v\n%s", err, out)
	}
}

func TestBackupRestoreHandoffStartupChild(t *testing.T) {
	dir := os.Getenv("NEKOMARI_RESTORE_STARTUP_TEST_DIR")
	if dir == "" {
		return
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	app := New(Options{})
	if err := app.Bootstrap(); err != nil {
		t.Fatalf("boot restored scratch panel: %v", err)
	}
	data, err := os.ReadFile(filepath.Join("data", "fixture-asset.txt"))
	if err != nil || !bytes.Equal(data, []byte("restored asset")) {
		t.Fatalf("restored asset = %q, error = %v", data, err)
	}
	if _, err := os.Stat(filepath.Join("data", "backup.zip")); !os.IsNotExist(err) {
		t.Fatalf("restore archive remains after startup: %v", err)
	}
	archives, err := filepath.Glob(filepath.Join("data", "backup", "pre-restore-*.zip"))
	if err != nil || len(archives) != 1 {
		t.Fatalf("pre-restore scratch archive = %v, error = %v", archives, err)
	}
	if err := app.Shutdown(); err != nil {
		t.Fatalf("shutdown restored panel: %v", err)
	}
}

func TestInvalidPublishedBackupStopsBeforeDatabaseOpen(t *testing.T) {
	const child = "NEKOMARI_INVALID_RESTORE_TEST_DIR"
	dir := os.Getenv(child)
	if dir == "" {
		dir = t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, "data"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "data", "backup.zip"), []byte("not a ZIP"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "data", "keep.txt"), []byte("original"), 0o600); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestInvalidPublishedBackupStopsBeforeDatabaseOpen$")
			cmd.Env = append(os.Environ(), child+"="+dir)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("invalid restore startup subprocess: %v\n%s", err, out)
			}
		}
		return
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	app := New(Options{})
	if err := app.Bootstrap(); err == nil {
		t.Fatal("invalid published archive did not prevent startup")
	}
	if data, err := os.ReadFile(filepath.Join("data", "keep.txt")); err != nil || string(data) != "original" {
		t.Fatalf("original data = %q, error = %v", data, err)
	}
	if _, err := os.Stat(filepath.Join("data", "komari.db")); !os.IsNotExist(err) {
		t.Fatalf("startup created a database after restore failure: %v", err)
	}
	if _, err := os.Stat(filepath.Join("data", "backup.zip")); err != nil {
		t.Fatalf("startup removed the invalid archive: %v", err)
	}
	if err := app.Shutdown(); err != nil {
		t.Fatalf("clean up failed startup: %v", err)
	}
}

// A failed cleanup is terminal even if the caller, a fatal path, and several
// concurrent callers all attempt shutdown again. A fresh process must not
// interpret the retained archive as a restore request.
func TestFailedRestoreHandoffStaysInertAcrossRetryAndRestart(t *testing.T) {
	const child = "NEKOMARI_FAILED_RESTORE_TEST_DIR"
	dir := os.Getenv(child)
	if dir == "" {
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestFailedRestoreHandoffStaysInertAcrossRetryAndRestart$")
		cmd.Env = append(os.Environ(), child+"="+t.TempDir())
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("failed restore handoff subprocess: %v\n%s", err, out)
		}
		return
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	app := New(Options{})
	if err := app.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("data", "keep.txt"), []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(dir, "source.zip")
	writeRestoreFixture(t, archive)
	file, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	if err := backup.StageUploadedBackup(file, "source.zip"); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	cause := errors.New("injected cleanup failure")
	var calls atomic.Int32
	app.addCleanup("failure", func(context.Context) error {
		if calls.Add(1) == 1 {
			return cause
		}
		return nil
	})
	const callers = 8
	var wg sync.WaitGroup
	results := make(chan error, callers)
	for range callers {
		wg.Go(func() { results <- app.Shutdown() })
	}
	wg.Wait()
	close(results)
	for err := range results {
		if !errors.Is(err, cause) {
			t.Fatalf("shutdown retry = %v, want original failure", err)
		}
	}
	app.onFatal(errors.New("subsequent fatal error"))
	if calls.Load() != 1 {
		t.Fatalf("cleanup called %d times, want once", calls.Load())
	}
	if _, err := os.Stat(filepath.Join("data", "backup.zip")); !os.IsNotExist(err) {
		t.Fatalf("failed handoff published a restore: %v", err)
	}
	if staged, err := filepath.Glob(filepath.Join("data", ".backup-pending-*.zip")); err != nil || len(staged) != 1 {
		t.Fatalf("retained archive = %v, error = %v", staged, err)
	}
	if lease, err := panellock.Acquire("data"); err == nil {
		_ = lease.Close()
		t.Fatal("failed shutdown released data lease")
	}
	// This child exits to release the lease; the parent invokes a fresh startup
	// in a nested process only after that exit (see the separate driver below).
}

func TestFailedRestoreHandoffRestart(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestFailedRestoreHandoffStaysInertAcrossRetryAndRestart$")
	cmd.Env = append(os.Environ(), "NEKOMARI_FAILED_RESTORE_TEST_DIR="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed handoff child: %v\n%s", err, out)
	}
	cmd = exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestFailedRestoreStartupChild$")
	cmd.Env = append(os.Environ(), "NEKOMARI_FAILED_RESTORE_STARTUP_DIR="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("startup after failed handoff: %v\n%s", err, out)
	}
}

func TestFailedRestoreStartupChild(t *testing.T) {
	dir := os.Getenv("NEKOMARI_FAILED_RESTORE_STARTUP_DIR")
	if dir == "" {
		return
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	app := New(Options{})
	if err := app.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join("data", "keep.txt"))
	if err != nil || string(data) != "original" {
		t.Fatalf("original data after restart = %q, error = %v", data, err)
	}
	if staged, err := filepath.Glob(filepath.Join("data", ".backup-pending-*.zip")); err != nil || len(staged) != 1 {
		t.Fatalf("inert archive after restart = %v, error = %v", staged, err)
	}
	if err := app.Shutdown(); err != nil {
		t.Fatal(err)
	}
}

func TestFatalShutdownDrainsHTTPBeforeHandoff(t *testing.T) {
	const child = "NEKOMARI_FATAL_HTTP_TEST"
	if os.Getenv(child) == "" {
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestFatalShutdownDrainsHTTPBeforeHandoff$")
		cmd.Env = append(os.Environ(), child+"=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fatal HTTP subprocess: %v\n%s", err, out)
		}
		return
	}
	entered, release, handedOff := make(chan struct{}), make(chan struct{}), make(chan struct{})
	app := New(Options{})
	app.addCleanHandoff("handoff", func(context.Context) error { close(handedOff); return nil })
	app.server = newHTTPServer("", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = app.server.Serve(listener) }()
	response := make(chan error, 1)
	go func() {
		resp, err := http.Get("http://" + listener.Addr().String())
		if err == nil {
			_ = resp.Body.Close()
		}
		response <- err
	}()
	<-entered
	done := make(chan struct{})
	go func() { app.onFatal(errors.New("injected listener failure")); close(done) }()
	select {
	case <-handedOff:
		t.Fatal("fatal handoff ran while an HTTP handler was active")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("fatal shutdown did not finish after HTTP drained")
	}
	if err := <-response; err != nil {
		t.Fatal(err)
	}
	select {
	case <-handedOff:
	default:
		t.Fatal("clean fatal shutdown did not run handoff")
	}
}

func writeRestoreFixture(t *testing.T, path string) {
	t.Helper()
	database := filepath.Join(t.TempDir(), "fixture.db")
	db, err := sql.Open("sqlite3", database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(), `CREATE TABLE fixture (value TEXT); INSERT INTO fixture VALUES ('source')`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	dbBytes, err := os.ReadFile(database)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	for name, data := range map[string][]byte{
		"komari.db":            dbBytes,
		"komari-backup-markup": []byte("scratch marker"),
		"fixture-asset.txt":    []byte("restored asset"),
	} {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}
