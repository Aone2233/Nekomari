package fs

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Model a write that already opened its file: closing the root alone cannot
// stop that file handle from changing the backup after plugin unload returns.
func TestCloseWaitsForAdmittedOpenFileWrite(t *testing.T) {
	root := t.TempDir()
	m, err := New(nil, root, root, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	opened, release := make(chan struct{}), make(chan struct{})
	if !m.startAsync(func() {
		file, err := m.nodeOpenFile(filepath.Join(root, "late.txt"), os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			t.Errorf("open: %v", err)
			close(opened)
			return
		}
		defer file.Close()
		close(opened)
		<-release
		if _, err := file.WriteString("finished"); err != nil {
			t.Errorf("write: %v", err)
		}
	}) {
		t.Fatal("worker was not accepted")
	}
	<-opened
	done := make(chan struct{})
	go func() { m.Close(); close(done) }()
	select {
	case <-done:
		t.Fatal("close returned while the open file could still be written")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("close did not finish after the write")
	}
	if data, err := os.ReadFile(filepath.Join(root, "late.txt")); err != nil || string(data) != "finished" {
		t.Fatalf("written file = %q, %v", data, err)
	}
	if m.startAsync(func() {}) {
		t.Fatal("filesystem accepted work after close")
	}
}

func TestCloseOrdersConcurrentAsyncAdmission(t *testing.T) {
	root := t.TempDir()
	m, err := New(nil, root, root, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var group sync.WaitGroup
	for range 64 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			m.startAsync(func() {})
		}()
	}
	close(start)
	m.Close()
	group.Wait()
	if m.startAsync(func() {}) {
		t.Fatal("filesystem accepted work after close")
	}
}
