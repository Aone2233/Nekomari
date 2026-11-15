package panellock

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLeaseFencesAnotherProcessAndRecoversAfterExit(t *testing.T) {
	const child = "NEKOMARI_PANEL_LOCK_TEST_CHILD"
	if dir := os.Getenv(child); dir != "" {
		lease, err := Acquire(dir)
		if !errors.Is(err, ErrInUse) {
			if lease != nil {
				_ = lease.Close()
			}
			t.Fatalf("child acquired or received wrong error: %v", err)
		}
		return
	}
	dir := t.TempDir()
	lease, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close() })
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestLeaseFencesAnotherProcessAndRecoversAfterExit$")
	cmd.Env = append(os.Environ(), child+"="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("second process did not fail closed: %v\n%s", err, out)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Acquire(dir)
	if err != nil {
		t.Fatalf("lock was not released: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestLeaseRejectsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks on Windows requires a privilege")
	}
	dir := t.TempDir()
	if err := os.Symlink(filepath.Join(dir, "other"), filepath.Join(dir, FileName)); err != nil {
		t.Fatal(err)
	}
	if lease, err := Acquire(dir); err == nil {
		_ = lease.Close()
		t.Fatal("symlink lock was accepted")
	}
}
