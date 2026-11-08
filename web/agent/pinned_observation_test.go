package agent

import (
	"fmt"
	"testing"
	"time"
)

func TestPinnedObservationLimitsVolumeAndCardinality(t *testing.T) {
	var l pinnedObservationLimiter
	now := time.Unix(6000, 0)
	key := pinnedObservationKey{stage: "permit_write", task: 1}
	if !l.allow(key, now) || l.allow(key, now.Add(time.Minute)) || !l.allow(key, now.Add(10*time.Minute)) {
		t.Fatal("per-key observation limit failed")
	}
	for i := range 16 {
		if !l.allow(pinnedObservationKey{stage: fmt.Sprint(i), task: 2}, now.Add(11*time.Minute)) {
			t.Fatalf("global budget rejected item %d", i)
		}
	}
	if l.allow(pinnedObservationKey{stage: "overflow", task: 2}, now.Add(11*time.Minute)) {
		t.Fatal("global observation budget exceeded")
	}
	var full pinnedObservationLimiter
	for i := range 256 {
		if !full.allow(pinnedObservationKey{stage: fmt.Sprint(i), task: 3}, now.Add(time.Duration(i)*time.Minute)) {
			t.Fatal("unexpected limit while advancing time")
		}
	}
	if len(full.last) > 256 {
		t.Fatalf("unbounded cardinality: %d", len(full.last))
	}
}
