package maintenance

import (
	"testing"
	"time"
)

var base = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

func at(minutes int) time.Time { return base.Add(time.Duration(minutes) * time.Minute) }

// window builds a window from minute offsets relative to base.
func window(id uint, startMinute, endMinute int, clients ...string) Window {
	return Window{
		ID:      id,
		Name:    "w",
		Start:   at(startMinute),
		End:     at(endMinute),
		Clients: clients,
	}
}

// A window that has not started, or has ended, changes nothing. The "has not started" half is
// the one that matters for a scheduled reboot: suppression must not begin early.
func TestAWindowOnlyAppliesBetweenItsEdges(t *testing.T) {
	windows := []Window{window(1, 10, 20, "node-a")}

	cases := []struct {
		when    time.Time
		active  bool
		comment string
	}{
		{at(9), false, "before the window"},
		{at(10), true, "at the start, inclusive"},
		{at(15), true, "inside"},
		{at(20), false, "at the end, exclusive"},
		{at(21), false, "after the window"},
	}
	for _, tc := range cases {
		status := Evaluate(tc.when, "node-a", windows)
		if status.Active != tc.active {
			t.Errorf("%s (%s): active = %v, want %v", tc.comment, tc.when.Format("15:04"), status.Active, tc.active)
		}
	}
}

// Half-open boundaries mean two back-to-back windows leave no instant that belongs to both and
// none to neither: the end of one is the start of the next.
func TestBackToBackWindowsDoNotOverlapOrGap(t *testing.T) {
	windows := []Window{window(1, 0, 10, "node-a"), window(2, 10, 20, "node-a")}

	first := Evaluate(at(10), "node-a", windows)
	if !first.Active {
		t.Fatal("the instant at the boundary must belong to the second window, not to neither")
	}
	if first.Window == nil || first.Window.ID != 2 {
		t.Fatalf("the boundary instant belongs to window %v, want 2", first.Window)
	}
}

// A window scoped to nodes applies only to those nodes; one with no node list applies to every
// node, which is how a fleet-wide maintenance is expressed.
func TestScopeIsExplicitOrAll(t *testing.T) {
	scoped := []Window{window(1, 10, 20, "node-a")}
	if Evaluate(at(15), "node-b", scoped).Active {
		t.Error("a window scoped to one node must not cover another")
	}

	all := []Window{window(2, 10, 20)}
	for _, uuid := range []string{"node-a", "node-b", "anything"} {
		if !Evaluate(at(15), uuid, all).Active {
			t.Errorf("a window with no node list must cover %s", uuid)
		}
	}
}

// With overlapping windows the reported one is the soonest to end, so "when do alerts come back"
// has an answer that does not depend on storage order.
func TestOverlappingWindowsReportTheSoonestEnd(t *testing.T) {
	windows := []Window{window(1, 0, 60, "node-a"), window(2, 10, 20, "node-a")}

	status := Evaluate(at(15), "node-a", windows)
	if !status.Active {
		t.Fatal("expected an active window")
	}
	if status.Window == nil || status.Window.ID != 2 {
		t.Fatalf("reported window %v, want the one ending soonest (2)", status.Window)
	}
	if status.Remaining != 5*time.Minute {
		t.Fatalf("remaining = %s, want 5m", status.Remaining)
	}

	// Storage order must not change the answer.
	reversed := []Window{windows[1], windows[0]}
	if got := Evaluate(at(15), "node-a", reversed); got.Window == nil || got.Window.ID != 2 {
		t.Fatalf("with the list reversed the answer changed: %v", got.Window)
	}
}

// The deferred notification is what stops the feature from losing the alert that matters most: a
// node that went offline inside a window and is still offline when it closes must be reported.
func TestADecisionDefersToTheEndOfTheWindow(t *testing.T) {
	windows := []Window{window(1, 10, 20, "node-a")}

	decision := Decide(at(15), "node-a", windows)
	if !decision.Suppress {
		t.Fatal("a notification inside a window must be suppressed")
	}
	if decision.Window == nil || decision.Window.ID != 1 {
		t.Fatalf("the decision must name the window, got %v", decision.Window)
	}
	if !decision.DeliverAt.Equal(at(20)) {
		t.Fatalf("DeliverAt = %s, want the end of the window (%s)", decision.DeliverAt, at(20))
	}

	// Outside a window nothing is suppressed and there is nothing to defer.
	outside := Decide(at(25), "node-a", windows)
	if outside.Suppress {
		t.Fatal("a notification outside every window must not be suppressed")
	}
	if !outside.DeliverAt.IsZero() {
		t.Fatalf("DeliverAt = %s outside a window, want zero", outside.DeliverAt)
	}
}

// A deferred alert older than the deferral limit is dropped rather than delivered late: "the
// node has been offline for two days" arriving as fresh news is worse than silence.
func TestAStaleDeferralIsDropped(t *testing.T) {
	deliverAt := at(20)

	if !ShouldDeliverDeferred(at(21), deliverAt) {
		t.Error("a deferral one minute old must still be delivered")
	}
	if !ShouldDeliverDeferred(deliverAt.Add(maxDeferral), deliverAt) {
		t.Error("a deferral exactly at the limit must be delivered")
	}
	if ShouldDeliverDeferred(deliverAt.Add(maxDeferral+time.Second), deliverAt) {
		t.Error("a deferral past the limit must be dropped")
	}
	if ShouldDeliverDeferred(at(19), deliverAt) {
		t.Error("a deferral must not be delivered before its time")
	}
	if ShouldDeliverDeferred(at(30), time.Time{}) {
		t.Error("a zero deliver-at is not a deferral")
	}
}

// Sort gives storage order no say in what a reader sees first.
func TestSortOrdersByStartThenID(t *testing.T) {
	windows := []Window{
		{ID: 3, Start: at(20)},
		{ID: 1, Start: at(10)},
		{ID: 2, Start: at(10)},
	}
	Sort(windows)
	want := []uint{1, 2, 3}
	for index, id := range want {
		if windows[index].ID != id {
			t.Fatalf("sorted order = %v, want %v", windows, want)
		}
	}
}

// A window the client is not in is not a decision about that client, even while it is open for
// others.
func TestDecisionsArePerClient(t *testing.T) {
	windows := []Window{window(1, 10, 20, "node-a")}

	if decision := Decide(at(15), "node-b", windows); decision.Suppress {
		t.Fatal("a window covering another node must not suppress this node's alerts")
	}
	if decision := Decide(at(15), "node-a", windows); !decision.Suppress {
		t.Fatal("a window covering this node must suppress its alerts")
	}
}
