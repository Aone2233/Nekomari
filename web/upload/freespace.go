package upload

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
)

// The store reservation counts the declared payload, but the flow holds several
// copies of those bytes before the session is released, and the store root
// shares its filesystem with the primary database, its journal, the logs and
// the extracted theme/plugin trees. Admission therefore has to reserve more
// than the payload itself.
const (
	// UploadFootprintFactor is the number of declared-payload-sized files that
	// can exist on the store filesystem at once, read off the code paths in
	// chunk.go: every chunk is published under the session directory, merge
	// copies all of them into `.merged-*.zip` while they are still present
	// (two copies at the merge peak), and the backup finalizer stages a third
	// copy in ./data before the restore, while the chunks and archive.zip are
	// still there. Three is the measured peak rather than a round number: the
	// rename that publishes archive.zip replaces the merge scratch file, so
	// those two never coexist.
	UploadFootprintFactor int64 = 3

	// StagingExpansionFactor bounds extracted or restored content as a
	// multiple of the declared payload. Extraction is bounded per package
	// rather than per byte: internal/plugin and web/api/admin cap a theme or
	// plugin at 512 MiB extracted, and web/backup accepts a backup whose
	// entries expand to at most backup.MaxArchiveSize (4 GiB), the same value
	// as the largest payload this store admits.
	StagingExpansionFactor int64 = 2

	// StagingExpansionFloor keeps the budget honest for small payloads: a few
	// megabytes of ZIP can expand to the 512 MiB extraction cap, so a pure
	// factor would under-count them. The value matches maxPluginExtractedSize
	// and maxThemeExtractedSize. The one case it does not fully cover is a
	// small backup whose restore expands to the 4 GiB backup cap; that restore
	// runs on the next startup, after Complete has already released the
	// session's chunks and archive, so the reserve floor absorbs it instead.
	StagingExpansionFloor int64 = 512 << 20

	// FreeSpaceReserve is the floor left untouched on the store filesystem for
	// everything else living beside the uploads: the database and its
	// journal/WAL, logs, runtime data directories and the staged restore that
	// runs on the next startup. 1 GiB covers those writes on the supported
	// deployments while staying small next to the 8 GiB reservation budget.
	FreeSpaceReserve int64 = 1 << 30
)

// ErrNoSpace reports that the store refused an upload because the filesystem
// holding the store root cannot absorb the flow's worst-case footprint, or
// because it could not answer the free-space query at all.
var ErrNoSpace = errors.New("insufficient free disk space for upload")

// RequiredFreeSpace is the free space a payload of size bytes needs before it
// can be admitted. It is exported so operators and tests can reason about the
// budget without duplicating the formula.
func RequiredFreeSpace(size int64) int64 {
	if size < 0 {
		return FreeSpaceReserve
	}
	// Init rejects a size above MaxSize before admission, but saturate instead
	// of wrapping so an oversized value can never produce a small budget.
	if size > (math.MaxInt64-FreeSpaceReserve)/(UploadFootprintFactor+StagingExpansionFactor) {
		return math.MaxInt64
	}
	expansion := size * StagingExpansionFactor
	if expansion < StagingExpansionFloor {
		expansion = StagingExpansionFloor
	}
	return size*UploadFootprintFactor + expansion + FreeSpaceReserve
}

// freeSpace reports the bytes available to this process on the filesystem
// holding path. A nil Store.FreeSpace selects the platform query; tests inject
// a deterministic implementation so admission never depends on the host disk.
func (s *Store) freeSpace(path string) (int64, error) {
	query := s.FreeSpace
	if query == nil {
		query = platformFreeSpace
	}
	return query(path)
}

// checkFreeSpace refuses admission when the filesystem holding the store root
// cannot absorb the worst-case footprint of size bytes plus the reserve floor.
//
// The query is deliberately fail-closed: treating an unanswerable query as
// infinite space would restore exactly the unbounded behaviour this check
// exists to remove, so the refusal names the path that failed and the wrapped
// query error is kept for the operator.
func (s *Store) checkFreeSpace(size int64) error {
	probe, err := existingPath(s.Root)
	if err != nil {
		return fmt.Errorf("%w: resolve %s: %w", ErrNoSpace, s.Root, err)
	}
	available, err := s.freeSpace(probe)
	if err != nil {
		return fmt.Errorf("%w: query %s: %w", ErrNoSpace, probe, err)
	}
	needed := RequiredFreeSpace(size)
	if available < needed {
		return fmt.Errorf("%w: %d bytes available on %s, %d required for a %d byte payload",
			ErrNoSpace, available, probe, needed, size)
	}
	return nil
}

// existingPath returns path or its closest existing ancestor. Init creates the
// store root lazily, so the first admission of a fresh install has to probe a
// path that already exists; the nearest ancestor sits on the filesystem the
// store is about to be created on.
func existingPath(path string) (string, error) {
	for {
		switch _, err := os.Stat(path); {
		case err == nil:
			return path, nil
		case !os.IsNotExist(err):
			return "", err
		}
		parent := filepath.Dir(path)
		if parent == path {
			return "", fmt.Errorf("no existing ancestor for %q", path)
		}
		path = parent
	}
}
