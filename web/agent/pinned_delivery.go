package agent

import (
	"context"
	"errors"
	"time"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/clientguard"
	"github.com/Aone2233/nekomari/internal/metricstore"
	"github.com/Aone2233/nekomari/internal/pingguard"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
	"github.com/Aone2233/nekomari/web/connection"
	"gorm.io/gorm"
)

// DispatchPinnedPing acquires a permit outside the task and credential fences
// because it waits for an Agent response. Delivery reloads the task under them.
func DispatchPinnedPing(ctx context.Context, uuid string, scheduled models.PingTask) error {
	db := dbcore.ReadyDBInstance()
	if db == nil {
		return errors.New("database unavailable")
	}
	if err := metricstore.PreparePinnedPingHistory(ctx, uuid, scheduled); err != nil {
		return err
	}
	lease, err := requestPinnedWSPermit(ctx, uuid)
	if err != nil {
		// A failed target-free permit request sends no probe. Record only an
		// unknown reason after rechecking task, credential and session fences.
		recordPinnedPermitFailureWith(ctx, db, uuid, scheduled, lease, pinnedDiagnostics, time.Now)
		return err
	}
	return deliverPinnedPingWith(ctx, db, uuid, scheduled, lease, func(ctx context.Context, conn *connection.SafeConn, request v2.Request) error {
		return conn.WriteJSONContext(ctx, request)
	})
}

type pinnedDeliveryWriter func(context.Context, *connection.SafeConn, v2.Request) error

// deliverPinnedPingWith orders task mutation, credential retirement and
// session replacement against delivery. A blocked interceptor can delay the
// call only until the bounded write returns; after cancellation the socket is
// aborted and the ledger entry is retired before these fences are released.
func deliverPinnedPingWith(ctx context.Context, db *gorm.DB, uuid string, scheduled models.PingTask, lease pinnedPermitLease, write pinnedDeliveryWriter) error {
	if ctx == nil || db == nil || uuid == "" || scheduled.Id == 0 || write == nil {
		return errPinnedDispatch
	}
	writeCtx, cancel := context.WithTimeout(ctx, pinnedWriteWait)
	defer cancel()
	return clientguard.With(writeCtx, uuid, func() error {
		return pingguard.With(writeCtx, scheduled.Id, func() error {
			if err := pingguard.CheckNotReserved(writeCtx, db, scheduled.Id); err != nil {
				return err
			}
			var current models.PingTask
			if err := db.WithContext(writeCtx).Take(&current, scheduled.Id).Error; err != nil {
				return err
			}
			// The callback for token retirement clears the session, but the SQL
			// row is removed before that callback when deleting a node. Require
			// the row here too, while holding its credential guard.
			var node models.Client
			if err := db.WithContext(writeCtx).Select("uuid").Where("uuid = ?", uuid).Take(&node).Error; err != nil {
				return err
			}
			spec := makePinnedDispatchSpec(current)
			if !current.AppliesToClient(uuid) || !validPinnedDispatchSpec(spec) || spec != makePinnedDispatchSpec(scheduled) {
				return errPinnedDispatch
			}
			unlock, err := lockPinnedSession(writeCtx, uuid)
			if err != nil {
				return err
			}
			defer unlock()
			if err := registerPinnedDispatchAt(uuid, lease, spec, time.Now); err != nil {
				return err
			}
			request := v2.Request{JSONRPC: v2.Version, Method: v2.MethodAgentPingFamilyProbe, Params: v2.PingFamilyProbeParams{
				TaskID: current.Id, Type: current.Type, Target: current.Target, Reference: current.Reference,
				Family: current.TargetFamily, ConfigGeneration: current.PinnedGeneration, Instance: lease.instance,
				Session: lease.session, DispatchID: lease.dispatch, FreshnessTicket: lease.ticket,
			}}
			if err := write(writeCtx, lease.conn, request); err != nil {
				cancelPinnedDispatchLocked(uuid, lease, time.Now)
				return err
			}
			if err := writeCtx.Err(); err != nil {
				cancelPinnedDispatchLocked(uuid, lease, time.Now)
				return err
			}
			return nil
		})
	})
}
