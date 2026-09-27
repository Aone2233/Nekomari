// Package maintenance decides whether a node is inside a maintenance window, and what to do
// with a notification that arrives while it is.
//
// Why the decision is its own package with no database in it: this is the rule that stands
// between "I scheduled a reboot" and "my phone rang at 03:00", and every part of it is a
// boundary. A window that starts a minute late, or ends a minute early, or swallows the alert
// that should have followed it, is a rule that failed in a way nobody can see from the outside.
// The definitions live here; the tests state them; the senders call them.
//
// The rule that shapes the design: **suppression is scoped to notification and never to
// collection.** The metrics keep recording through a window, because the whole point of a
// maintenance window is that the node is doing something real — a report that showed a gap in
// the charts afterwards would be lying about what happened.
package maintenance

import (
	"sort"
	"time"
)

// Window is one scheduled period in which a node's alerts are suppressed.
//
// Scoping is deliberately two-shaped rather than three: a window applies to a set of nodes, and
// the set is either explicit or "every node". An empty Clients slice means every node, which is
// how a fleet-wide maintenance is expressed and why Clients is not also used to mean "none" —
// a window that applies to nothing is a window that should be deleted, not stored.
type Window struct {
	ID      uint      `json:"id,omitempty"`
	Name    string    `json:"name"`
	Start   time.Time `json:"start"`
	End     time.Time `json:"end"`
	Clients []string  `json:"clients,omitempty"`
	Reason  string    `json:"reason,omitempty"`
}

// Covers reports whether this window applies to the given client, ignoring time.
func (w Window) Covers(clientUUID string) bool {
	if len(w.Clients) == 0 {
		return true
	}
	for _, client := range w.Clients {
		if client == clientUUID {
			return true
		}
	}
	return false
}

// Contains reports whether the window is open at `now` for the given client.
//
// The boundaries are half-open — `start <= now < end` — which is the choice worth stating. A
// closed interval would make two back-to-back windows overlap for exactly one instant, and an
// open one would leave a one-instant gap between them; either way a sample landing precisely on
// the boundary decides by luck. Half-open means the end of one window is the beginning of the
// next, with nothing in between.
func (w Window) Contains(now time.Time, clientUUID string) bool {
	if !w.Covers(clientUUID) {
		return false
	}
	now = now.UTC()
	return !now.Before(w.Start.UTC()) && now.Before(w.End.UTC())
}

// Status is what a window means for one node at one instant.
type Status struct {
	// Active is true while the window is open; the alert is suppressed.
	Active bool `json:"active"`
	// Window is the window responsible, when one is.
	Window *Window `json:"window,omitempty"`
	// Remaining is how long the window has left, zero when not active.
	Remaining time.Duration `json:"remaining,omitempty"`
}

// Evaluate returns the status of a client at `now` across every window given.
//
// The *soonest-ending* matching window is reported, not the first found. With overlapping
// windows the useful answer to "when should I expect alerts again" is the nearest end, and
// picking arbitrarily would make that answer depend on storage order.
func Evaluate(now time.Time, clientUUID string, windows []Window) Status {
	var best *Window
	for index := range windows {
		window := windows[index]
		if !window.Contains(now, clientUUID) {
			continue
		}
		if best == nil || window.End.Before(best.End) {
			candidate := window
			best = &candidate
		}
	}
	if best == nil {
		return Status{}
	}
	return Status{Active: true, Window: best, Remaining: best.End.Sub(now.UTC())}
}

// Decision is what should happen to a notification that arrived during a window.
type Decision struct {
	// Suppress is true when the notification must not be sent now.
	Suppress bool `json:"suppress"`
	// Window is the window that caused it, so the reason can be recorded.
	Window *Window `json:"window,omitempty"`
	// DeliverAt is when a suppressed notification should be sent instead. Zero when the
	// notification should simply be dropped.
	//
	// This distinction is the one the feature lives or dies on: an offline alert suppressed by
	// a window that ends in ten minutes is worth sending then, while one suppressed by a window
	// that ended an hour ago is stale and sending it would be noise about a recovery nobody
	// needs telling about.
	DeliverAt time.Time `json:"deliver_at,omitempty"`
}

// maxDeferral is how far past the end of a window a suppressed notification is still worth
// sending.
//
// A node that goes offline inside a window and is still offline when it closes should produce
// an alert then — that is exactly the case a naive implementation loses, and it is the one that
// matters most, because the maintenance did not fix it. A node that went offline and came back
// before the window closed needs no alert at all, which is what the caller checks before using
// DeliverAt: whether the condition still holds.
const maxDeferral = 30 * time.Minute

// Decide returns what to do with a notification for a client at `now`.
func Decide(now time.Time, clientUUID string, windows []Window) Decision {
	status := Evaluate(now, clientUUID, windows)
	if !status.Active {
		return Decision{}
	}
	return Decision{
		Suppress:  true,
		Window:    status.Window,
		DeliverAt: status.Window.End,
	}
}

// ShouldDeliverDeferred reports whether a notification deferred to `deliverAt` is still worth
// sending at `now`.
//
// It exists so the caller cannot get the staleness rule wrong by inlining a comparison: a
// deferred alert older than maxDeferral is dropped rather than sent late, because "the node has
// been offline for two days" arriving as a fresh notification is worse than no notification.
func ShouldDeliverDeferred(now, deliverAt time.Time) bool {
	if deliverAt.IsZero() {
		return false
	}
	now = now.UTC()
	deliverAt = deliverAt.UTC()
	if now.Before(deliverAt) {
		return false
	}
	return now.Sub(deliverAt) <= maxDeferral
}

// Sort orders windows by start time, then by id, so storage order never decides which window a
// reader sees first.
func Sort(windows []Window) {
	sort.SliceStable(windows, func(i, j int) bool {
		if !windows[i].Start.Equal(windows[j].Start) {
			return windows[i].Start.Before(windows[j].Start)
		}
		return windows[i].ID < windows[j].ID
	})
}
