package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/clientguard"
	"github.com/Aone2233/nekomari/internal/pingguard"
	"github.com/Aone2233/nekomari/web/connection"
)

func TestPinnedDiagnosticSnapshotCurrentAssignmentAndSession(t *testing.T) {
	const uuid = "pinned-snapshot-current"
	db, task, lease, result := pinnedAdmissionFixture(t, uuid)
	if err := db.AutoMigrate(&models.Client{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.Client{UUID: uuid, Token: "fixture"}).Error; err != nil {
		t.Fatal(err)
	}
	store := newPinnedDiagnosticStore(4, pinnedDiagnosticTTL)
	now := time.Now()
	accepted := pinnedAcceptedResult{at: now, sequence: lease.sequence, result: result}
	if !store.record(task, uuid, accepted, "recorded") {
		t.Fatal("could not seed diagnostic")
	}
	snapshot := func(at time.Time) PinnedDiagnosticSnapshot {
		t.Helper()
		got, err := snapshotPinnedDiagnosticWith(t.Context(), db, task.Id, uuid, store, func() time.Time { return at })
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	got := snapshot(now)
	if got.State != "session_active" || len(got.Legs) != 1 || got.Legs[0].Status != "recorded" ||
		got.Legs[0].At == nil || !got.Legs[0].At.Equal(now) || got.ValidUntil.After(now.Add(pinnedWSLease)) {
		t.Fatalf("snapshot=%+v", got)
	}
	// A fresh handshake alone is not execution proof. Nor may the prior
	// session's successful observation survive replacement or re-handshake.
	SetConnectedClients(uuid, &connection.SafeConn{})
	if got = snapshot(now); got.State != "unknown" || got.Legs[0].Status != "unknown" {
		t.Fatalf("replacement exposed stale result: %+v", got)
	}
	conn := GetConnectedClient(uuid)
	challenge, ok := BeginPinnedWSHandshake(uuid, conn, pinnedTestInstanceA, 1)
	if !ok || !AckPinnedWSHandshake(uuid, conn, pinnedTestInstanceA, challenge) {
		t.Fatal("replacement handshake failed")
	}
	if got = snapshot(time.Now()); got.State != "session_active" || got.Legs[0].Status != "unknown" {
		t.Fatalf("handshake implied execution or exposed stale result: %+v", got)
	}
	if got = snapshot(time.Now().Add(pinnedWSLease)); got.State != "unknown" || got.Legs[0].Status != "unknown" {
		t.Fatalf("expired session exposed result: %+v", got)
	}
	DeleteConnectedClients(uuid)
	if got = snapshot(time.Now()); got.State != "offline" || got.Legs[0].Status != "unknown" {
		t.Fatalf("offline snapshot=%+v", got)
	}
	KeepAlivePresence(uuid, 1, time.Minute)
	if got = snapshot(time.Now()); got.State != "post_only" || got.Legs[0].Status != "unknown" {
		t.Fatalf("POST-only snapshot=%+v", got)
	}
}

func TestPinnedDiagnosticSnapshotFencesTaskAndReportsContention(t *testing.T) {
	const uuid = "pinned-snapshot-fences"
	db, task, lease, result := pinnedAdmissionFixture(t, uuid)
	if err := db.AutoMigrate(&models.Client{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.Client{UUID: uuid, Token: "fixture"}).Error; err != nil {
		t.Fatal(err)
	}
	store := newPinnedDiagnosticStore(2, pinnedDiagnosticTTL)
	now := time.Now()
	if !store.record(task, uuid, pinnedAcceptedResult{at: now, sequence: lease.sequence, result: result}, "dns_error") {
		t.Fatal("could not seed diagnostic")
	}
	read := func(ctx context.Context) (PinnedDiagnosticSnapshot, error) {
		return snapshotPinnedDiagnosticWith(ctx, db, task.Id, uuid, store, time.Now)
	}
	store.mu.Lock()
	got, err := read(t.Context())
	store.mu.Unlock()
	if err != nil || got.State != "unavailable" || got.Legs[0].Status != "unknown" {
		t.Fatalf("contended snapshot=%+v err=%v", got, err)
	}
	store.dropped.Add(2)
	got, err = read(t.Context())
	if err != nil || got.DroppedUpdates != 2 || got.Legs[0].Status != "dns_error" {
		t.Fatalf("dropped updates not surfaced: %+v err=%v", got, err)
	}
	if err := db.Model(&task).Update("pinned_generation", pinnedTestInstanceB).Error; err != nil {
		t.Fatal(err)
	}
	got, err = read(t.Context())
	if err != nil || got.Legs[0].Status != "unknown" {
		t.Fatalf("old generation exposed: %+v err=%v", got, err)
	}
	if err := db.Model(&task).Update("clients", models.StringArray{"another"}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := read(t.Context()); !errors.Is(err, ErrPinnedDiagnosticNotFound) {
		t.Fatalf("removed assignment exposed: %v", err)
	}
	if err := db.Model(&task).Update("clients", models.StringArray{uuid}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("uuid = ?", uuid).Delete(&models.Client{}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := read(t.Context()); !errors.Is(err, ErrPinnedDiagnosticNotFound) {
		t.Fatalf("deleted node exposed: %v", err)
	}
}

func TestPinnedDiagnosticSnapshotOrdersGuardsAndExpiresLegs(t *testing.T) {
	const uuid = "pinned-snapshot-order"
	db, task, lease, result := pinnedAdmissionFixture(t, uuid)
	if err := db.AutoMigrate(&models.Client{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.Client{UUID: uuid, Token: "fixture"}).Error; err != nil {
		t.Fatal(err)
	}
	store := newPinnedDiagnosticStore(4, pinnedDiagnosticTTL)
	now := time.Now()
	if !store.record(task, uuid, pinnedAcceptedResult{at: now.Add(-pinnedDiagnosticTTL), sequence: lease.sequence, result: result}, "recorded") {
		t.Fatal("could not seed expired diagnostic")
	}
	got, err := snapshotPinnedDiagnosticWith(t.Context(), db, task.Id, uuid, store, func() time.Time { return now })
	if err != nil || got.Legs[0].Status != "unknown" {
		t.Fatalf("expired observation visible: %+v err=%v", got, err)
	}
	for _, hold := range []func(context.Context, func() error) error{
		func(ctx context.Context, fn func() error) error { return clientguard.With(ctx, uuid, fn) },
		func(ctx context.Context, fn func() error) error { return pingguard.With(ctx, task.Id, fn) },
		func(ctx context.Context, fn func() error) error {
			unlock, err := lockPinnedSession(ctx, uuid)
			if err != nil {
				return err
			}
			defer unlock()
			return fn()
		},
	} {
		if err := hold(t.Context(), func() error {
			ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
			defer cancel()
			_, err := snapshotPinnedDiagnosticWith(ctx, db, task.Id, uuid, store, time.Now)
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("snapshot crossed held guard: %v", err)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
}
