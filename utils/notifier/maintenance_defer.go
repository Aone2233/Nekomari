package notifier

import (
	"sync"
	"time"

	"github.com/Aone2233/nekomari/database/clients"
	logger "github.com/Aone2233/nekomari/utils/log"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	messageevent "github.com/Aone2233/nekomari/database/models/messageEvent"
	"github.com/Aone2233/nekomari/utils/messageSender"
)

// Deferred offline alerts.
//
// A window that suppresses an alert must not swallow it. The case that matters most is the node
// that went offline *inside* a window and is still offline when the window closes — the
// maintenance did not fix it, and silence there would be the feature's worst failure. So a
// suppressed offline alert is queued with the time it becomes deliverable, and a sweeper sends
// it once three things are true:
//
//   - the window has ended,
//   - the deferral is not stale (see maintenance.ShouldSendLater),
//   - **the node is still offline**, which is the condition the alert was about.
//
// The third check is the one a naive implementation omits, and omitting it means every
// suppressed alert is delivered late as a false alarm about a node that came back.

type deferredAlert struct {
	clientID string
	// deliverAt is the window's end, when the alert becomes deliverable.
	deliverAt time.Time
	// connectionID guards the same race the immediate path does: if the node reconnected and
	// disconnected again, this queued alert is about an older outage and is dropped.
	connectionID int64
}

var (
	deferredMu     sync.Mutex
	deferredAlerts = map[string]deferredAlert{}
)

// deferOfflineAlert queues an offline alert for after a maintenance window.
//
// One entry per client: a node that flaps inside a window produces one alert after it, not one
// per flap. The latest queued deliverAt wins, because that is the window actually covering the
// most recent outage.
func deferOfflineAlert(clientID string, deliverAt time.Time, connectionID int64) {
	if deliverAt.IsZero() {
		return
	}
	deferredMu.Lock()
	defer deferredMu.Unlock()
	existing, ok := deferredAlerts[clientID]
	if ok && existing.deliverAt.After(deliverAt) {
		deliverAt = existing.deliverAt
	}
	deferredAlerts[clientID] = deferredAlert{
		clientID:     clientID,
		deliverAt:    deliverAt.UTC(),
		connectionID: connectionID,
	}
}

// forgetDeferredAlert drops a queued alert, for a node that came back.
//
// Called from the online path: an alert about an outage that has ended is not worth sending.
func forgetDeferredAlert(clientID string) {
	deferredMu.Lock()
	delete(deferredAlerts, clientID)
	deferredMu.Unlock()
}

// forgetDeferredAlertIfCurrent drops the queued alert only while it still describes the outage the
// caller just handled.
//
// A send is in flight for as long as the sender takes, and the node can go down again under a new
// connection while it is: that newer outage queues its own entry, under the same client id.
// Deleting by client id alone would throw that entry away as if it were the delivered one — the
// same shape of bug as forgetting a re-queued alert, one level down.
func forgetDeferredAlertIfCurrent(clientID string, connectionID int64) {
	deferredMu.Lock()
	defer deferredMu.Unlock()
	current, ok := deferredAlerts[clientID]
	if !ok || current.connectionID != connectionID {
		return
	}
	delete(deferredAlerts, clientID)
}

// requeueDeferredAlert moves the queued alert for one outage to the end of the window that has
// taken the node over.
//
// Only the entry that still describes the outage the sweeper is handling is moved:
//
//   - a newer outage on the same client has queued its own entry under the same client id and has
//     to keep its own connection and its own (necessarily later) deadline;
//   - an entry the online path has already forgotten is *not* resurrected — the node came back, so
//     there is nothing left to deliver for that outage.
//
// The connection is preserved rather than refreshed from the node's current state. A node that
// reconnected leaves that state looking offline — updateOnlineState clears the pending marker, and
// isConnExist is only set by a later disconnect-then-connect pair — so borrowing the current id
// would let the sweeper send a false alarm about a node that is up.
func requeueDeferredAlert(clientID string, connectionID int64, deliverAt time.Time) {
	if deliverAt.IsZero() {
		return
	}
	deferredMu.Lock()
	defer deferredMu.Unlock()
	current, ok := deferredAlerts[clientID]
	if !ok || current.connectionID != connectionID {
		return
	}
	// No max() against the entry being replaced: the window covering the node now ends after the
	// instant the sweeper is running at, which is already past the deferral that is being moved.
	deferredAlerts[clientID] = deferredAlert{
		clientID:     clientID,
		deliverAt:    deliverAt.UTC(),
		connectionID: connectionID,
	}
}

// pendingDeferredAlerts returns a snapshot, so the sweeper does not hold the lock while it sends.
func pendingDeferredAlerts() []deferredAlert {
	deferredMu.Lock()
	defer deferredMu.Unlock()
	out := make([]deferredAlert, 0, len(deferredAlerts))
	for _, alert := range deferredAlerts {
		out = append(out, alert)
	}
	return out
}

// SweepDeferredAlerts delivers alerts that maintenance windows deferred, and is called from the
// server's scheduler every minute.
//
// On the existing scheduler rather than a goroutine of its own: there is then one place that owns
// periodic work and one place to look when something is not running.
func SweepDeferredAlerts() {
	sweepDeferredAlerts(time.Now().UTC())
}

// deliveryOutcome is what the sweeper must do with a queued entry after one delivery attempt.
type deliveryOutcome int

