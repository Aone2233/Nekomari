package notifier

import (
	"testing"
	"time"
)

// The deferral rule, which is the part of maintenance windows that is easiest to get wrong and
// hardest to notice.
//
// A suppressed alert must not be swallowed. The case that matters most is the node that went
// offline *inside* a window and is still offline when it closes — the maintenance did not fix it,
// and silence there is the feature's worst failure. The case that must **not** produce an alert is
// the node that came back, because a late alert about an outage that has ended is a false alarm.
//
// Driven through the sweeper's own function with a controlled clock, so the rule is exercised
// without waiting a minute for a tick.

// withSeams replaces the two functions the sweeper reaches out of the package with, and restores
// them afterwards.
func withSeams(t *testing.T, windows func(time.Time, string) Decision) {
	t.Helper()
	oldFor, oldLater := maintenanceFor, maintenanceShouldSendLater
	t.Cleanup(func() {
		maintenanceFor, maintenanceShouldSendLater = oldFor, oldLater
	})
	if windows != nil {
		maintenanceFor = windows
	}
}

// clearDeferred empties the queue so each case starts from a known state.
//
// The cleanup empties it *inline* rather than calling clearDeferred again: registering
// clearDeferred as its own cleanup made it register another, forever. The symptom was a test
// binary that ran for the full ten-minute timeout with a stack of identical frames, which is at
// least an honest way for it to fail.
func clearDeferred(t *testing.T) {
	t.Helper()
	resetDeferred()
	t.Cleanup(resetDeferred)
}

// resetDeferred is the body, so the cleanup can use it without recursing.
func resetDeferred() {
	deferredMu.Lock()
	deferredAlerts = map[string]deferredAlert{}
	deferredMu.Unlock()
}

func queueLen() int {
	deferredMu.Lock()
	defer deferredMu.Unlock()
	return len(deferredAlerts)
}

// A suppressed alert is queued for the window's end rather than dropped.
func TestASuppressedAlertIsQueuedNotDropped(t *testing.T) {
	clearDeferred(t)
	windowEnd := time.Now().UTC().Add(10 * time.Minute)

	deferOfflineAlert("node-a", windowEnd, 7)

	if queueLen() != 1 {
		t.Fatalf("queue length = %d, want 1", queueLen())
	}
	deferredMu.Lock()
	queued := deferredAlerts["node-a"]
	deferredMu.Unlock()
	if !queued.deliverAt.Equal(windowEnd.UTC()) {
		t.Fatalf("deliverAt = %s, want %s", queued.deliverAt, windowEnd.UTC())
	}
	if queued.connectionID != 7 {
		t.Fatalf("connectionID = %d, want 7: the sweep must know which outage this was about",
			queued.connectionID)
	}
}

// One entry per client: a node that flaps inside a window produces one alert after it, not one
// per flap.
func TestRepeatedSuppressionsCollapseToOneEntry(t *testing.T) {
	clearDeferred(t)
	windowEnd := time.Now().UTC().Add(10 * time.Minute)

	deferOfflineAlert("node-a", windowEnd, 1)
	deferOfflineAlert("node-a", windowEnd.Add(time.Minute), 2)
	deferOfflineAlert("node-a", windowEnd, 3)

	if queueLen() != 1 {
		t.Fatalf("queue length = %d, want 1", queueLen())
	}
	deferredMu.Lock()
	queued := deferredAlerts["node-a"]
	deferredMu.Unlock()
	// The latest window end wins, because that is the window covering the most recent outage.
	if !queued.deliverAt.Equal(windowEnd.Add(time.Minute).UTC()) {
		t.Fatalf("deliverAt = %s, want the latest window end", queued.deliverAt)
	}
}

