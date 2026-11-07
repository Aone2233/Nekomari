package metric

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// CheckDistinctSQLiteSourcePath is a preflight before opening a migration
// source. Opening a hard-linked SQLite file under another name can create a
// second WAL; compare its filesystem identity to the active destination first.
// This cannot prevent path replacement or another process opening either file.
func CheckDistinctSQLiteSourcePath(ctx context.Context, destination *Store, sourceDSN string) error {
	if ctx == nil || destination == nil || destination.cfg.Driver != DriverSQLite {
		return errors.New("SQLite source preflight requires an open destination")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := sqliteSourceIdentityPath(sourceDSN)
	if err != nil {
		return err
	}
	source, err := os.Stat(path)
	if err != nil || !source.Mode().IsRegular() {
		return errors.New("cannot identify SQLite source file")
	}
	dest, err := sqliteStoreFileIdentity(ctx, destination)
	if err != nil {
		return err
	}
	if os.SameFile(source, dest) {
		return errors.New("SQLite source and destination refer to the same physical file")
	}
	return nil
}

func sqliteSourceIdentityPath(dsn string) (string, error) {
	if dsn == "" {
		return "", errors.New("SQLite source must be file-backed")
	}
	path := dsn
	queryStart := strings.IndexByte(dsn, '?')
	if queryStart >= 0 {
		query, err := url.ParseQuery(dsn[queryStart+1:])
		if err != nil || strings.EqualFold(query.Get("mode"), "memory") || query.Has("vfs") || query.Has("_vfs") || query.Has("immutable") || query.Has("nolock") || query.Has("psow") {
			return "", errors.New("unsupported SQLite source URI options")
		}
	}
	if strings.HasPrefix(dsn, "file:") {
		u, err := url.Parse(dsn)
		if err != nil || u.User != nil || (u.Host != "" && u.Host != "localhost") || u.Fragment != "" {
			return "", errors.New("unsupported SQLite source URI")
		}
		path = u.Path
		if u.Opaque != "" {
			path, err = url.PathUnescape(u.Opaque)
			if err != nil {
				return "", errors.New("invalid SQLite source path")
			}
		}
		if runtime.GOOS == "windows" && len(path) >= 3 && path[0] == '/' && path[2] == ':' {
			path = path[1:]
		}
	} else if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	if path == "" || path == ":memory:" || strings.ContainsRune(path, '\x00') {
		return "", errors.New("SQLite source must be file-backed")
	}
	return filepath.FromSlash(path), nil
}

// RequireDistinctSQLiteFiles checks for physical aliases among open
// file-backed SQLite stores, including symlinks and hard links. It is only a
// preliminary check: callers must separately stop all writers and prevent
// file replacement for the whole operation. It is not an exclusive lease.
func RequireDistinctSQLiteFiles(ctx context.Context, stores ...*Store) error {
	if ctx == nil || len(stores) < 2 {
		return errors.New("distinct SQLite files require a context and at least two stores")
	}
	identities := make([]os.FileInfo, 0, len(stores))
	for _, store := range stores {
		info, err := sqliteStoreFileIdentity(ctx, store)
		if err != nil {
			return err
		}
		for _, other := range identities {
			if os.SameFile(info, other) {
				return errors.New("SQLite stores refer to the same physical file")
			}
		}
		identities = append(identities, info)
	}
	return nil
}

func sqliteStoreFileIdentity(ctx context.Context, store *Store) (os.FileInfo, error) {
	if store == nil || store.cfg.Driver != DriverSQLite || store.db == nil {
		return nil, errors.New("distinct SQLite files require open SQLite stores")
	}
	if err := store.ensureOpen(); err != nil {
		return nil, err
	}
	rows, err := store.db.QueryContext(ctx, "PRAGMA database_list")
	if err != nil {
		return nil, fmt.Errorf("inspect SQLite database identity: %w", err)
	}
	var path string
	foundMain := false
	var inspectionErr error
	for rows.Next() {
		var sequence int
		var name, file string
		if err := rows.Scan(&sequence, &name, &file); err != nil {
			inspectionErr = err
			break
		}
		switch name {
		case "main":
			if foundMain || file == "" {
				inspectionErr = errors.New("SQLite main database is not uniquely file-backed")
			}
			foundMain = true
			path = file
		case "temp":
		default:
			inspectionErr = errors.New("attached SQLite databases are not supported")
		}
		if inspectionErr != nil {
			break
		}
	}
	if inspectionErr == nil {
		inspectionErr = rows.Err()
	}
	if err := rows.Close(); inspectionErr == nil {
		inspectionErr = err
	}
	if inspectionErr != nil {
		return nil, fmt.Errorf("inspect SQLite database identity: %w", inspectionErr)
	}
	if path == "" {
		return nil, errors.New("SQLite main database is not file-backed")
	}
	info, err := os.Stat(path)
	if err != nil || info == nil || !info.Mode().IsRegular() {
		return nil, errors.New("cannot identify SQLite main database file")
	}
	return info, nil
}
