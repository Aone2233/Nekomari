package upload

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTemporaryStoreFileAllowlist(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{name: ".merged-1234567.zip", want: true},
		{name: ".0-987654321.part", want: true},
		{name: ".12-1.part", want: true},
		{name: "0.part", want: false},       // published chunk
		{name: "12.part", want: false},      // published chunk
		{name: "archive.zip", want: false},  // published archive
		{name: "upload.json", want: false},  // reservation metadata
		{name: ".merged-.zip", want: false}, // no random suffix
		{name: ".merged-abc.zip", want: false},
		{name: ".0-.part", want: false},
		{name: ".-1.part", want: false},
		{name: ".0-1.txt", want: false},
		{name: "notes.txt", want: false},
		{name: "..merged-1.zip", want: false},
	} {
		if got := isTemporaryStoreFile(tc.name); got != tc.want {
			t.Errorf("isTemporaryStoreFile(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestReconcileRemovesOnlyKnownScratchFiles covers the allowlist contract: the
// scratch names this package writes are reclaimed, everything else in the store
// survives and an unrecognised entry keeps failing admission.
func TestReconcileRemovesOnlyKnownScratchFiles(t *testing.T) {
	store := &Store{Root: t.TempDir(), MaxSize: 1 << 20, FreeSpace: unlimitedFreeSpace}
	session, err := store.Init(PurposeTheme, "theme.zip", 16)
	if err != nil {
		t.Fatal(err)
	}
	canonical := []string{"upload.json", "0.part", "archive.zip", "notes.txt"}
	if err := os.WriteFile(filepath.Join(session.Directory, "0.part"), make([]byte, 16), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(session.Directory, "archive.zip"), make([]byte, 16), 0o600); err != nil {
		t.Fatal(err)
	}
	// Not a name this package writes, even though it sits in a session
	// directory: an operator's own file must survive.
	if err := os.WriteFile(filepath.Join(session.Directory, "notes.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	scratch := map[string]int{
		".merged-1234.zip":  32,
		".0-987654321.part": 8,
	}
	for name, size := range scratch {
		if err := os.WriteFile(filepath.Join(session.Directory, name), make([]byte, size), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A directory that merely carries a scratch name is not a file this
	// package created and must not be removed.
	scratchDir := filepath.Join(session.Directory, ".merged-9.zip")
	if err := os.Mkdir(scratchDir, 0o700); err != nil {
		t.Fatal(err)
	}
	unknownDir := filepath.Join(store.Root, "keep-me")
	if err := os.Mkdir(unknownDir, 0o700); err != nil {
		t.Fatal(err)
	}

	files, bytes, err := store.ReconcileOrphans()
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if files != len(scratch) || bytes != 40 {
		t.Fatalf("reclaimed %d files / %d bytes, want %d / 40", files, bytes, len(scratch))
	}
	for name := range scratch {
		if _, err := os.Stat(filepath.Join(session.Directory, name)); !os.IsNotExist(err) {
			t.Errorf("scratch file %s survived reconciliation", name)
		}
	}
	for _, name := range canonical {
		if _, err := os.Stat(filepath.Join(session.Directory, name)); err != nil {
			t.Errorf("canonical file %s was removed: %v", name, err)
		}
	}
	if _, err := os.Stat(scratchDir); err != nil {
		t.Errorf("scratch-named directory was removed: %v", err)
	}
	if _, err := os.Stat(unknownDir); err != nil {
		t.Fatalf("unknown directory was removed: %v", err)
	}

	// An unrecognised entry still fails admission and is reported.
	if err := store.CleanupExpired(); err == nil {
		t.Fatal("unknown entry was not reported")
	}
	if _, err := store.Init(PurposeTheme, "b.zip", 8); err == nil {
		t.Fatal("admission succeeded despite an unknown entry")
	}
	stats := store.Stats()
	if stats.ReclaimedFiles != len(scratch) || stats.ReclaimedBytes != 40 {
		t.Fatalf("reclaimed totals not exposed: %+v", stats)
	}
}

// TestInterruptedMergeOrphanReclaimedOnRestart models a process killed in the
// middle of merge(): the scratch archive stays behind, the published chunks do
// not. A restart has to reclaim the scratch bytes without losing the
// reservation or the ability to finish the upload.
func TestInterruptedMergeOrphanReclaimedOnRestart(t *testing.T) {
	root := t.TempDir()
	size := int64(3)
	store := &Store{Root: root, MaxSize: 8, MaxSessions: 1, MaxTotalSize: 8, FreeSpace: unlimitedFreeSpace}
	session, err := store.Init(PurposeTheme, "theme.zip", size)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveChunk(session.ID, 0, bytes.NewReader([]byte{1, 2, 3})); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(session.Directory, ".merged-4242.zip")
	if err := os.WriteFile(orphan, []byte{1, 2}, 0o600); err != nil {
		t.Fatal(err)
	}

	restarted := &Store{Root: root, MaxSize: 8, MaxSessions: 1, MaxTotalSize: 8, FreeSpace: unlimitedFreeSpace}
	restarted.startupPass()

	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatal("merge scratch survived the restart")
	}
	stats := restarted.Stats()
	if stats.Sessions != 1 || stats.ReservedBytes != size {
		t.Fatalf("restart lost the reservation: %+v", stats)
	}
	if stats.LastSuccess.IsZero() || stats.LastError != "" {
		t.Fatalf("startup pass did not report a clean success: %+v", stats)
	}
	// The reservation still bounds new work: the single session slot is taken.
	if _, err := restarted.Init(PurposeTheme, "b.zip", 1); !errors.Is(err, ErrQuota) {
		t.Fatalf("session accounting lost after reconciliation: %v", err)
	}
	// And the interrupted upload can still be completed from its chunks.
	finalized := false
	if _, err := restarted.Complete(session.ID, func(s Session) (Result, error) {
		data, err := os.ReadFile(s.ArchivePath)
		if err == nil && !bytes.Equal(data, []byte{1, 2, 3}) {
			err = errors.New("merged archive does not match the chunks")
		}
		finalized = true
		return Result{Message: "ok"}, err
	}); err != nil {
		t.Fatal(err)
	}
	if !finalized {
		t.Fatal("finalizer was not reached")
	}
}

// TestCleanupFailureIsVisibleAndRateLimited produces a real removal failure and
// checks that it reaches both Stats and the log exactly once, then logs recovery
// exactly once.
func TestCleanupFailureIsVisibleAndRateLimited(t *testing.T) {
	store := &Store{Root: t.TempDir(), MaxSize: 1 << 20, FreeSpace: unlimitedFreeSpace}
	session, err := store.Init(PurposeTheme, "theme.zip", 8)
	if err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(session.Directory, ".merged-77.zip")
	if err := os.WriteFile(orphan, make([]byte, 4), 0o600); err != nil {
		t.Fatal(err)
	}
	restore := blockRemoval(t, session.Directory, orphan)
	defer restore()

	logs := captureLogs(t)

	store.startupPass()
	store.startupPass()
	store.startupPass()

	stats := store.Stats()
	if stats.LastError == "" {
		t.Fatalf("cleanup failure is not exposed through Stats: %+v", stats)
	}
	if !stats.LastSuccess.IsZero() {
		t.Fatalf("a failed pass recorded a success: %+v", stats)
	}
	if failures := logs.matching("cleanup failed"); len(failures) != 1 {
		t.Fatalf("want one failure line for three identical failures, got %d: %v", len(failures), failures)
	}
	if _, err := os.Stat(orphan); err != nil {
		t.Fatalf("failed removal must leave the file alone: %v", err)
	}

	restore()
	store.startupPass()

	stats = store.Stats()
	if stats.LastError != "" {
		t.Fatalf("recovery did not clear the error: %+v", stats)
	}
	if stats.LastSuccess.IsZero() {
		t.Fatal("recovery did not record a success")
	}
	if stats.ReclaimedFiles != 1 || stats.ReclaimedBytes != 4 {
		t.Fatalf("recovery did not account for the reclaimed file: %+v", stats)
	}
	if recovered := logs.matching("cleanup recovered"); len(recovered) != 1 {
		t.Fatalf("want one recovery line, got %d: %v", len(recovered), recovered)
	}
	if failures := logs.matching("cleanup failed"); len(failures) != 1 {
		t.Fatalf("recovery added a failure line: %v", failures)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatal("orphan survived a successful pass")
	}
}

// TestFailedCleanupKeepsQuotaAccounting pins the invariant that a failed pass
// leaves the last known-good accounting untouched instead of looking like an
// empty store.
func TestFailedCleanupKeepsQuotaAccounting(t *testing.T) {
	store := &Store{
		Root:         t.TempDir(),
		MaxSize:      20,
		MaxSessions:  2,
		MaxTotalSize: 20,
		FreeSpace:    unlimitedFreeSpace,
	}
	if _, err := store.Init(PurposeTheme, "a.zip", 12); err != nil {
		t.Fatal(err)
	}
	store.expirePass()
	before := store.Stats()
	if before.Sessions != 1 || before.ReservedBytes != 12 {
		t.Fatalf("initial accounting: %+v", before)
	}
	if before.LastSuccess.IsZero() {
		t.Fatalf("successful pass did not record a success: %+v", before)
	}

	unknown := filepath.Join(store.Root, "keep-me")
	if err := os.Mkdir(unknown, 0o700); err != nil {
		t.Fatal(err)
	}
	store.expirePass()

	after := store.Stats()
	if after.LastError == "" {
		t.Fatal("cleanup failure is not exposed through Stats")
	}
	if after.Sessions != before.Sessions || after.ReservedBytes != before.ReservedBytes {
		t.Fatalf("failed cleanup changed the accounting: %+v -> %+v", before, after)
	}
	if !after.LastSuccess.Equal(before.LastSuccess) {
		t.Fatalf("failed cleanup overwrote the last success: %v -> %v", before.LastSuccess, after.LastSuccess)
	}

	if err := os.Remove(unknown); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Init(PurposeTheme, "b.zip", 9); !errors.Is(err, ErrQuota) {
		t.Fatalf("reservation lost after a failed cleanup: %v", err)
	}
	if _, err := store.Init(PurposeTheme, "b.zip", 8); err != nil {
		t.Fatalf("reservation not enforced after a failed cleanup: %v", err)
	}
}

// TestRunCleanupStartupPassAndStop checks the lifecycle entry point: it
// reconciles before the first tick and returns when the context is cancelled.
func TestRunCleanupStartupPassAndStop(t *testing.T) {
	store := &Store{Root: t.TempDir(), MaxSize: 1 << 20, FreeSpace: unlimitedFreeSpace}
	session, err := store.Init(PurposeTheme, "theme.zip", 8)
	if err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(session.Directory, ".merged-5.zip")
	if err := os.WriteFile(orphan, make([]byte, 4), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		store.RunCleanup(ctx)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("RunCleanup did not stop on context cancellation")
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatal("RunCleanup did not reconcile the scratch file")
	}
	stats := store.Stats()
	if stats.LastSuccess.IsZero() || stats.ReclaimedFiles != 1 {
		t.Fatalf("startup pass was not reported: %+v", stats)
	}
}

// captureLogs routes the process logger into memory. web/upload never calls
// logger.Setup, so utils/log falls back to slog.Default() and this capture sees
// every line the package emits.
func captureLogs(t *testing.T) *logCapture {
	t.Helper()
	previous := slog.Default()
	capture := &logCapture{}
	slog.SetDefault(slog.New(capture))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return capture
}

type logCapture struct {
	mu      sync.Mutex
	records []slog.Record
}

func (c *logCapture) Enabled(context.Context, slog.Level) bool { return true }

func (c *logCapture) Handle(_ context.Context, record slog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = append(c.records, record.Clone())
	return nil
}

func (c *logCapture) WithAttrs([]slog.Attr) slog.Handler { return c }

func (c *logCapture) WithGroup(string) slog.Handler { return c }

// matching formats the captured lines whose message contains substring.
func (c *logCapture) matching(substring string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var lines []string
	for _, record := range c.records {
		if !strings.Contains(record.Message, substring) {
			continue
		}
		line := record.Level.String() + " " + record.Message
		record.Attrs(func(attr slog.Attr) bool {
			line += " " + attr.Key + "=" + attr.Value.String()
			return true
		})
		lines = append(lines, line)
	}
	return lines
}

// canonicalUUID is a well-formed id, so validUploadID accepts it without the
// test having to generate one.
const canonicalUUID = "9f1c2f7e-3a4b-4c5d-8e6f-0a1b2c3d4e5f"

// TestAbandonedReservationDirectoryDoesNotWedgeAdmission pins the fix for a
// process killed between Init's MkdirAll and its upload.json write. The
// directory left behind has no metadata, so scan() fails on the missing file and
// refuses every later admission until its mtime passes the 24-hour TTL -- a
// crash during Init would take uploads down for a day. Reconciliation is the
// only thing that runs before the next admission, so it has to reclaim it.
func TestAbandonedReservationDirectoryDoesNotWedgeAdmission(t *testing.T) {
	root := t.TempDir()
	store := &Store{Root: root, MaxSize: 1 << 20, FreeSpace: unlimitedFreeSpace}

	abandoned := filepath.Join(root, canonicalUUID)
	if err := os.MkdirAll(abandoned, 0o755); err != nil {
		t.Fatal(err)
	}

	// The wedge itself: with the directory present no upload can start.
	if _, err := store.Init(PurposeTheme, "theme.zip", 8); err == nil {
		t.Fatal("admission succeeded despite an abandoned reservation directory")
	}

	entries, _, err := store.ReconcileOrphans()
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if entries != 1 {
		t.Fatalf("want the abandoned directory reclaimed, got %d entries", entries)
	}
	if _, err := os.Stat(abandoned); !os.IsNotExist(err) {
		t.Fatal("abandoned reservation directory survived reconciliation")
	}
	if _, err := store.Init(PurposeTheme, "theme.zip", 8); err != nil {
		t.Fatalf("admission still refused after reconciliation: %v", err)
	}
}

// TestAbandonedReservationDirectoryWithUnknownContentIsPreserved is the other
// half of that contract: reclaiming a metadata-less directory must never become
// a way to delete files this package did not write.
func TestAbandonedReservationDirectoryWithUnknownContentIsPreserved(t *testing.T) {
	root := t.TempDir()
	store := &Store{Root: root, MaxSize: 1 << 20, FreeSpace: unlimitedFreeSpace}

	abandoned := filepath.Join(root, canonicalUUID)
	if err := os.MkdirAll(abandoned, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(abandoned, "notes.txt")
	if err := os.WriteFile(keep, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := store.ReconcileOrphans(); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("unknown content must survive reconciliation: %v", err)
	}
	// It keeps failing admission, which is the documented behaviour for
	// unrecognised data -- reported, not silently deleted.
	if _, err := store.Init(PurposeTheme, "theme.zip", 8); err == nil {
		t.Fatal("admission accepted a store holding unrecognised content")
	}
}
