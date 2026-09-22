package upload

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

// Operation names used for contention accounting. They are the caller-visible
// operations that take the store lock, plus the hourly maintenance pass.
const (
	opInit     = "init"
	opChunk    = "chunk"
	opMerge    = "merge"
	opComplete = "complete"
	opCancel   = "cancel"
	opCleanup  = "cleanup"
)

// OpTiming is how long one operation class held the store lock.
type OpTiming struct {
	Count int           `json:"count"`
	Total time.Duration `json:"total"`
	Max   time.Duration `json:"max"`
}

// ContentionStats answers one question with data instead of a guess: is the
// single store lock actually refusing unrelated work?
//
// Every write path -- chunk saves, merge, plugin and theme installation, backup
// finalization -- shares one non-queuing lock, so a caller that finds it held is
// answered 429 and has to retry. Splitting that lock into per-session exclusion
// plus a small global I/O semaphore is only worth its risk if the refusals and
// the hold durations are real, so they are measured first.
//
// Measured before this was written: the production panel had served **zero**
// archive uploads across every retained nginx log (all rotations, including the
// compressed ones), the upload store was empty, and there was not one 429 on an
// upload route. There was no contention to find, so the lock was deliberately
// left alone and this counter added in its place. If Busy stays empty in
// production, the lock is not the problem and should stay as it is.
type ContentionStats struct {
	// Busy counts refused lock acquisitions by operation: the direct evidence
	// of one caller blocking another.
	Busy map[string]int `json:"busy"`
	// Hold counts completed lock holds by operation, with total and worst-case
	// duration. Complete is the one to watch: it holds the lock across the whole
	// installation, which is the longest hold by construction.
	Hold map[string]OpTiming `json:"hold"`
}

// acquire takes the store lock for op without queueing, recording the refusal
// when someone else holds it and the hold duration when it is released. The
// returned release function must be called exactly once, and is safe to defer.
//
// It is a drop-in replacement for the TryLock/defer Unlock pair the store used
// before, so the locking behaviour is unchanged: this only observes it.
func (s *Store) acquire(op string) (func(), bool) {
	if !s.mu.TryLock() {
		s.noteBusy(op)
		return nil, false
	}
	start := time.Now()
	return func() {
		s.mu.Unlock()
		s.noteHold(op, time.Since(start))
	}, true
}

func (s *Store) noteBusy(op string) {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	if s.contention.Busy == nil {
		s.contention.Busy = make(map[string]int, 4)
	}
	s.contention.Busy[op]++
	s.contentionPending = true
}

func (s *Store) noteHold(op string, elapsed time.Duration) {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	if s.contention.Hold == nil {
		s.contention.Hold = make(map[string]OpTiming, 4)
	}
	timing := s.contention.Hold[op]
	timing.Count++
	timing.Total += elapsed
	if elapsed > timing.Max {
		timing.Max = elapsed
	}
	s.contention.Hold[op] = timing
	// The hourly maintenance pass takes the lock on every tick even on a panel
	// nobody is uploading to, so it must not be what makes the summary log fire.
	// Only caller work marks the figures worth reporting.
	if op != opCleanup {
		s.contentionPending = true
	}
}

// Contention returns a copy of the accumulated figures without clearing them.
func (s *Store) Contention() ContentionStats {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	return copyContention(s.contention)
}

// takeContentionSummary returns the figures gathered since the last call and
// marks them reported, so the hourly pass logs them once instead of repeating
// the same numbers forever. It reports false when nothing caller-driven
// happened, which is the normal case on a panel that is not being uploaded to.
func (s *Store) takeContentionSummary() (ContentionStats, bool) {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	if !s.contentionPending {
		return ContentionStats{}, false
	}
	s.contentionPending = false
	return copyContention(s.contention), true
}

// copyContention deep-copies the maps so a reader never shares state with the
// writer that is still updating it.
func copyContention(source ContentionStats) ContentionStats {
	out := ContentionStats{
		Busy: make(map[string]int, len(source.Busy)),
		Hold: make(map[string]OpTiming, len(source.Hold)),
	}
	for op, count := range source.Busy {
		out.Busy[op] = count
	}
	for op, timing := range source.Hold {
		out.Hold[op] = timing
	}
	return out
}

// formatBusy renders refusals as "op=count" pairs in a stable order, so the log
// line can be compared between runs instead of reshuffling every time.
func formatBusy(busy map[string]int) string {
	if len(busy) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(busy))
	for op, count := range busy {
		parts = append(parts, op+"="+strconv.Itoa(count))
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// formatHold renders holds as "op=count/max" pairs, also in a stable order.
func formatHold(hold map[string]OpTiming) string {
	if len(hold) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(hold))
	for op, timing := range hold {
		parts = append(parts, op+"="+strconv.Itoa(timing.Count)+"/"+timing.Max.Round(time.Millisecond).String())
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}