// A node that comes back needs no alert: the online path forgets the queued one.
func TestComingBackForgetsTheQueuedAlert(t *testing.T) {
	clearDeferred(t)
	deferOfflineAlert("node-a", time.Now().UTC().Add(time.Minute), 1)
	if queueLen() != 1 {
		t.Fatal("expected a queued alert")
	}

	forgetDeferredAlert("node-a")
	if queueLen() != 0 {
		t.Fatalf("queue length = %d, want 0: an outage that ended needs no alert", queueLen())
	}
}

// The sweeper must not send while the window is still open.
func TestTheSweeperWaitsForTheWindowToClose(t *testing.T) {
	clearDeferred(t)
	now := time.Now().UTC()
	deferOfflineAlert("node-a", now.Add(10*time.Minute), 1)

	// Stub the staleness rule to "never due yet", which is what the real one answers before the
	// window closes.
	withSeams(t, nil)
	maintenanceShouldSendLater = func(now, deliverAt time.Time) bool { return !now.Before(deliverAt) }

	sweepDeferredAlerts(now)
	if queueLen() != 1 {
		t.Fatalf("the sweeper sent an alert before the window closed (queue length %d)", queueLen())
	}
}

// A deferral past the staleness limit is dropped, not delivered late. "The node has been offline
// for two days" arriving as fresh news is worse than no notification.
func TestAStaleDeferralIsDroppedByTheSweeper(t *testing.T) {
	clearDeferred(t)
	now := time.Now().UTC()
	// Queued long ago, and the window ended long ago too.
	deferOfflineAlert("node-a", now.Add(-2*time.Hour), 1)

	withSeams(t, nil)
	// The real rule: due, but past the deferral limit.
	maintenanceShouldSendLater = func(now, deliverAt time.Time) bool {
		return !now.Before(deliverAt) && now.Sub(deliverAt) <= 30*time.Minute
	}

	sweepDeferredAlerts(now)
	if queueLen() != 0 {
		t.Fatalf("a stale deferral was left queued (length %d)", queueLen())
	}
}

// A node that reconnected and disconnected again is a different outage: the queued alert is about
// the older one and must not be sent as if it were current.
func TestTheSweeperDropsAnAlertForAnOlderConnection(t *testing.T) {
	clearDeferred(t)
	now := time.Now().UTC()
	deferOfflineAlert("node-a", now.Add(-time.Minute), 1)

	// The node is offline, but under a new connection: the state names connection 2.
	state := getOrInitState("node-a")
	state.mu.Lock()
	state.connectionID = 2
	state.isConnExist = false
	state.mu.Unlock()

	withSeams(t, nil)
	maintenanceShouldSendLater = func(now, deliverAt time.Time) bool { return true }

	sweepDeferredAlerts(now)
	if queueLen() != 0 {
		t.Fatalf("an alert about an older outage survived (length %d)", queueLen())
	}
}

// A second window that takes the node over must not cost the alert.
//
// The sweeper used to forget the entry immediately after the send call returned, and re-queueing
// happened *inside* that call: "offline inside window A, still offline, B begins before the sweep
// gets there" therefore deleted the entry that had just been written for B, and the outage was
// never reported at all.
func TestAnAlertRequeuedIntoASecondWindowIsNotForgotten(t *testing.T) {
	clearDeferred(t)
	now := time.Now().UTC()
	// Window A ended a minute ago, so the alert is due...
	deferOfflineAlert("node-a", now.Add(-time.Minute), 7)

	// ...and the node is still offline, under the connection the alert is about.
	state := getOrInitState("node-a")
	state.mu.Lock()
	state.connectionID = 7
	state.isConnExist = false
	state.pendingOfflineSince = now.Add(-5 * time.Minute)
	state.mu.Unlock()

	// ...but window B now covers it and ends in twenty minutes.
	windowBEnd := now.Add(20 * time.Minute)
	withSeams(t, func(time.Time, string) Decision {
		return Decision{Suppress: true, Reason: "maintenance window B", DeliverAt: windowBEnd}
	})
	maintenanceShouldSendLater = func(now, deliverAt time.Time) bool { return true }

	sweepDeferredAlerts(now)

	if queueLen() != 1 {
		t.Fatalf("the alert was dropped when a second window took the node over (queue length %d)",
			queueLen())
	}
	deferredMu.Lock()
	queued := deferredAlerts["node-a"]
	deferredMu.Unlock()
	if !queued.deliverAt.Equal(windowBEnd.UTC()) {
		t.Fatalf("deliverAt = %s, want window B's end %s: the alert must wait for the window that "+
			"now covers the node", queued.deliverAt, windowBEnd.UTC())
	}
	if queued.connectionID != 7 {
		t.Fatalf("connectionID = %d, want 7: re-queueing must keep the outage the alert is about, "+
			"not borrow the node's current connection", queued.connectionID)
	}

	// Nothing has been sent, so the outage is still pending: that marker is what makes a
	// reconnect resolve as "still down" rather than as the recovery of an alert nobody received.
	state.mu.Lock()
	stillPending := !state.pendingOfflineSince.IsZero()
	state.mu.Unlock()
	if !stillPending {
		t.Fatal("re-queueing cleared pendingOfflineSince: an alert that was never sent must not " +
			"look reported")
	}
}