const (
	// alertDelivered: the sender accepted the alert, so the entry has done its job.
	alertDelivered deliveryOutcome = iota
	// alertSuperseded: the outage the alert was about is over — the node came back, or went down
	// again under a new connection — so there is nothing left to report.
	alertSuperseded
	// alertStillQueued: the alert has not been delivered and is still worth delivering later, so
	// the entry must stay in the queue. Either another window now covers the node (the alert was
	// re-queued for that window's end) or the send failed and will be retried.
	alertStillQueued
)

// sweepDeferredAlerts sends every queued alert that is due and still true.
//
// Exported to the package's tests through its own name rather than the ticker, so the rule can be
// exercised without waiting a minute.
func sweepDeferredAlerts(now time.Time) {
	for _, alert := range pendingDeferredAlerts() {
		if !maintenanceShouldSendLater(now, alert.deliverAt) {
			// Stale: the deferral outlived its usefulness, so it is dropped rather than sent as
			// fresh news about an old outage.
			if now.After(alert.deliverAt) {
				forgetDeferredAlert(alert.clientID)
			}
			continue
		}

		// The condition the alert was about. A node that came back needs no alert, and one that
		// is offline again under a new connection is a different outage with its own alert.
		state := getOrInitState(alert.clientID)
		state.mu.Lock()
		stillOffline := state.connectionID == alert.connectionID && !state.isConnExist
		state.mu.Unlock()
		if !stillOffline {
			forgetDeferredAlert(alert.clientID)
			continue
		}

		outcome, err := sendDeferredOfflineAlert(alert.clientID, now, alert.connectionID)
		if err != nil {
			logger.ErrorArgs("notifier", "Failed to send a deferred offline notification:", err)
			// Left queued: a transport failure is worth retrying, unlike a stale alert.
			continue
		}
		switch outcome {
		case alertStillQueued:
			// The entry was just written again, for the window that now covers the node. The
			// next statement used to forget it unconditionally, which deleted the entry the call
			// above had written — so a node that went offline inside window A and was still
			// offline when B began lost its alert for good.
			continue
		case alertDelivered, alertSuperseded:
			// Only the entry this sweep handled: a newer outage queued while the send was in
			// flight has its own entry and keeps it.
			forgetDeferredAlertIfCurrent(alert.clientID, alert.connectionID)
		}
	}
}

// sendDeferredOfflineAlert delivers a queued alert, or says why it is still waiting.
//
// connectionID is the outage this alert was about.
//
// The order inside is deliberate in two places.
//
// The window is re-checked before the client is read, so the re-queue decision needs no database
// read and can be exercised without one.
//
// The state is claimed — "is this still that outage?" answered and the outage marked reported in
// one critical section — *before* the send, not after it. The send is not instantaneous: a node
// that reconnects while it is in flight has to find the marker already cleared so that
// updateOnlineState reports it as a recovery. Settling after the send let that reconnect consume
// the marker mid-flight, which produced a late "offline" that was never followed by an "online".
func sendDeferredOfflineAlert(clientID string, now time.Time, connectionID int64) (deliveryOutcome, error) {
	// Suppressed is scoped to the window that caused it: if another window now covers this node,
	// the alert is deferred again rather than sent into the second window.
	//
	// This is decided before the outage is claimed below, because an alert that is re-queued has
	// still not been reported: it must keep the pending marker that makes a reconnect resolve as
	// "still offline" instead of as the recovery of an alert nobody ever received.
	if decision := maintenanceFor(now, clientID); decision.Suppress {
		requeueDeferredAlert(clientID, connectionID, decision.DeliverAt)
		return alertStillQueued, nil
	}

	client, err := clients.GetClientByUUID(clientID)
	if err != nil {
		return alertStillQueued, err
	}

	state := getOrInitState(clientID)
	state.mu.Lock()
	if state.connectionID != connectionID || state.isConnExist {
		// The node came back, or went down again under a newer connection: this alert describes
		// an outage that has been superseded, so there is nothing left to report for it.
		state.mu.Unlock()
		return alertSuperseded, nil
	}
	// The outage is being reported now, so the pending marker has done its job and is cleared
	// under the same lock that just proved it still describes this outage. isConnExist is already
	// false — that is the other half of what the guard proved — and the outage remains a fact, so
	// it is deliberately left alone.
	state.pendingOfflineSince = time.Time{}
	state.mu.Unlock()

	if err := messageSender.SendNotification(models.EventMessage{
		Event:   messageevent.Offline,
		Clients: []models.Client{client},
		Time:    now,
		Emoji:   "🔴",
	}); err != nil {
		// Still queued, so the next sweep retries. The retry asks the state whether the outage is
		// still current rather than the marker cleared above, so it does not need the marker back.
		// It is deliberately not restored: the immediate path (offline.go's grace-period
		// goroutine) also treats an alert it could not hand to the sender as reported, and one
		// rule for both paths is worth more than a distinction nobody can observe.
		return alertStillQueued, err
	}

	db := dbcore.GetDBInstance()
	if db != nil {
		if err := db.Model(&models.OfflineNotification{}).Where("client = ?", clientID).
			Update("last_notified", now).Error; err != nil {
			logger.Errorf("notifier", "Failed to update last_notified for client %s: %v", clientID, err)
		}
	}
	return alertDelivered, nil
}

// maintenanceShouldSendLater is a seam for the staleness rule, so this file does not reach into
// internal/maintenance directly and a test can drive the sweeper with a controlled clock instead of
// waiting for a tick. Replaced by the tests, restored on cleanup.
var maintenanceShouldSendLater = func(now, deliverAt time.Time) bool {
	return ShouldSendLater(now, deliverAt)
}

// maintenanceFor is a seam for the window decision, so the sweeper's rule is testable without a
// database.
var maintenanceFor = For
