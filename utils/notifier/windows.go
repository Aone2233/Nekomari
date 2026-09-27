// Package maintenance windows, for the notifier: it answers "should this alert be sent now, and
// if not, when" using the rules in internal/maintenance.
//
// The package is split in two on purpose. `internal/maintenance` decides, with no database and
// no clock of its own, so its boundaries are testable; this file reads the windows and answers
// the notifier's question. The notifier calls one function, so there is one place where the
// suppression decision is made rather than one per event type.
package notifier

import (
	"sync"
	"time"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	corewindow "github.com/Aone2233/nekomari/internal/maintenance"
	"github.com/Aone2233/nekomari/utils/log"
)

// cacheTTL is how long a loaded window list is reused.
//
// The notifier is called on every connect and disconnect, and a window list changes when an
// operator edits it — a handful of times a month. A short cache keeps a per-connection database
// read off the hot path without making an edit take effect slowly enough that anyone notices.
// Thirty seconds is chosen against the thing it could break: an operator who creates a window
// and immediately reboots a node expects suppression, and half a minute is inside the time it
// takes to open a terminal.
const cacheTTL = 30 * time.Second

type windowCacheState struct {
	mu      sync.Mutex
	windows []corewindow.Window
	loaded  time.Time
}

var windowCacheStore windowCacheState

// load reads the windows, using the cache when it is fresh.
//
// A read failure returns the empty list rather than an error: the caller is deciding whether to
// send an alert, and "the database is unreachable" must not suppress a notification. Failing
// open is the deliberate choice — a missed maintenance suppression is an annoyance, while a
// swallowed outage alert is the thing this system exists to prevent.
func load(now time.Time) []corewindow.Window {
	windowCacheStore.mu.Lock()
	defer windowCacheStore.mu.Unlock()

	if !windowCacheStore.loaded.IsZero() && now.Sub(windowCacheStore.loaded) < cacheTTL {
		return windowCacheStore.windows
	}

	db := dbcore.GetDBInstance()
	var rows []models.MaintenanceWindow
	if db == nil {
		return windowCacheStore.windows
	}
	if err := db.Order("start asc, id asc").Find(&rows).Error; err != nil {
		logger.Errorf("maintenance", "cannot read maintenance windows, alerts are not suppressed: %v", err)
		return windowCacheStore.windows
	}

	windows := make([]corewindow.Window, 0, len(rows))
	for _, row := range rows {
		windows = append(windows, corewindow.Window{
			ID:      row.ID,
			Name:    row.Name,
			Start:   row.Start.UTC(),
			End:     row.End.UTC(),
			Clients: []string(row.Clients),
			Reason:  row.Reason,
		})
	}
	windowCacheStore.windows = windows
	windowCacheStore.loaded = now
	return windows
}

// InvalidateMaintenanceWindows drops the cache, so an edit takes effect on the next
// notification rather than up to cacheTTL later. Called by the admin handler after every write.
func InvalidateMaintenanceWindows() { Invalidate() }

// Invalidate drops the cache, so an edit takes effect on the next notification rather than up to
// cacheTTL later.
func Invalidate() {
	windowCacheStore.mu.Lock()
	windowCacheStore.loaded = time.Time{}
	windowCacheStore.mu.Unlock()
}

// Decision is what the notifier should do about an alert for one client.
type Decision struct {
	// Suppress is true when the alert must not be sent now.
	Suppress bool
	// Reason names the window, for the log line that records why an alert did not fire.
	Reason string
	// DeliverAt is when a suppressed alert becomes worth sending. Zero when dropping it.
	DeliverAt time.Time
	// StillWorthSending is set by ShouldSendLater below.
	StillWorthSending bool
}

// For returns what to do about an alert for clientUUID at `now`.
func For(now time.Time, clientUUID string) Decision {
	decision := corewindow.Decide(now.UTC(), clientUUID, load(now.UTC()))
	if !decision.Suppress {
		return Decision{}
	}
	reason := "maintenance window"
	if decision.Window != nil {
		reason = "maintenance window " + decision.Window.Name
		if decision.Window.Reason != "" {
			reason += ": " + decision.Window.Reason
		}
	}
	return Decision{Suppress: true, Reason: reason, DeliverAt: decision.DeliverAt}
}

// ShouldSendLater reports whether an alert deferred to `deliverAt` is still worth sending.
//
// The caller checks the condition as well — whether the node is still offline — because this
// only knows about time. The two together are the rule: an alert is sent after a window closes
// when the condition still holds and the deferral is not stale.
func ShouldSendLater(now, deliverAt time.Time) bool {
	return corewindow.ShouldDeliverDeferred(now.UTC(), deliverAt)
}

// ActiveFor reports whether a client is inside a window at `now`, for the UI.
func ActiveFor(now time.Time, clientUUID string) corewindow.Status {
	return corewindow.Evaluate(now.UTC(), clientUUID, load(now.UTC()))
}
