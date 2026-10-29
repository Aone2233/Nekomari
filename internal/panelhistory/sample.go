package panelhistory

import (
	"context"
	"database/sql"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/internal/metricstore"
	"github.com/Aone2233/nekomari/web/public"
)

// RSSBytes returns zero when the platform cannot provide a reading.
func RSSBytes() uint64 {
	raw, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(raw))
	if len(fields) < 2 {
		return 0
	}
	pages, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0
	}
	return pages * uint64(os.Getpagesize())
}

// Sampler skips an overlapping run instead of accumulating work while the
// metric store is busy. Its caller supplies a bounded, cancellable context.
type Sampler struct {
	mu     sync.Mutex
	pools  metricstore.PanelPoolSampler
	missed atomic.Bool
}

func (s *Sampler) Collect(ctx context.Context) error {
	if !s.mu.TryLock() {
		s.missed.Store(true)
		return nil
	}
	defer s.mu.Unlock()
	if s.missed.Swap(false) {
		s.pools.Reset()
	}
	if err := ctx.Err(); err != nil {
		s.pools.Reset()
		return err
	}
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	var mainDB *sql.DB
	if db := dbcore.ReadyDBInstance(); db != nil {
		mainDB, _ = db.DB()
	}
	cache := public.ThemeCacheSnapshot()
	return s.pools.Write(ctx, metricstore.PanelSample{
		At: time.Now(), RSSBytes: RSSBytes(), HeapBytes: memory.HeapAlloc, Goroutines: runtime.NumGoroutine(),
		ThemeCache: &metricstore.PanelThemeCacheSample{Disk: cache.Disk, MemoryLoaded: cache.MemoryLoaded},
	}, mainDB)
}
