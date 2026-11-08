package metric

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	sqlite3 "github.com/mattn/go-sqlite3"
)

func TestLostImportAndRebuildCommitFencesSubsequentMutations(t *testing.T) {
	policy := RollupPolicy{Tiers: []RollupTier{
		{Interval: time.Minute, Retention: 24 * time.Hour},
		{Interval: 5 * time.Minute, Retention: 7 * 24 * time.Hour},
	}}
	base := time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC)
	row := constantTransferRollup("sample", "node", base, time.Minute, 7, base)
	point := Point{MetricName: "sample", EntityID: "node", Timestamp: base, Value: 7}
	cases := map[string]struct {
		setup     func(context.Context, *Store) error
		operation func(context.Context, *Store) error
		wantRows  int
	}{
		"rollup import": {
			operation: func(ctx context.Context, s *Store) error { return s.ImportRollups(ctx, []PersistedRollup{row}) },
			wantRows:  1,
		},
		"preserving import": {
			operation: func(ctx context.Context, s *Store) error {
				return s.ImportRollupsPreservingExisting(ctx, []PersistedRollup{row})
			},
			wantRows: 1,
		},
		"representative import": {
			operation: func(ctx context.Context, s *Store) error {
				return s.ReplaceRollupPoints(ctx, time.Minute, []Point{point})
			},
			wantRows: 1,
		},
		"coarse rebuild": {
			setup:     func(ctx context.Context, s *Store) error { return s.ImportRollups(ctx, []PersistedRollup{row}) },
			operation: func(ctx context.Context, s *Store) error { return s.RebuildCoarserRollups(ctx, time.Minute) },
			wantRows:  2,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			var lost atomic.Bool
			db := sql.OpenDB(&lostCommitConnector{base: &sqlite3.SQLiteDriver{}, dsn: filepath.Join(t.TempDir(), "lost.db"), fail: &lost})
			t.Cleanup(func() { _ = db.Close() })
			s, err := Open(ctx, SQLite("", WithDB(db), WithMaxOpenConns(1), WithRollupPolicy(policy)))
			if err != nil {
				t.Fatal(err)
			}
			if err := s.CreateMetric(ctx, Definition{Name: "sample", RetentionDays: 7}); err != nil {
				t.Fatal(err)
			}
			if tc.setup != nil {
				if err := tc.setup(ctx, s); err != nil {
					t.Fatal(err)
				}
			}
			lost.Store(true)
			if err := tc.operation(ctx, s); !errors.Is(err, ErrCommitUncertain) {
				t.Fatalf("lost commit acknowledgement = %v", err)
			}
			var count int
			if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM metric_rollups").Scan(&count); err != nil || count != tc.wantRows {
				t.Fatalf("committed rows = %d, %v; want %d", count, err, tc.wantRows)
			}
			checks := map[string]func() error{
				"write":          func() error { return s.Write(ctx, point) },
				"import":         func() error { return s.ImportRollups(ctx, []PersistedRollup{row}) },
				"representative": func() error { return s.ReplaceRollupPoints(ctx, time.Minute, []Point{point}) },
				"rebuild":        func() error { return s.RebuildCoarserRollups(ctx, time.Minute) },
				"create":         func() error { return s.CreateMetric(ctx, Definition{Name: "other", RetentionDays: 1}) },
				"schema":         func() error { return s.Migrate(ctx) },
				"checkpoint":     func() error { return s.CheckpointWAL(ctx) },
				"snapshot":       func() error { return s.SnapshotSQLite(ctx, filepath.Join(t.TempDir(), "snapshot.db")) },
				"close":          func() error { return s.CloseContext(ctx) },
			}
			for operation, run := range checks {
				if err := run(); !errors.Is(err, ErrCommitUncertain) {
					t.Errorf("%s after uncertain commit = %v", operation, err)
				}
			}
		})
	}
}

func TestRebuildRejectsUnrecoveredCoarseState(t *testing.T) {
	s := newRollupStore(t, RollupPolicy{Tiers: []RollupTier{
		{Interval: time.Minute, Retention: time.Hour},
		{Interval: 5 * time.Minute, Retention: 24 * time.Hour},
	}})
	s.rollupViewMu.Lock()
	s.coarseRecoveryPending = true
	s.rollupViewMu.Unlock()
	if err := s.RebuildCoarserRollups(t.Context(), time.Minute); !errors.Is(err, ErrImportPending) {
		t.Fatalf("rebuild before coarse recovery = %v", err)
	}
}
