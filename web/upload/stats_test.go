package upload

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMissingStoreClearsCachedReservationStats(t *testing.T) {
	store := &Store{Root: t.TempDir(), MaxSize: 10, FreeSpace: unlimitedFreeSpace}
	session, err := store.Init(PurposeTheme, "example.zip", 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CleanupExpired(); err != nil {
		t.Fatal(err)
	}
	before := store.Stats()
	if before.Sessions != 1 || before.ReservedBytes != 10 || before.LastScan.IsZero() {
		t.Fatalf("missing reservation snapshot: %+v", before)
	}
	if err := store.Cancel(session.ID); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(store.Root); err != nil {
		t.Fatal(err)
	}
	if err := store.CleanupExpired(); err != nil {
		t.Fatal(err)
	}
	after := store.Stats()
	if after.Sessions != 0 || after.ReservedBytes != 0 || after.OldestSessionAge != 0 || after.LastScan.IsZero() {
		t.Fatalf("stale reservation snapshot: %+v", after)
	}
}

func TestScanDurationPreservesLastSuccessfulSnapshot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "uploads")
	store := &Store{Root: root, MaxSize: 10, FreeSpace: unlimitedFreeSpace}
	session, err := store.Init(PurposeTheme, "example.zip", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CleanupExpired(); err != nil {
		t.Fatal(err)
	}
	before := store.Stats()
	if before.LastScan.IsZero() || before.LastScanDurationNS < 0 {
		t.Fatalf("successful scan lacks measured duration: %+v", before)
	}
	encoded, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	var published map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &published); err != nil {
		t.Fatal(err)
	}
	var duration int64
	if err := json.Unmarshal(published["last_scan_duration_ns"], &duration); err != nil || duration != before.LastScanDurationNS {
		t.Fatalf("scan duration must be a JSON nanosecond count: %s, err=%v", encoded, err)
	}
	metadata := filepath.Join(session.Directory, "upload.json")
	if err := os.Remove(metadata); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(metadata, 0700); err != nil {
		t.Fatal(err)
	}
	if err := store.CleanupExpired(); err == nil {
		t.Fatal("expected failed scan of a metadata directory")
	}
	after := store.Stats()
	if !after.LastScan.Equal(before.LastScan) || after.LastScanDurationNS != before.LastScanDurationNS {
		t.Fatalf("failed scan replaced known-good measurements: before=%+v after=%+v", before, after)
	}
}
