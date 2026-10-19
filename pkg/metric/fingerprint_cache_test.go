package metric

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"
)

// resetFingerprintCache clears the package-level fingerprint cache and its
// computation counter so a test starts from a known state.
func resetFingerprintCache() {
	fingerprintCache.mu.Lock()
	fingerprintCache.entries = make(map[string]string, 256)
	fingerprintCache.mu.Unlock()
	fingerprintComputations.Store(0)
}

// fingerprintCacheState returns the number of cached canonical entries and the
// number of canonical strings that were actually hashed.
func fingerprintCacheState() (entries int, computations int64) {
	fingerprintCache.mu.RLock()
	entries = len(fingerprintCache.entries)
	fingerprintCache.mu.RUnlock()
	return entries, fingerprintComputations.Load()
}

// TestFingerprintCacheReuse verifies that one canonical tag set is hashed once
// and then served from the cache, no matter how many points carry it.
func TestFingerprintCacheReuse(t *testing.T) {
	resetFingerprintCache()

	first := map[string]string{"task_id": "7", "region": "ap"}
	hash, canonical, err := tagsFingerprint(first)
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	if entries, computations := fingerprintCacheState(); entries != 1 || computations != 1 {
		t.Fatalf("after first call entries = %d, computations = %d, want 1/1", entries, computations)
	}

	// The same map repeated must not hash again.
	for i := 0; i < 10; i++ {
		again, againCanonical, err := tagsFingerprint(first)
		if err != nil {
			t.Fatalf("fingerprint repeat: %v", err)
		}
		if again != hash || againCanonical != canonical {
			t.Fatalf("repeat %d = (%s, %s), want (%s, %s)", i, again, againCanonical, hash, canonical)
		}
	}
	// A freshly built map with the same content and a different insertion order
	// is the same canonical input, so it must also hit the cache.
	reordered := map[string]string{"region": "ap", "task_id": "7"}
	again, _, err := tagsFingerprint(reordered)
	if err != nil {
		t.Fatalf("fingerprint reordered: %v", err)
	}
	if again != hash {
		t.Fatalf("reordered hash = %s, want %s", again, hash)
	}
	if _, computations := fingerprintCacheState(); computations != 1 {
		t.Fatalf("computations = %d, want 1 for one canonical input", computations)
	}

	// A different tag set is a different canonical input.
	if _, _, err := tagsFingerprint(map[string]string{"task_id": "8"}); err != nil {
		t.Fatalf("fingerprint distinct: %v", err)
	}
	if entries, computations := fingerprintCacheState(); entries != 2 || computations != 2 {
		t.Fatalf("after distinct call entries = %d, computations = %d, want 2/2", entries, computations)
	}
}

// TestFingerprintNilEmptyEquivalence verifies that a nil tag map and an empty
// tag map produce the same canonical form and the same fingerprint, both in
// tagsFingerprint and in the prepared ingest points.
func TestFingerprintNilEmptyEquivalence(t *testing.T) {
	resetFingerprintCache()

	nilHash, nilCanonical, err := tagsFingerprint(nil)
	if err != nil {
		t.Fatalf("fingerprint nil: %v", err)
	}
	emptyHash, emptyCanonical, err := tagsFingerprint(map[string]string{})
	if err != nil {
		t.Fatalf("fingerprint empty: %v", err)
	}
	if nilHash != emptyHash {
		t.Fatalf("nil hash %s != empty hash %s", nilHash, emptyHash)
	}
	if nilCanonical != emptyCanonical || nilCanonical != "{}" {
		t.Fatalf("canonical = (%q, %q), want both {}", nilCanonical, emptyCanonical)
	}
	if _, computations := fingerprintCacheState(); computations != 0 {
		t.Fatalf("computations = %d, want 0 for the shared empty canonical form", computations)
	}

	sum := sha256.Sum256([]byte("{}"))
	if nilHash != hex.EncodeToString(sum[:]) {
		t.Fatalf("empty fingerprint = %s, want %s", nilHash, hex.EncodeToString(sum[:]))
	}

	at := time.Now().UTC()
	nilPoints, err := prepareMetricPoints([]Point{{MetricName: "m", EntityID: "e", Timestamp: at, Value: 1}})
	if err != nil {
		t.Fatalf("prepare nil points: %v", err)
	}
	emptyPoints, err := prepareMetricPoints([]Point{{
		MetricName: "m",
		EntityID:   "e",
		Timestamp:  at,
		Value:      1,
		Tags:       map[string]string{},
		Labels:     map[string]string{},
	}})
	if err != nil {
		t.Fatalf("prepare empty points: %v", err)
	}
	if nilPoints[0].tagsHash != emptyPoints[0].tagsHash || nilPoints[0].labelsHash != emptyPoints[0].labelsHash {
		t.Fatalf("prepared hashes differ: nil=%#v empty=%#v", nilPoints[0], emptyPoints[0])
	}
	if nilPoints[0].tagsJSON != "{}" || nilPoints[0].labelsJSON != "{}" {
		t.Fatalf("prepared JSON = %q / %q, want {} / {}", nilPoints[0].tagsJSON, nilPoints[0].labelsJSON)
	}
	if nilPoints[0].tagsJSON != emptyPoints[0].tagsJSON || nilPoints[0].labelsJSON != emptyPoints[0].labelsJSON {
		t.Fatalf("prepared JSON differs: nil=%#v empty=%#v", nilPoints[0], emptyPoints[0])
	}
}

