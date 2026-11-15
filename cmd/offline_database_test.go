package cmd

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Aone2233/nekomari/internal/panellock"
)

func TestOfflineDatabaseFencesLivePanelAndPendingRestore(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.Mkdir("data", 0o755); err != nil {
		t.Fatal(err)
	}
	lease, err := panellock.Acquire("data")
	if err != nil {
		t.Fatal(err)
	}
	called := false
	action := func() error { called = true; return nil }
	if err := withOfflineDatabase(action); !errors.Is(err, panellock.ErrInUse) || called {
		t.Fatalf("live panel fence = %v, called = %v", err, called)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("data", "komari.db"), []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("data", "backup.zip"), []byte("pending"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := withOfflineDatabase(action); err == nil || !strings.Contains(err.Error(), "restore is pending") || called {
		t.Fatalf("pending restore fence = %v, called = %v", err, called)
	}
	if data, err := os.ReadFile(filepath.Join("data", "komari.db")); err != nil || string(data) != "original" {
		t.Fatalf("account command changed DB while restore pending: %q, %v", data, err)
	}
	if err := os.Remove(filepath.Join("data", "backup.zip")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("data", ".restore-in-progress"), []byte("journal"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := withOfflineDatabase(action); err == nil || !strings.Contains(err.Error(), "restore journal") || called {
		t.Fatalf("restore journal fence = %v, called = %v", err, called)
	}
	lease, err = panellock.Acquire("data")
	if err != nil {
		t.Fatalf("rejected command left lease held: %v", err)
	}
	_ = lease.Close()
}

func TestOfflineDatabaseSuccessfulActionReleasesLease(t *testing.T) {
	const child = "NEKOMARI_OFFLINE_COMMAND_TEST"
	if os.Getenv(child) == "" {
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestOfflineDatabaseSuccessfulActionReleasesLease$")
		cmd.Env = append(os.Environ(), child+"="+t.TempDir())
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("offline command subprocess: %v\n%s", err, out)
		}
		return
	}
	if err := os.Chdir(os.Getenv(child)); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir("data", 0o755); err != nil {
		t.Fatal(err)
	}
	// An empty existing file is a scratch SQLite database, never production.
	if err := os.WriteFile(filepath.Join("data", "komari.db"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	called := false
	if err := withOfflineDatabase(func() error { called = true; return nil }); err != nil || !called {
		t.Fatalf("offline action = %v, called = %v", err, called)
	}
	lease, err := panellock.Acquire("data")
	if err != nil {
		t.Fatalf("successful command left lease held: %v", err)
	}
	_ = lease.Close()
}
