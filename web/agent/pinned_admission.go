package agent

import (
	"context"
	"errors"
	"time"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/metricstore"
	"github.com/Aone2233/nekomari/internal/pingguard"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
	"github.com/Aone2233/nekomari/web/connection"
	"gorm.io/gorm"
)

// AdmitPinnedResult requires the caller to hold current-credential admission
// and to verify that conn is the current socket. The dispatch ledger and task
// guard independently reject results lacking a current authorized dispatch.
func AdmitPinnedResult(ctx context.Context, uuid string, conn *connection.SafeConn, result v2.PingFamilyResultParams) error {
	db := dbcore.ReadyDBInstance()
	if db == nil {
		return errors.New("database unavailable")
	}
	return admitPinnedResultWith(ctx, db, uuid, conn, result, metricstore.WritePingRecordSync)
}

func admitPinnedResultWith(ctx context.Context, db *gorm.DB, uuid string, conn *connection.SafeConn, result v2.PingFamilyResultParams, write func(context.Context, models.PingRecord) error) error {
	if ctx == nil || db == nil || write == nil {
		return errors.New("pinned admission unavailable")
	}
	// Bound the DB and guard waits. The production writer also refuses
	// contended store locks rather than continuing after this fence is released.
	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return pingguard.With(writeCtx, result.TaskID, func() error {
		if err := pingguard.CheckNotReserved(writeCtx, db, result.TaskID); err != nil {
			return err
		}
		var task models.PingTask
		if err := db.WithContext(writeCtx).Take(&task, result.TaskID).Error; err != nil {
			return err
		}
		_, err := consumePinnedDispatchWithWriterAt(writeCtx, uuid, conn, makePinnedDispatchSpec(task), task.AppliesToClient(uuid), result, time.Now,
			func(accepted pinnedAcceptedResult) error {
				r := accepted.result
				if r.Diagnostic != "" {
					// A diagnostic is not a failed probe and must not become loss.
					pinnedDiagnostics.record(task, uuid, accepted, r.Diagnostic)
					return nil
				}
				role := ""
				if r.Role == "reference" {
					role = "reference"
				}
				err := write(writeCtx, models.PingRecord{Client: uuid, TaskId: r.TaskID,
					PingType: r.PingType, Role: role, Family: r.Family, Value: *r.Value, ValueUS: *r.ValueUS,
					Time: accepted.at.UTC()})
				status := "recorded"
				if err != nil {
					status = "write_unavailable"
				}
				pinnedDiagnostics.record(task, uuid, accepted, status)
				return err
			})
		return err
	})
}
