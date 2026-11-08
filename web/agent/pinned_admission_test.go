package agent

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/pingguard"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
	"github.com/Aone2233/nekomari/web/connection"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func pinnedAdmissionFixture(t *testing.T, uuid string) (*gorm.DB, models.PingTask, pinnedPermitLease, v2.PingFamilyResultParams) {
	return pinnedAdmissionFixtureForTask(t, uuid, 7)
}

func pinnedAdmissionFixtureForTask(t *testing.T, uuid string, id uint) (*gorm.DB, models.PingTask, pinnedPermitLease, v2.PingFamilyResultParams) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", uuid)), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&models.PingTask{}, &models.PingTaskDeletion{}); err != nil {
		t.Fatal(err)
	}
	task := models.PingTask{Id: id, Name: "fixture", Target: "host.example:80", TargetFamily: "ipv6", PinnedGeneration: pinnedTestInstanceA, Type: "auto", Interval: 60, Clients: models.StringArray{uuid}}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	lease, _, now := pinnedDispatchFixture(t, uuid, "auto", false)
	spec := makePinnedDispatchSpec(task)
	mu.Lock()
	s := pinnedWSSessions[uuid]
	entry := s.dispatches[lease.dispatch]
	entry.spec = spec
	s.dispatches[lease.dispatch] = entry
	mu.Unlock()
	if now.IsZero() {
		t.Fatal("missing registration time")
	}
	return db, task, lease, pinnedDispatchResult(lease, spec, "primary", "tcp", "ipv6")
}

func TestPinnedAdmissionRejectsReservedTaskID(t *testing.T) {
	const uuid = "pinned-reserved-task"
	db, task, lease, result := pinnedAdmissionFixture(t, uuid)
	if err := db.Create(&models.PingTaskDeletion{TaskID: task.Id}).Error; err != nil {
		t.Fatal(err)
	}
	writes := 0
	err := admitPinnedResultWith(t.Context(), db, uuid, lease.conn, result, func(context.Context, models.PingRecord) error {
		writes++
		return nil
	})
	if err == nil || writes != 0 {
		t.Fatalf("reserved ID admitted a result: err=%v writes=%d", err, writes)
	}
}

func TestPinnedBlockedWriterDoesNotBlockOtherNodes(t *testing.T) {
	const uuid = "pinned-blocked-writer"
	db, task, lease, result := pinnedAdmissionFixture(t, uuid)
	otherDB, _, otherLease, otherResult := pinnedAdmissionFixtureForTask(t, "pinned-unrelated-result", 8)
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- admitPinnedResultWith(t.Context(), db, uuid, lease.conn, result, func(context.Context, models.PingRecord) error {
			close(entered)
			<-release // Model an ordinary store mutex which ignores the context.
			return nil
		})
	}()
	<-entered
	defer func() {
		close(release)
		if err := <-done; err != nil {
			t.Errorf("writer: %v", err)
		}
	}()

	otherDone := make(chan struct{})
	go func() {
		const other = "pinned-unrelated-node"
		SetConnectedClients(other, &connection.SafeConn{})
		MarkV2Client(other)
		_ = GetConnectedClients()
		ResetClient(other)
		if err := admitPinnedResultWith(t.Context(), otherDB, "pinned-unrelated-result", otherLease.conn, otherResult, func(context.Context, models.PingRecord) error { return nil }); err != nil {
			t.Errorf("unrelated result admission: %v", err)
		}
		close(otherDone)
	}()
	select {
	case <-otherDone:
	case <-time.After(time.Second):
		t.Fatal("blocked writer stalled unrelated connection state")
	}

	// The held task cannot be mutated, but a different task's guard remains free.
	if err := pingguard.With(t.Context(), task.Id+1, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if _, err := lockPinnedSession(ctx, uuid); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("same-node waiter = %v, want deadline", err)
	}
}

