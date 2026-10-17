package server

import (
	"testing"
	"time"
)

func TestRouteScheduleIsIndependentOfMedia(t *testing.T) {
	now := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	media := now.Add(6 * time.Hour)
	if got := nextProbeDelay(now, media, now.Add(2*time.Hour), true); got != 2*time.Hour {
		t.Fatalf("route delayed until media tick: %s", got)
	}
	if got := nextProbeDelay(now, media, time.Time{}, false); got != 6*time.Hour {
		t.Fatalf("disabled routes affect media: %s", got)
	}
	if got := nextProbeDelay(now, media, now.Add(-time.Minute), true); got != time.Second {
		t.Fatalf("overdue route busy loop: %s", got)
	}
}
