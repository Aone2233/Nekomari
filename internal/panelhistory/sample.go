package panelhistory

import (
	"context"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Aone2233/nekomari/internal/metricstore"
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
	mu sync.Mutex
}

func (s *Sampler) Collect(ctx context.Context) error {
	if !s.mu.TryLock() {
		return nil
	}
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	return metricstore.WritePanelSample(ctx, metricstore.PanelSample{
		At: time.Now().UTC(), RSSBytes: RSSBytes(), HeapBytes: memory.HeapAlloc, Goroutines: runtime.NumGoroutine(),
	})
}
