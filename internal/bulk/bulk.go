// Package bulk applies one change to many nodes, one at a time, and reports each outcome.
//
// Why per-node and not transactional: a fleet edit is not a transaction, and pretending it
// is produces the worse failure. All-or-nothing rollback means one node with a stale value
// costs the operator the fifteen that would have succeeded, and it hides *which* node is the
// problem — the operator re-runs the whole edit and hits the same node again. Applying each
// update through the same per-node validation the single-node path uses, and reporting every
// outcome, keeps one implementation of the rules and makes the odd node visible.
//
// The rules this package keeps:
//
//   - **Every uuid gets an outcome.** A node that is skipped silently is a node the
//     operator will believe was changed.
//   - **Validation is the single-node path's.** The applier is an interface because this
//     package must not have a second opinion about what a valid client update is; the same
//     `SaveClientInfo` that `admin:editClient` calls is what runs here.
//   - **The update is never mutated between nodes.** A map handed to an applier that adds a
//     key — `updated_at`, say — would leak that key into the next node's request, which is
//     the kind of bug that only shows up as "why does node 7 have node 6's timestamp".
package bulk

import (
	"fmt"
	"sort"
)

// Applier applies one update to one client. `database/clients.SaveClientInfo` satisfies it.
type Applier interface {
	SaveClientInfo(update map[string]interface{}) error
}

// Outcome is what happened to one node.
type Outcome struct {
	UUID string `json:"uuid"`
	// OK is true when the update was applied.
	OK bool `json:"ok"`
	// Error is the reason it was not, empty on success.
	Error string `json:"error,omitempty"`
}

// Report is the whole result of one bulk edit.
type Report struct {
	Applied   int       `json:"applied"`
	Failed    int       `json:"failed"`
	Total     int       `json:"total"`
	Outcomes  []Outcome `json:"outcomes"`
	// FieldNames are the fields the edit touched, echoed back so an operator can confirm
	// what was applied without re-reading the request they sent.
	FieldNames []string `json:"field_names"`
}

// Apply runs one update against many nodes and reports every outcome.
//
// The uuid list is deduplicated and ordered before work begins, so the report is stable
// between identical requests and a doubled selection cannot apply an edit twice.
//
// An empty update is refused outright rather than reported as N failures: it would write
// `updated_at` on every node in the fleet while changing nothing, which looks like success
// and is not.
func Apply(applier Applier, uuids []string, update map[string]interface{}) (Report, error) {
	if applier == nil {
		return Report{}, fmt.Errorf("no applier")
	}

	unique := make([]string, 0, len(uuids))
	seen := make(map[string]bool, len(uuids))
	for _, uuid := range uuids {
		if uuid == "" || seen[uuid] {
			continue
		}
		seen[uuid] = true
		unique = append(unique, uuid)
	}
	sort.Strings(unique)

	fields := make([]string, 0, len(update))
	for name := range update {
		// `uuid` is the selector, not a field being edited.
		if name == "uuid" {
			continue
		}
		fields = append(fields, name)
	}
	sort.Strings(fields)
	if len(fields) == 0 {
		return Report{}, fmt.Errorf("no fields to update")
	}

	report := Report{Total: len(unique), Outcomes: make([]Outcome, 0, len(unique)), FieldNames: fields}
	for _, uuid := range unique {
		// A fresh map per node: an applier that writes into the request it was given must
		// not be able to affect the next one.
		perNode := make(map[string]interface{}, len(update))
		for key, value := range update {
			perNode[key] = value
		}
		perNode["uuid"] = uuid

		if err := applier.SaveClientInfo(perNode); err != nil {
			report.Failed++
			report.Outcomes = append(report.Outcomes, Outcome{UUID: uuid, OK: false, Error: err.Error()})
			continue
		}
		report.Applied++
		report.Outcomes = append(report.Outcomes, Outcome{UUID: uuid, OK: true})
	}
	return report, nil
}

// FailedOutcomes returns just the failures, in the order they were attempted.
//
// A caller that wants to show only what went wrong should not have to filter the whole list
// itself and risk dropping a failure while doing it.
func (r Report) FailedOutcomes() []Outcome {
	failed := make([]Outcome, 0, r.Failed)
	for _, outcome := range r.Outcomes {
		if !outcome.OK {
			failed = append(failed, outcome)
		}
	}
	return failed
}
