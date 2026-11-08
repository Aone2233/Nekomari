package metric

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSQLiteSourcePathPreflightChecksAliasesWithoutOpeningSource(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	path := filepath.Join(dir, "active with space.db")
	store, err := Open(ctx, SQLite(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	other := filepath.Join(dir, "other.db")
	if err := os.WriteFile(other, []byte("distinct"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckDistinctSQLiteSourcePath(ctx, store, other); err != nil {
		t.Fatalf("distinct source = %v", err)
	}
	uri := (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
	for _, source := range []string{path, path + "?_busy_timeout=10", uri, ":memory:", "file:memory?mode=memory", other + "?mode=memory", "file:/tmp/other?vfs=custom", "file:/tmp/other?immutable=1"} {
		if err := CheckDistinctSQLiteSourcePath(ctx, store, source); err == nil {
			t.Fatalf("invalid or aliased source accepted: %q", source)
		}
	}
	if runtime.GOOS != "windows" {
		for _, link := range []struct {
			name string
			make func(string, string) error
		}{{"symbolic", os.Symlink}, {"hard", os.Link}} {
			alias := filepath.Join(dir, link.name+".db")
			if err := link.make(path, alias); err != nil {
				t.Fatal(err)
			}
			if err := CheckDistinctSQLiteSourcePath(ctx, store, alias); err == nil {
				t.Fatalf("%s link was accepted", link.name)
			}
			if _, err := os.Stat(alias + "-wal"); !os.IsNotExist(err) {
				t.Fatalf("preflight opened source WAL: %v", err)
			}
		}
	}
}

func TestRequireDistinctSQLiteFilesRejectsAliases(t *testing.T) {
	dir := t.TempDir()
	open := func(t *testing.T, dsn string) *Store {
		t.Helper()
		store, err := Open(t.Context(), SQLite(dsn))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		return store
	}
	firstPath := filepath.Join(dir, "first.db")
	first, distinct := open(t, firstPath), open(t, filepath.Join(dir, "distinct.db"))
	if err := RequireDistinctSQLiteFiles(t.Context(), first, distinct); err != nil {
		t.Fatalf("distinct databases rejected: %v", err)
	}
	secondHandle := open(t, firstPath)
	if err := RequireDistinctSQLiteFiles(t.Context(), first, secondHandle); err == nil {
		t.Fatal("separate handles to the same file accepted")
	}
	for _, kind := range []string{"symlink", "hardlink"} {
		t.Run(kind, func(t *testing.T) {
			if runtime.GOOS == "windows" {
				t.Skip("link creation may require Windows privileges")
			}
			alias := filepath.Join(dir, kind+".db")
			var err error
			if kind == "symlink" {
				err = os.Symlink(firstPath, alias)
			} else {
				err = os.Link(firstPath, alias)
			}
			if err != nil {
				t.Fatal(err)
			}
			// Use a query-only handle: opening the hard link with the full Store
			// initializer may attempt to create a second, unsafe WAL file.
			db, err := sql.Open("sqlite3", "file:"+alias+"?mode=ro")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			store := &Store{cfg: Config{Driver: DriverSQLite}, db: db}
			if err := RequireDistinctSQLiteFiles(t.Context(), first, store); err == nil {
				t.Fatalf("%s alias accepted", kind)
			}
		})
	}
}

func TestRequireDistinctSQLiteFilesFailsClosed(t *testing.T) {
	dir := t.TempDir()
	first, err := Open(t.Context(), SQLite(filepath.Join(dir, "first.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	memory, err := Open(t.Context(), SQLite("file:identity-test?mode=memory&cache=shared"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = memory.Close() })
	for _, call := range []func() error{
		func() error { return RequireDistinctSQLiteFiles(nil, first, memory) },
		func() error { return RequireDistinctSQLiteFiles(t.Context(), first) },
		func() error { return RequireDistinctSQLiteFiles(t.Context(), first, nil) },
		func() error { return RequireDistinctSQLiteFiles(t.Context(), first, memory) },
	} {
		if err := call(); err == nil {
			t.Fatal("invalid file identity input accepted")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := RequireDistinctSQLiteFiles(ctx, first, memory); err == nil {
		t.Fatal("canceled identity check succeeded")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := RequireDistinctSQLiteFiles(t.Context(), first, memory); err == nil {
		t.Fatal("closed store accepted")
	}
}
