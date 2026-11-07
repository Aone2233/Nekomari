package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/clientguard"
	"github.com/Aone2233/nekomari/internal/pingguard"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
	"github.com/Aone2233/nekomari/web/connection"
	"gorm.io/gorm"
)

func pinnedDeliveryFixture(t *testing.T, uuid string) (*gorm.DB, models.PingTask, pinnedPermitLease) {
	t.Helper()
	db, task, previous, _ := pinnedAdmissionFixture(t, uuid)
	cancelPinnedDispatchAt(uuid, previous, time.Now)
	if err := db.AutoMigrate(&models.Client{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.Client{UUID: uuid, Name: "fixture", Token: "fixture"}).Error; err != nil {
		t.Fatal(err)
	}
	lease, err := requestPinnedWSPermitWithWriter(t.Context(), uuid, func(_ context.Context, conn *connection.SafeConn, req v2.Request) error {
		response := permitResultForRequest(t, req)
		if !resolvePinnedPermit(uuid, conn, response.DispatchID, &response) {
			return errors.New("fixture response rejected")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return db, task, lease
}

func TestPinnedDeliveryRegistersBeforeWriteAndAcceptsResult(t *testing.T) {
	const uuid = "pinned-delivery-valid"
	db, task, lease := pinnedDeliveryFixture(t, uuid)
	var wire v2.PingFamilyProbeParams
	err := deliverPinnedPingWith(t.Context(), db, uuid, task, lease, func(ctx context.Context, conn *connection.SafeConn, req v2.Request) error {
		if _, ok := ctx.Deadline(); !ok || conn != lease.conn || req.Method != v2.MethodAgentPingFamilyProbe || req.ID != nil {
			t.Fatal("delivery did not use the bounded current socket")
		}
		wire = req.Params.(v2.PingFamilyProbeParams)
		if wire.Target != task.Target || wire.Family != "ipv6" || wire.FreshnessTicket != lease.ticket || wire.ConfigGeneration != task.PinnedGeneration {
			t.Fatalf("incorrect probe payload: %+v", wire)
		}
		mu.RLock()
		_, registered := pinnedWSSessions[uuid].dispatches[lease.dispatch]
		mu.RUnlock()
		if !registered {
			t.Fatal("probe sent before result registration")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	result := pinnedDispatchResult(lease, makePinnedDispatchSpec(task), "primary", "tcp", "ipv6")
	writes := 0
	if err := admitPinnedResultWith(t.Context(), db, uuid, lease.conn, result, func(_ context.Context, _ models.PingRecord) error { writes++; return nil }); err != nil || writes != 1 {
		t.Fatalf("delivered result: err=%v writes=%d", err, writes)
	}
}

func TestPinnedDeliveryFailsClosedOnStaleStateAndWriteError(t *testing.T) {
	for _, mode := range []string{"assignment", "generation", "target", "reference", "deleted", "reserved", "node-deleted", "replacement", "expired", "write-error"} {
		t.Run(mode, func(t *testing.T) {
			uuid := "pinned-delivery-" + mode
			db, task, lease := pinnedDeliveryFixture(t, uuid)
			switch mode {
			case "reserved":
				if err := db.Create(&models.PingTaskDeletion{TaskID: task.Id}).Error; err != nil {
					t.Fatal(err)
				}
			case "assignment":
				if err := db.Model(&task).Update("clients", models.StringArray{"another"}).Error; err != nil {
					t.Fatal(err)
				}
			case "generation":
				if err := db.Model(&models.PingTask{}).Where("id = ?", task.Id).Update("pinned_generation", pinnedTestInstanceB).Error; err != nil {
					t.Fatal(err)
				}
			case "target", "reference":
				if err := db.Model(&models.PingTask{}).Where("id = ?", task.Id).Update(mode, "changed.example").Error; err != nil {
					t.Fatal(err)
				}
			case "deleted":
				if err := db.Delete(&task).Error; err != nil {
					t.Fatal(err)
				}
			case "node-deleted":
				if err := db.Where("uuid = ?", uuid).Delete(&models.Client{}).Error; err != nil {
					t.Fatal(err)
				}
			case "replacement":
				SetConnectedClients(uuid, &connection.SafeConn{})
			case "expired":
				lease.validUntil = time.Now().Add(-time.Second)
			}
			writes := 0
			err := deliverPinnedPingWith(t.Context(), db, uuid, task, lease, func(context.Context, *connection.SafeConn, v2.Request) error {
				writes++
				return errors.New("simulated write failure")
			})
			wantWrites := 0
			if mode == "write-error" {
				wantWrites = 1
			}
			if err == nil || writes != wantWrites {
				t.Fatalf("mode=%s err=%v writes=%d", mode, err, writes)
			}
			result := pinnedDispatchResult(lease, makePinnedDispatchSpec(task), "primary", "tcp", "ipv6")
			if _, err := consumePinnedDispatchAt(uuid, lease.conn, makePinnedDispatchSpec(task), true, result, time.Now); err == nil {
				t.Fatal("undelivered probe accepted a result")
			}
		})
	}
}

func TestPinnedDeliveryRejectsRevertedAssignment(t *testing.T) {
	const uuid = "pinned-delivery-aba"
	db, task, lease := pinnedDeliveryFixture(t, uuid)
	for _, clients := range []models.StringArray{{"other"}, {uuid}} {
		generation, err := pingguard.NewGeneration()
		if err != nil {
			t.Fatal(err)
		}
		if err := pingguard.With(t.Context(), task.Id, func() error {
			return db.Model(&models.PingTask{}).Where("id = ?", task.Id).Updates(map[string]any{"clients": clients, "pinned_generation": generation}).Error
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := deliverPinnedPingWith(t.Context(), db, uuid, task, lease, func(context.Context, *connection.SafeConn, v2.Request) error {
		t.Error("stale scheduled assignment reached socket")
		return nil
	}); err == nil {
		t.Fatal("reassignment revived a stale scheduled task")
	}
}

func TestPinnedDeliveryOrdersMutationCredentialAndReplacement(t *testing.T) {
	const uuid = "pinned-delivery-order"
	db, task, lease := pinnedDeliveryFixture(t, uuid)
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- deliverPinnedPingWith(t.Context(), db, uuid, task, lease, func(ctx context.Context, _ *connection.SafeConn, _ v2.Request) error {
			close(entered)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("delivery failed before reaching the writer: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("delivery did not reach the writer")
	}
	mutation := make(chan error, 1)
	credential := make(chan error, 1)
	replaced := make(chan struct{})
	go func() { mutation <- pingguard.With(t.Context(), task.Id, func() error { return nil }) }()
	go func() { credential <- clientguard.With(t.Context(), uuid, func() error { return nil }) }()
	go func() { SetConnectedClients(uuid, &connection.SafeConn{}); close(replaced) }()
	select {
	case <-mutation:
		t.Fatal("mutation passed an in-flight delivery")
	case <-credential:
		t.Fatal("credential retirement passed an in-flight delivery")
	case <-replaced:
		t.Fatal("replacement passed an in-flight delivery")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-mutation; err != nil {
		t.Fatal(err)
	}
	if err := <-credential; err != nil {
		t.Fatal(err)
	}
	<-replaced
	result := pinnedDispatchResult(lease, makePinnedDispatchSpec(task), "primary", "tcp", "ipv6")
	if _, err := consumePinnedDispatchAt(uuid, lease.conn, makePinnedDispatchSpec(task), true, result, time.Now); err == nil {
		t.Fatal("replacement admitted an old result")
	}
}

func TestPinnedDeliveryCancellationRetiresRegistration(t *testing.T) {
	const uuid = "pinned-delivery-cancel"
	db, task, lease := pinnedDeliveryFixture(t, uuid)
	ctx, cancel := context.WithCancel(t.Context())
	err := deliverPinnedPingWith(ctx, db, uuid, task, lease, func(context.Context, *connection.SafeConn, v2.Request) error {
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled delivery = %v", err)
	}
	result := pinnedDispatchResult(lease, makePinnedDispatchSpec(task), "primary", "tcp", "ipv6")
	if _, err := consumePinnedDispatchAt(uuid, lease.conn, makePinnedDispatchSpec(task), true, result, time.Now); err == nil {
		t.Fatal("canceled delivery accepted a result")
	}
}