func TestPinnedAdmissionOrdersEverySessionRetirement(t *testing.T) {
	for _, mode := range []string{"replacement", "disconnect", "delete", "reset", "rehandshake", "expiry"} {
		t.Run(mode, func(t *testing.T) {
			uuid := "pinned-admit-retire-" + mode
			db, _, lease, result := pinnedAdmissionFixture(t, uuid)
			entered, release, admitDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			go func() {
				admitDone <- admitPinnedResultWith(t.Context(), db, uuid, lease.conn, result, func(context.Context, models.PingRecord) error {
					close(entered)
					<-release
					return nil
				})
			}()
			<-entered
			retired := make(chan struct{})
			go func() {
				switch mode {
				case "replacement":
					SetConnectedClients(uuid, &connection.SafeConn{})
				case "disconnect":
					DeleteClientConditionally(uuid, lease.conn)
				case "delete":
					DeleteConnectedClients(uuid)
				case "reset":
					ResetClient(uuid)
				case "rehandshake":
					BeginPinnedWSHandshake(uuid, lease.conn, pinnedTestInstanceA, 1)
				case "expiry":
					ackPinnedWSHandshakeAt(uuid, lease.conn, lease.instance, lease.session, func() time.Time { return time.Now().Add(pinnedWSLease) })
				}
				close(retired)
			}()
			select {
			case <-retired:
				t.Error("session retired before the admitted write finished")
			case <-time.After(30 * time.Millisecond):
			}
			close(release)
			if err := <-admitDone; err != nil {
				t.Fatal(err)
			}
			select {
			case <-retired:
			case <-time.After(time.Second):
				t.Fatal("session retirement did not finish")
			}
			if err := admitPinnedResultWith(t.Context(), db, uuid, lease.conn, result, func(context.Context, models.PingRecord) error {
				t.Error("retired result reached writer")
				return nil
			}); err == nil {
				t.Fatal("retired session accepted a result")
			}
		})
	}
}

func TestPinnedAdmissionWritesValidMeasurementOnce(t *testing.T) {
	const uuid = "pinned-admit-valid"
	db, task, lease, result := pinnedAdmissionFixture(t, uuid)
	var records []models.PingRecord
	write := func(_ context.Context, rec models.PingRecord) error { records = append(records, rec); return nil }
	if err := admitPinnedResultWith(t.Context(), db, uuid, lease.conn, result, write); err != nil {
		t.Fatal(err)
	}
	if err := admitPinnedResultWith(t.Context(), db, uuid, lease.conn, result, write); err == nil {
		t.Fatal("duplicate result was admitted")
	}
	if len(records) != 1 || records[0].TaskId != task.Id || records[0].Client != uuid || records[0].Family != "ipv6" ||
		records[0].Role != "" || records[0].Value != 2 || records[0].ValueUS != 2345 || records[0].Time.IsZero() {
		t.Fatalf("unexpected admitted record: %+v", records)
	}
}

