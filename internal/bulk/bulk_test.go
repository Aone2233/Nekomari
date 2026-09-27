package bulk

import (
	"fmt"
	"testing"
)

// fakeApplier records what it was asked to do, and fails for the uuids it was told to.
type fakeApplier struct {
	calls  []map[string]interface{}
	failOn map[string]string
}

func (f *fakeApplier) SaveClientInfo(update map[string]interface{}) error {
	// Copy: the caller is expected to hand over a map it will not reuse, and a fake that
	// kept the reference would not notice if that stopped being true.
	snapshot := make(map[string]interface{}, len(update))
	for key, value := range update {
		snapshot[key] = value
	}
	f.calls = append(f.calls, snapshot)

	uuid, _ := update["uuid"].(string)
	if reason, ok := f.failOn[uuid]; ok {
		return fmt.Errorf("%s", reason)
	}
	return nil
}

func TestApplyReportsEveryOutcome(t *testing.T) {
	applier := &fakeApplier{failOn: map[string]string{"b": "name is required"}}
	report, err := Apply(applier, []string{"a", "b", "c"}, map[string]interface{}{"group": "asia"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if report.Total != 3 || report.Applied != 2 || report.Failed != 1 {
		t.Fatalf("report = %+v, want 3 total / 2 applied / 1 failed", report)
	}
	// Sorted, so the same request produces the same report and it can be diffed.
	if report.Outcomes[0].UUID != "a" || report.Outcomes[1].UUID != "b" || report.Outcomes[2].UUID != "c" {
		t.Fatalf("outcomes are not ordered: %+v", report.Outcomes)
	}
	// The failure carries the applier's own message, not a generic one.
	if report.Outcomes[1].OK || report.Outcomes[1].Error != "name is required" {
		t.Fatalf("the failure lost its reason: %+v", report.Outcomes[1])
	}
	// And the successes still succeeded: no rollback.
	if !report.Outcomes[0].OK || !report.Outcomes[2].OK {
		t.Fatalf("a failure rolled back a success: %+v", report.Outcomes)
	}
}

// A partial failure must leave the successes applied — the whole reason this is not
// transactional.
func TestOneFailureDoesNotUndoTheOthers(t *testing.T) {
	applier := &fakeApplier{failOn: map[string]string{"b": "boom"}}
	if _, err := Apply(applier, []string{"a", "b", "c"}, map[string]interface{}{"weight": 5}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(applier.calls) != 3 {
		t.Fatalf("the applier ran %d times, want 3: a failure must not stop the rest", len(applier.calls))
	}
}

// The update is never mutated between nodes. An applier that writes into the map it is
// handed must not be able to affect the next node's request — which shows up as one node
// carrying another's `updated_at`, or a key nobody asked for.
//
// The assertion is on *what the applier added*, not on the map's cleanliness: the applier's
// own writes belong in its own call. What must not happen is those keys appearing in the
// call after it, or more than once.
func TestTheUpdateIsNotSharedBetweenNodes(t *testing.T) {
	applier := &mutatingApplier{}
	if _, err := Apply(applier, []string{"a", "b", "c"}, map[string]interface{}{"group": "asia"}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	for index, call := range applier.calls {
		if call["added"] != 1 {
			t.Fatalf("call %d saw `added` = %v, want 1: the applier's own write leaked in from an earlier call",
				index, call["added"])
		}
		if call["uuid"] != []string{"a", "b", "c"}[index] {
			t.Fatalf("call %d had uuid %v, want %s", index, call["uuid"], []string{"a", "b", "c"}[index])
		}
	}
}

// mutatingApplier writes into the map it is handed, the way a careless implementation might,
// incrementing a counter so a leak is visible rather than merely present.
type mutatingApplier struct{ calls []map[string]interface{} }

func (m *mutatingApplier) SaveClientInfo(update map[string]interface{}) error {
	added := 0
	if existing, ok := update["added"].(int); ok {
		added = existing
	}
	update["added"] = added + 1
	snapshot := make(map[string]interface{}, len(update))
	for key, value := range update {
		snapshot[key] = value
	}
	m.calls = append(m.calls, snapshot)
	return nil
}

// A doubled selection must not apply an edit twice, and the report must be stable.
func TestDuplicateUUIDsAreAppliedOnce(t *testing.T) {
	applier := &fakeApplier{}
	report, err := Apply(applier, []string{"a", "b", "a", "", "b"}, map[string]interface{}{"hidden": true})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(applier.calls) != 2 {
		t.Fatalf("the applier ran %d times, want 2", len(applier.calls))
	}
	if report.Total != 2 {
		t.Fatalf("Total = %d, want 2", report.Total)
	}
}

// An empty update is refused, not reported as N failures: it would stamp `updated_at` on
// every node while changing nothing, which reads as success.
func TestAnEmptyUpdateIsRefused(t *testing.T) {
	applier := &fakeApplier{}
	if _, err := Apply(applier, []string{"a", "b"}, map[string]interface{}{}); err == nil {
		t.Fatal("an empty update must be refused")
	}
	// `uuid` alone is also empty: it is the selector, not a field.
	if _, err := Apply(applier, []string{"a"}, map[string]interface{}{"uuid": "a"}); err == nil {
		t.Fatal("a uuid-only update must be refused")
	}
	if len(applier.calls) != 0 {
		t.Fatalf("the applier ran %d times for an empty update", len(applier.calls))
	}
}

// No nodes is an empty report rather than an error: selecting nothing and pressing apply is
// a no-op, and an error would make the UI show a failure for it.
func TestNoSelectionIsAnEmptyReport(t *testing.T) {
	report, err := Apply(&fakeApplier{}, nil, map[string]interface{}{"group": "asia"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if report.Total != 0 || report.Applied != 0 || len(report.Outcomes) != 0 {
		t.Fatalf("report = %+v, want empty", report)
	}
}

// The reported field names let an operator confirm what was applied without re-reading the
// request, and `uuid` is not one of them.
func TestFieldNamesExcludeTheSelector(t *testing.T) {
	report, err := Apply(&fakeApplier{}, []string{"a"}, map[string]interface{}{
		"uuid": "a", "weight": 3, "group": "asia",
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(report.FieldNames) != 2 || report.FieldNames[0] != "group" || report.FieldNames[1] != "weight" {
		t.Fatalf("FieldNames = %v, want [group weight]", report.FieldNames)
	}
}

// FailedOutcomes is what a UI shows, so it must not drop a failure while filtering.
func TestFailedOutcomesReturnsOnlyFailures(t *testing.T) {
	applier := &fakeApplier{failOn: map[string]string{"a": "x", "c": "y"}}
	report, err := Apply(applier, []string{"a", "b", "c"}, map[string]interface{}{"group": "asia"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	failed := report.FailedOutcomes()
	if len(failed) != 2 || failed[0].UUID != "a" || failed[1].UUID != "c" {
		t.Fatalf("failed = %+v, want a and c", failed)
	}
	if report.FailedOutcomes() == nil {
		t.Fatal("FailedOutcomes must return an empty slice, not nil, so callers can range over it")
	}
}
