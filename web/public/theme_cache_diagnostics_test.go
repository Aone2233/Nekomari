package public

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestThemeCacheSnapshotIsNonInvasive(t *testing.T) {
	t.Chdir(t.TempDir())
	root := enableThemeCacheForTest(t)
	forgetEmbeddedDistForTest(t)
	if got := ThemeCacheSnapshot(); got.Disk != "uninitialized" || got.MemoryLoaded {
		t.Fatalf("cold snapshot = %+v", got)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("snapshot materialized the cache: %v", err)
	}

	done := make(chan struct{})
	defaultThemeCache.mu.Lock()
	defaultThemeCache.done = done
	defaultThemeCache.mu.Unlock()
	if got := ThemeCacheSnapshot(); got.Disk != "initializing" {
		t.Fatalf("in-flight snapshot = %+v", got)
	}
	defaultThemeCache.mu.Lock()
	defaultThemeCache.err = errors.New("private filesystem path")
	defaultThemeCache.mu.Unlock()
	close(done)
	if got := ThemeCacheSnapshot(); got.Disk != "failed" {
		t.Fatalf("failed snapshot = %+v", got)
	}
}

func TestThemeCacheSnapshotTracksDiskAndFallbackIndependently(t *testing.T) {
	router := newRouter(t)
	root := enableThemeCacheForTest(t)
	forgetEmbeddedDistForTest(t)
	if response := get(t, router, "/themes/default/dist/index.html"); response.Code != http.StatusOK {
		t.Fatalf("initial asset status = %d", response.Code)
	}
	if got := ThemeCacheSnapshot(); got.Disk != "ready" || got.MemoryLoaded {
		t.Fatalf("disk snapshot = %+v", got)
	}
	// A missing asset in an otherwise available cache is an ordinary 404.
	_, hit, authoritative := lookupDefaultThemeCachedAsset("missing-file-for-diagnostics")
	if hit || !authoritative || ThemeCacheSnapshot().Disk != "ready" {
		t.Fatal("an ordinary cache miss marked the disk as degraded")
	}
	if err := os.RemoveAll(filepath.Join(root, embeddedDistArchiveKey())); err != nil {
		t.Fatal(err)
	}
	if response := get(t, router, "/themes/default/dist/index.html"); response.Code != http.StatusOK {
		t.Fatalf("fallback asset status = %d", response.Code)
	}
	if got := ThemeCacheSnapshot(); got.Disk != "degraded" || !got.MemoryLoaded {
		t.Fatalf("fallback snapshot = %+v", got)
	}
}

func TestThemeCacheDisabledMemoryFallbackAndConcurrentReads(t *testing.T) {
	t.Chdir(t.TempDir())
	root := enableThemeCacheForTest(t)
	forgetEmbeddedDistForTest(t)
	t.Setenv(themeCacheEnvVar, "0")
	if got := ThemeCacheSnapshot(); got.Disk != "disabled" || got.MemoryLoaded {
		t.Fatalf("disabled snapshot = %+v", got)
	}
	if len(embeddedDistFiles()) == 0 {
		t.Fatal("fallback did not load")
	}
	if got := ThemeCacheSnapshot(); got.Disk != "disabled" || !got.MemoryLoaded {
		t.Fatalf("loaded snapshot = %+v", got)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("disabled cache touched disk: %v", err)
	}
	var group sync.WaitGroup
	for i := 0; i < 20; i++ {
		group.Go(func() {
			for n := 0; n < 50; n++ {
				if got := ThemeCacheSnapshot(); got.Disk != "disabled" || !got.MemoryLoaded {
					t.Errorf("concurrent snapshot = %+v", got)
				}
			}
		})
	}
	group.Wait()
}