// TestPrepareMetricPointsKeepsTimestampNormalisation verifies that dropping the
// Point.normalized call from the ingest path keeps the timestamp canonical.
func TestPrepareMetricPointsKeepsTimestampNormalisation(t *testing.T) {
	zone := time.FixedZone("UTC+8", 8*60*60)
	at := time.Date(2026, 7, 15, 20, 0, 0, 0, zone)
	prepared, err := prepareMetricPoints([]Point{{MetricName: "m", EntityID: "e", Timestamp: at, Value: 1}})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if prepared[0].timestamp != at.UTC().UnixMilli() {
		t.Fatalf("timestamp = %d, want %d", prepared[0].timestamp, at.UTC().UnixMilli())
	}
}

// TestFingerprintCacheBound verifies the cache clears itself once it reaches
// its capacity instead of growing without bound.
func TestFingerprintCacheBound(t *testing.T) {
	resetFingerprintCache()

	for i := 0; i < fingerprintCacheLimit+8; i++ {
		tags := map[string]string{"id": strconv.Itoa(i)}
		if _, _, err := tagsFingerprint(tags); err != nil {
			t.Fatalf("fingerprint %d: %v", i, err)
		}
	}
	entries, computations := fingerprintCacheState()
	if entries > fingerprintCacheLimit {
		t.Fatalf("cache entries = %d, want <= %d", entries, fingerprintCacheLimit)
	}
	if computations != int64(fingerprintCacheLimit+8) {
		t.Fatalf("computations = %d, want %d", computations, fingerprintCacheLimit+8)
	}

	// The cache must still answer correctly right after the reset.
	hash, canonical, err := tagsFingerprint(map[string]string{"id": "0"})
	if err != nil {
		t.Fatalf("fingerprint after reset: %v", err)
	}
	if canonical != `{"id":"0"}` {
		t.Fatalf("canonical = %s, want %s", canonical, `{"id":"0"}`)
	}
	want := hashCanonicalTags(canonical)
	if hash != want {
		t.Fatalf("hash = %s, want %s", hash, want)
	}
}

// TestFingerprintCacheConcurrent exercises the cache from many goroutines. Run
// with -race.
func TestFingerprintCacheConcurrent(t *testing.T) {
	resetFingerprintCache()

	sets := []map[string]string{
		{"task_id": "1"},
		{"task_id": "2"},
		{"device_index": "0", "device_name": "GPU 0"},
		nil,
	}
	// Warm the cache so the assertion below measures reuse rather than the
	// benign first-miss race.
	want := make([]string, len(sets))
	for i, set := range sets {
		hash, _, err := tagsFingerprint(set)
		if err != nil {
			t.Fatalf("warm fingerprint %d: %v", i, err)
		}
		want[i] = hash
	}
	_, warmed := fingerprintCacheState()

	const goroutines = 16
	const iterations = 200
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				index := i % len(sets)
				set := sets[index]
				hash, _, err := tagsFingerprint(set)
				if err != nil {
					errs <- err
					return
				}
				if hash != want[index] {
					errs <- fmt.Errorf("set %d hash = %s, want %s", index, hash, want[index])
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent fingerprint: %v", err)
		}
	}
	if _, computations := fingerprintCacheState(); computations != warmed {
		t.Fatalf("computations grew from %d to %d; warmed entries should be reused", warmed, computations)
	}
}
