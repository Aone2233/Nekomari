package sla

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// The wire shape is a contract with the frontend, and it has one failure mode that every
// other test in this package is blind to: a struct field with no `json` tag marshals
// under its Go name, in PascalCase. The fixture could not catch it either, because the
// fixture's report is hand-written JSON that already matches the TypeScript types — so
// the page and the API can disagree while both suites pass.
//
// That is not hypothetical. The first version of this report shipped `Presence`,
// `Availability` and `Incident` without tags, so the live API answered
// `{"ExpectedBuckets":288,"Coverage":0.97}` while the page read `expected_buckets`. It
// was found by calling the deployed panel, which is the slowest possible way to find it.
//
// This test marshals a fully populated report and walks the result generically, so any
// untagged field anywhere in the tree fails it — including one added later.

// sampleReport is a report with every nested field populated, so nothing hides behind an
// omitempty.
func sampleReport() Report {
	return Report{
		Start:           at(0),
		End:             at(60),
		Window:          "24h",
		IntervalSeconds: 60,
		Clamped:         "test",
		Nodes: []NodeReport{{
			EntityID: "node-a",
			Presence: Presence{
				ExpectedBuckets: 60,
				ObservedBuckets: 59,
				Coverage:        59.0 / 60.0,
				FirstData:       at(0),
				LastData:        at(59),
			},
			HasReport: true,
			Gaps: []Incident{{
				Start: at(10), End: at(12), Buckets: 3,
				Duration: 2 * time.Minute, PeakLoss: 1,
			}},
			Tasks: []TaskReport{{
				TaskID: "7",
				Tags:   map[string]string{"task_id": "7"},
				Loss: Availability{
					Fraction: 0.95,
					HasData:  true,
					Buckets:  59,
					Lost:     3,
					Presence: Presence{
						ExpectedBuckets: 60,
						ObservedBuckets: 59,
						Coverage:        59.0 / 60.0,
						FirstData:       at(0),
						LastData:        at(59),
					},
					Window: 24 * time.Hour,
				},
				Latency: Latency{P50: 12.5, P95: 30.0, P99: 48.9, Buckets: 59, HasData: true},
				Outages: []Incident{{
					Start: at(20), End: at(24), Buckets: 5,
					Duration: 4 * time.Minute, PeakLoss: 1,
				}},
			}},
		}},
	}
}

// seenKeys walks a decoded JSON value and returns every object key it contains.
func seenKeys(value any, out map[string]bool) {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			out[key] = true
			seenKeys(nested, out)
		}
	case []any:
		for _, nested := range typed {
			seenKeys(nested, out)
		}
	}
}

func TestReportJSONUsesSnakeCaseKeysEverywhere(t *testing.T) {
	encoded, err := json.Marshal(sampleReport())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	keys := map[string]bool{}
	seenKeys(decoded, keys)

	// The keys the frontend reads. A missing one is as bad as a wrong one: the page
	// would silently fall back to a default or render nothing.
	want := []string{
		"start", "end", "window", "interval_seconds", "clamped",
		"nodes", "entity_id", "presence", "tasks", "reporting_gaps", "has_report",
		"expected_buckets", "observed_buckets", "coverage", "first_data", "last_data",
		"task_id", "tags", "loss", "latency", "outages",
		"fraction", "has_data", "buckets", "lost",
		"p50_ms", "p95_ms", "p99_ms",
		"Start", "End", "duration", "peak_loss",
	}
	for _, key := range want {
		if !keys[key] {
			t.Errorf("the report does not carry the key %q the frontend reads", key)
		}
	}

	// Any key that starts with an uppercase letter is a struct field marshalled under
	// its Go name, which means it is missing a tag. `Start` and `End` are the deliberate
	// exception — they match the shape the panel's existing ping records use, and the
	// frontend types declare them that way.
	for key := range keys {
		if key == "Start" || key == "End" {
			continue
		}
		if key != "" && key[0] >= 'A' && key[0] <= 'Z' {
			t.Errorf("key %q is PascalCase, so its field has no json tag", key)
		}
	}
}

// A duration field must marshal as a number of nanoseconds, not as a Go duration string:
// the frontend divides by 1e9 to render it.
func TestDurationsAreNumbers(t *testing.T) {
	encoded, err := json.Marshal(sampleReport())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	text := string(encoded)
	if strings.Contains(text, `"duration":"`) || strings.Contains(text, `"window":"24h0m0s"`) {
		t.Fatalf("a duration marshalled as a string: %s", text)
	}

	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	nodes := decoded["nodes"].([]any)
	node := nodes[0].(map[string]any)
	gaps := node["reporting_gaps"].([]any)
	gap := gaps[0].(map[string]any)
	if _, ok := gap["duration"].(float64); !ok {
		t.Fatalf("gap duration is %T, want a number", gap["duration"])
	}
}
