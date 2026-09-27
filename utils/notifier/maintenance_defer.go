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

		if err := sendDeferredOfflineAlert(alert.clientID, now); err != nil {
			logger.ErrorArgs("notifier", "Failed to send a deferred offline notification:", err)
			// Left queued: a transport failure is worth retrying, unlike a stale alert.
			continue
		}
		forgetDeferredAlert(alert.clientID)
	}
}

func sendDeferredOfflineAlert(clientID string, now time.Time) error {
	client, err := clients.GetClientByUUID(clientID)
	if err != nil {
		return err
	}
	// Suppressed is scoped to the window that caused it: if another window now covers this node,
	// the alert is deferred again rather than sent into the second window.
	if decision := maintenanceFor(now, clientID); decision.Suppress {
		deferOfflineAlert(clientID, decision.DeliverAt, currentConnectionID(clientID))
		return nil
	}
	if err := messageSender.SendNotification(models.EventMessage{
		Event:   messageevent.Offline,
		Clients: []models.Client{client},
		Time:    now,
		Emoji:   "🔴",
	}); err != nil {
		return err
	}
	db := dbcore.GetDBInstance()
	if db != nil {
		if err := db.Model(&models.OfflineNotification{}).Where("client = ?", clientID).
			Update("last_notified", now).Error; err != nil {
			logger.Errorf("notifier", "Failed to update last_notified for client %s: %v", clientID, err)
		}
	}
	return nil
}

func currentConnectionID(clientID string) int64 {
	state := getOrInitState(clientID)
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.connectionID
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
