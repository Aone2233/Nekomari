package upload

import (
	"os"
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
