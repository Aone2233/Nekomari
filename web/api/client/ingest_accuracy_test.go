package client

import (
	"testing"
	"time"

	v2 "github.com/Aone2233/nekomari/protocol/v2"
)

func TestNormalizeReportTimestampBoundaries(t *testing.T) {
	now := time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		offset time.Duration
		accept bool
	}{
		{"current", 0, true}, {"future_boundary", 30 * time.Second, true},
		{"old_boundary", -5 * time.Minute, true}, {"future", 30*time.Second + time.Nanosecond, false},
		{"stale", -5*time.Minute - time.Nanosecond, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sampled := now.Add(tc.offset).In(time.FixedZone("client", 8*3600))
			r := v2.Report{SampledAt: sampled, ReceivedAt: now.Add(time.Hour)}
			err := normalizeReportTimestamp(&r, now)
			if (err == nil) != tc.accept {
				t.Fatalf("accepted=%v error=%v", tc.accept, err)
			}
			if tc.accept && (!r.UpdatedAt.Equal(sampled) || r.UpdatedAt.Location() != time.UTC || !r.ReceivedAt.Equal(now)) {
				t.Fatalf("timestamp moved or received_at trusted: %+v", r)
			}
		})
	}
	r := v2.Report{UpdatedAt: now.Add(-time.Hour)}
	if err := normalizeReportTimestamp(&r, now); err != nil {
		t.Fatal(err)
	}
	if !r.UpdatedAt.Equal(now) || r.Quality["timestamp"] != "server_received_legacy" {
		t.Fatalf("legacy timestamp must be explicit: %+v", r)
	}
}
