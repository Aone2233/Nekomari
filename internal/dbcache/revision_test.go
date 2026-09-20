package dbcache

import "testing"

// An unwatched table must not panic and must not look like it changed. The
// original implementation indexed the map directly, so any caller reaching a
// table nobody registered took the process down on the first lookup.
func TestRevisionOfUnwatchedTable(t *testing.T) {
	before := Revision("not_a_watched_table")
	if before != 0 {
		t.Fatalf("unwatched table reported revision %d, want 0", before)
	}
	InvalidateAll()
	if after := Revision("not_a_watched_table"); after != before {
		t.Fatalf("unwatched table revision moved from %d to %d", before, after)
	}
}

func TestInvalidateAllMovesWatchedTables(t *testing.T) {
	for _, table := range []string{"configs", "clients", "sessions", "users"} {
		before := Revision(table)
		InvalidateAll()
		if after := Revision(table); after == before {
			t.Fatalf("table %q did not advance on InvalidateAll", table)
		}
	}
}