func TestPinnedAdmissionRejectsLateOlderMeasurement(t *testing.T) {
	const uuid = "pinned-admit-late-result"
	db, task, older, oldResult := pinnedAdmissionFixture(t, uuid)
	newer, err := requestPinnedWSPermitWithWriter(t.Context(), uuid, func(_ context.Context, conn *connection.SafeConn, req v2.Request) error {
		response := permitResultForRequest(t, req)
		if !resolvePinnedPermit(uuid, conn, response.DispatchID, &response) {
			return errors.New("permit response rejected")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	spec := makePinnedDispatchSpec(task)
	var records []models.PingRecord
	write := func(_ context.Context, rec models.PingRecord) error { records = append(records, rec); return nil }
	if err := registerPinnedDispatchAt(uuid, newer, spec, time.Now); err == nil {
		t.Fatal("overlapping dispatch replaced the pending result")
	}
	if err := admitPinnedResultWith(t.Context(), db, uuid, older.conn, oldResult, write); err != nil {
		t.Fatalf("first result was starved: %v", err)
	}
	if err := registerPinnedDispatchAt(uuid, newer, spec, time.Now); err != nil {
		t.Fatal(err)
	}
	newResult := pinnedDispatchResult(newer, spec, "primary", "tcp", "ipv6")
	if err := admitPinnedResultWith(t.Context(), db, uuid, newer.conn, newResult, write); err != nil {
		t.Fatal(err)
	}
	if err := admitPinnedResultWith(t.Context(), db, uuid, older.conn, oldResult, write); err == nil {
		t.Fatal("older result was admitted after the newer result")
	}
	if len(records) != 2 {
		t.Fatalf("writer received %d records, want each result once", len(records))
	}
}

func TestPinnedAdmissionRejectsStaleTaskOrAssignment(t *testing.T) {
	for _, mode := range []string{"edit", "unassign", "delete", "legacy", "socket", "family"} {
		t.Run(mode, func(t *testing.T) {
			uuid := "pinned-admit-" + mode
			db, task, lease, result := pinnedAdmissionFixture(t, uuid)
			switch mode {
			case "edit":
				if err := db.Model(&task).Updates(map[string]any{"target": "changed.example", "pinned_generation": pinnedTestInstanceB}).Error; err != nil {
					t.Fatal(err)
				}
			case "unassign":
				if err := db.Model(&task).Update("clients", models.StringArray{"other"}).Error; err != nil {
					t.Fatal(err)
				}
			case "delete":
				if err := db.Delete(&task).Error; err != nil {
					t.Fatal(err)
				}
			case "legacy":
				if err := db.Model(&task).Update("target_family", "").Error; err != nil {
					t.Fatal(err)
				}
			case "socket":
				SetConnectedClients(uuid, nil)
			case "family":
				result.Family = "ipv4"
			}
			calls := 0
			err := admitPinnedResultWith(t.Context(), db, uuid, lease.conn, result, func(context.Context, models.PingRecord) error { calls++; return nil })
			if err == nil || calls != 0 {
				t.Fatalf("stale result: err=%v writes=%d", err, calls)
			}
		})
	}
}

func TestPinnedAdmissionRejectsRevertedConfigurationAndRecreatedTask(t *testing.T) {
	for _, mode := range []string{"edit-revert", "unassign-reassign", "delete-recreate"} {
		t.Run(mode, func(t *testing.T) {
			uuid := "pinned-admit-aba-" + mode
			db, task, lease, result := pinnedAdmissionFixture(t, uuid)
			mutate := func(updates map[string]any) {
				t.Helper()
				generation, err := pingguard.NewGeneration()
				if err != nil {
					t.Fatal(err)
				}
				updates["pinned_generation"] = generation
				if err := pingguard.With(t.Context(), task.Id, func() error { return db.Model(&task).Updates(updates).Error }); err != nil {
					t.Fatal(err)
				}
			}
			switch mode {
			case "edit-revert":
				mutate(map[string]any{"target": "other.example"})
				mutate(map[string]any{"target": task.Target})
			case "unassign-reassign":
				mutate(map[string]any{"clients": models.StringArray{"other"}})
				mutate(map[string]any{"clients": task.Clients})
			case "delete-recreate":
				if err := db.Delete(&task).Error; err != nil {
					t.Fatal(err)
				}
				var err error
				task.PinnedGeneration, err = pingguard.NewGeneration()
				if err != nil {
					t.Fatal(err)
				}
				if err := db.Create(&task).Error; err != nil {
					t.Fatalf("recreate task: %v", err)
				}
			}
			calls := 0
			err := admitPinnedResultWith(t.Context(), db, uuid, lease.conn, result, func(context.Context, models.PingRecord) error { calls++; return nil })
			if err == nil || calls != 0 {
				t.Fatalf("old result survived %s: err=%v writes=%d", mode, err, calls)
			}
		})
	}
}

func TestPinnedAdmissionNonProbeAndWriteFailureDoNotRetry(t *testing.T) {
	for _, mode := range []string{"diagnostic", "write-error"} {
		t.Run(mode, func(t *testing.T) {
			uuid := "pinned-admit-" + mode
			db, _, lease, result := pinnedAdmissionFixture(t, uuid)
			if mode == "diagnostic" {
				result.Diagnostic, result.Value, result.ValueUS, result.Family = "dns_error", nil, nil, ""
			}
			calls := 0
			write := func(context.Context, models.PingRecord) error { calls++; return errors.New("write failed") }
			err := admitPinnedResultWith(t.Context(), db, uuid, lease.conn, result, write)
			if (err == nil) != (mode == "diagnostic") || calls != map[string]int{"diagnostic": 0, "write-error": 1}[mode] {
				t.Fatalf("mode=%s err=%v writes=%d", mode, err, calls)
			}
			if err := admitPinnedResultWith(t.Context(), db, uuid, lease.conn, result, write); err == nil {
				t.Fatal("diagnostic or failed write was admitted twice")
			}
		})
	}
}

func TestPinnedAdmissionOrdersTaskMutationAndSessionRetirement(t *testing.T) {
	const uuid = "pinned-admit-ordered"
	db, task, lease, result := pinnedAdmissionFixture(t, uuid)
	entered, release := make(chan struct{}), make(chan struct{})
	admitDone := make(chan error, 1)
	go func() {
		admitDone <- admitPinnedResultWith(t.Context(), db, uuid, lease.conn, result, func(context.Context, models.PingRecord) error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	mutated, retired := make(chan error, 1), make(chan struct{})
	go func() {
		mutated <- pingguard.With(t.Context(), task.Id, func() error { return db.Model(&task).Update("target", "new.example").Error })
	}()
	go func() { ResetClient(uuid); close(retired) }()
	select {
	case err := <-mutated:
		t.Fatalf("mutation passed in-flight result: %v", err)
	case <-retired:
		t.Fatal("session retired during in-flight write")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if err := <-admitDone; err != nil {
		t.Fatal(err)
	}
	if err := <-mutated; err != nil {
		t.Fatal(err)
	}
	<-retired
	var writes atomic.Int32
	if err := admitPinnedResultWith(t.Context(), db, uuid, lease.conn, result, func(context.Context, models.PingRecord) error { writes.Add(1); return nil }); err == nil || writes.Load() != 0 {
		t.Fatal("old result survived mutation and retirement")
	}
}
