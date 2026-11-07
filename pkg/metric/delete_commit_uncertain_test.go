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

func TestLostDeleteAcknowledgementStopsPendingDataFromReturning(t *testing.T) {
	cases := map[string]func(context.Context, *Store, time.Time) error{
		"metric": func(ctx context.Context, s *Store, _ time.Time) error {
			return s.DeleteMetric(ctx, "sample")
		},
		"disable and delete": func(ctx context.Context, s *Store, _ time.Time) error {
			_, err := s.SetMetricRetention(ctx, "sample", 0)
			return err
		},
		"disable only": func(ctx context.Context, s *Store, _ time.Time) error {
			_, err := s.UpdateMetricRetention(ctx, "sample", 0)
			return err
		},
		"entity": func(ctx context.Context, s *Store, _ time.Time) error {
			_, err := s.DeleteEntity(ctx, "node")
			return err
		},
		"series": func(ctx context.Context, s *Store, _ time.Time) error {
			_, err := s.DeleteSeries(ctx, Query{MetricName: "sample", EntityID: "node"})
			return err
		},
		"cutoff": func(ctx context.Context, s *Store, at time.Time) error {
			_, err := s.DeleteBefore(ctx, "sample", at.Add(2*time.Minute))
			return err
		},
		"retention cleanup": func(ctx context.Context, s *Store, at time.Time) error {
			_, err := s.CleanupExpired(ctx, at.Add(48*time.Hour))
			return err
		},
		"metric retention cleanup": func(ctx context.Context, s *Store, at time.Time) error {
			_, err := s.CompactMetric(ctx, "sample", at.Add(48*time.Hour))
			return err
		},
		"discard history": func(ctx context.Context, s *Store, _ time.Time) error {
			_, err := s.DiscardHistory(ctx, nil)
			return err
		},
	}
	for name, operation := range cases {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			var lost atomic.Bool
			db := sql.OpenDB(&lostCommitConnector{base: &sqlite3.SQLiteDriver{}, dsn: filepath.Join(t.TempDir(), "lost.db"), fail: &lost})
			t.Cleanup(func() { _ = db.Close() })
			s, err := Open(ctx, SQLite("", WithDB(db), WithMaxOpenConns(1)))
			if err != nil {
				t.Fatal(err)
			}
			if err := s.CreateMetric(ctx, Definition{Name: "sample", RetentionDays: 1}); err != nil {
				t.Fatal(err)
			}
			at := time.Now().UTC().Truncate(time.Minute).Add(5 * time.Second)
			if err := s.Write(ctx, Point{MetricName: "sample", EntityID: "node", Timestamp: at, Value: 7}); err != nil {
				t.Fatal(err)
			}
			if err := s.PersistHotRollups(ctx); err != nil {
				t.Fatal(err)
			}
			if err := s.Write(ctx, Point{MetricName: "sample", EntityID: "node", Timestamp: at.Add(time.Second), Value: 8}); err != nil {
				t.Fatal(err)
			}
			lost.Store(true)
			if err := operation(ctx, s, at); !errors.Is(err, ErrCommitUncertain) {
				t.Fatalf("lost commit acknowledgement = %v", err)
			}
			if err := s.Write(ctx, Point{MetricName: "sample", EntityID: "node", Timestamp: at.Add(2 * time.Second), Value: 9}); !errors.Is(err, ErrCommitUncertain) {
				t.Fatalf("write after uncertain deletion = %v", err)
			}
			if _, err := s.FlushCoarse(ctx, at.Add(2*time.Hour)); !errors.Is(err, ErrCommitUncertain) {
				t.Fatalf("coarse flush after uncertain deletion = %v", err)
			}
			if err := s.CloseContext(ctx); !errors.Is(err, ErrCommitUncertain) {
				t.Fatalf("close after uncertain deletion = %v", err)
			}
			if _, err := s.DiscardHistory(ctx, nil); !errors.Is(err, ErrCommitUncertain) {
				t.Fatalf("discard after uncertain deletion = %v", err)
			}
			if err := s.ReclaimSpace(ctx); !errors.Is(err, ErrCommitUncertain) {
				t.Fatalf("reclaim after uncertain deletion = %v", err)
			}
			if _, err := s.Restructure(ctx, nil); !errors.Is(err, ErrCommitUncertain) {
				t.Fatalf("restructure after uncertain deletion = %v", err)
			}
			if len(s.hot) == 0 {
				t.Fatal("uncertain outcome discarded pending hot data")
			}
		})
	}
}
