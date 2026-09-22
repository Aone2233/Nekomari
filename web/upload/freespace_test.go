package upload

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// unlimitedFreeSpace keeps a test's admission independent of the host's real
// disk. Every test that calls Init must inject a query.
func unlimitedFreeSpace(string) (int64, error) { return math.MaxInt64, nil }

func TestRequiredFreeSpaceCoversTheWholeFlow(t *testing.T) {
	// Above StagingExpansionFloor/StagingExpansionFactor the factor dominates.
	size := int64(1) << 30
	needed := RequiredFreeSpace(size)
	if needed <= size*UploadFootprintFactor+FreeSpaceReserve {
		t.Fatalf("budget %d does not include a staging expansion", needed)
	}
	if needed != size*UploadFootprintFactor+StagingExpansionFactor*size+FreeSpaceReserve {
		t.Fatalf("budget %d does not match the documented formula", needed)
	}
	// A payload too small to reach the extraction cap still reserves the cap.
	if small := RequiredFreeSpace(1); small != StagingExpansionFloor+UploadFootprintFactor+FreeSpaceReserve {
		t.Fatalf("small payload budget %d ignores the extraction floor", small)
	}
	// Saturation: an absurd size must not wrap into a small budget.
	if huge := RequiredFreeSpace(math.MaxInt64); huge != math.MaxInt64 {
		t.Fatalf("oversized payload budget wrapped to %d", huge)
	}
}

func TestAdmissionRequiresFreeSpaceForTheWholeFlow(t *testing.T) {
	size := int64(4 << 20)
	needed := RequiredFreeSpace(size)
	for _, tc := range []struct {
		name      string
		available int64
		wantErr   bool
	}{
		{name: "one byte short", available: needed - 1, wantErr: true},
		{name: "exactly the budget", available: needed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &Store{
				Root:      t.TempDir(),
				MaxSize:   size,
				FreeSpace: func(string) (int64, error) { return tc.available, nil },
			}
			_, err := store.Init(PurposeTheme, "theme.zip", size)
			if tc.wantErr {
				if !errors.Is(err, ErrNoSpace) {
					t.Fatalf("want ErrNoSpace, got %v", err)
				}
				// A refused admission must not leave a reservation behind.
				entries, readErr := os.ReadDir(store.Root)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if len(entries) != 0 {
					t.Fatalf("refused upload left %d entries in the store", len(entries))
				}
				if stats := store.Stats(); stats.Sessions != 0 || stats.ReservedBytes != 0 {
					t.Fatalf("refused upload was accounted for: %+v", stats)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFreeSpaceQueryFailureRefusesInsteadOfAssumingSpace(t *testing.T) {
	injected := errors.New("statfs: operation not supported")
	store := &Store{
		Root:      t.TempDir(),
		MaxSize:   1024,
		FreeSpace: func(string) (int64, error) { return 0, injected },
	}
	_, err := store.Init(PurposeTheme, "theme.zip", 1024)
	if !errors.Is(err, ErrNoSpace) {
		t.Fatalf("an unanswerable query must refuse the upload, got %v", err)
	}
	if !errors.Is(err, injected) {
		t.Fatalf("underlying query error is not visible: %v", err)
	}
	if entries, readErr := os.ReadDir(store.Root); readErr != nil || len(entries) != 0 {
		t.Fatalf("refused upload left the store dirty: %v, %d entries", readErr, len(entries))
	}
}

func TestFreeSpaceCheckProbesNearestExistingAncestor(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "missing", "nested", ".uploading")
	var probed string
	store := &Store{
		Root:    root,
		MaxSize: 1024,
		FreeSpace: func(path string) (int64, error) {
			probed = path
			return math.MaxInt64, nil
		},
	}
	if _, err := store.Init(PurposeTheme, "theme.zip", 1024); err != nil {
		t.Fatal(err)
	}
	// The root is created lazily by Init, so the first admission of a fresh
	// install has to probe an ancestor that exists.
	if probed != base {
		t.Fatalf("probed %q, want the nearest existing ancestor %q", probed, base)
	}
}

// TestPlatformFreeSpaceQuery exercises the build-tagged query itself. The
// value is not asserted: only that the platform answers for a directory that
// exists, so a stub that always fails would be caught.
func TestPlatformFreeSpaceQuery(t *testing.T) {
	free, err := platformFreeSpace(t.TempDir())
	if err != nil {
		t.Fatalf("platform free space query: %v", err)
	}
	if free <= 0 {
		t.Fatalf("platform free space query returned %d bytes", free)
	}
}

func TestExistingPathFindsAncestor(t *testing.T) {
	base := t.TempDir()
	probe, err := existingPath(filepath.Join(base, "a", "b"))
	if err != nil {
		t.Fatal(err)
	}
	if probe != base {
		t.Fatalf("existingPath = %q, want %q", probe, base)
	}
}