// A node that is no longer offline needs no alert, even though the deferral is due and fresh.
func TestTheSweeperDropsAnAlertForANodeThatCameBack(t *testing.T) {
	clearDeferred(t)
	now := time.Now().UTC()
	deferOfflineAlert("node-a", now.Add(-time.Minute), 1)

	state := getOrInitState("node-a")
	state.mu.Lock()
	state.connectionID = 1
	state.isConnExist = true // it is connected
	state.mu.Unlock()

	withSeams(t, nil)
	maintenanceShouldSendLater = func(now, deliverAt time.Time) bool { return true }

	sweepDeferredAlerts(now)
	if queueLen() != 0 {
		t.Fatalf("an alert survived for a node that is connected (length %d)", queueLen())
	}
}

// Re-queueing acts on the outage the sweeper is handling and nothing else.
//
// Deleting by client id alone is what the sweeper's bug did; overwriting by client id alone would
// be the same mistake moved one call earlier. A newer outage under the same client id has to keep
// its own connection and its own later deadline, and an entry the online path already forgot must
// not be resurrected.
func TestRequeueingMovesOnlyTheOutageBeingSwept(t *testing.T) {
	clearDeferred(t)
	now := time.Now().UTC()
	newWindowEnd := now.Add(20 * time.Minute)

	// The entry being swept is moved to the end of the window that took the node over.
	deferOfflineAlert("node-swept", now.Add(-time.Minute), 7)
	requeueDeferredAlert("node-swept", 7, newWindowEnd)

	deferredMu.Lock()
	moved := deferredAlerts["node-swept"]
	deferredMu.Unlock()
	if moved.connectionID != 7 || !moved.deliverAt.Equal(newWindowEnd.UTC()) {
		t.Fatalf("the swept entry = %+v, want connection 7 due at %s", moved, newWindowEnd.UTC())
	}

	// A newer outage queued its own entry: the older one's re-queue must leave it alone.
	newerEnd := now.Add(30 * time.Minute)
	deferOfflineAlert("node-newer", newerEnd, 99)
	requeueDeferredAlert("node-newer", 7, newWindowEnd)

	deferredMu.Lock()
	newer := deferredAlerts["node-newer"]
	deferredMu.Unlock()
	if newer.connectionID != 99 || !newer.deliverAt.Equal(newerEnd.UTC()) {
		t.Fatalf("the newer outage's entry = %+v, want connection 99 due at %s: the older alert's "+
			"re-queue must not overwrite it", newer, newerEnd.UTC())
	}

	// An entry the online path has forgotten (the node came back) is not brought back to life.
	requeueDeferredAlert("node-gone", 7, newWindowEnd)
	if queueLen() != 2 {
		t.Fatalf("re-queueing resurrected a forgotten entry (queue length %d, want 2)", queueLen())
	}
}
