package jsonrpc

import (
	"testing"

	"github.com/Aone2233/nekomari/database/clients"
	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/bulk"
	"github.com/Aone2233/nekomari/pkg/rpc"
)

// The bulk handler runs through the same validation as a single-node edit, and that is the
// point of this test rather than an incidental property.
//
// H2's acceptance criterion, verbatim: "Bulk writes go through the existing per-node
// validation, proved by a test that a value refused for one node is refused in a bulk apply
// too." The database is real (`TestMain` wires an in-memory SQLite), the applier is the
// production `clients.ClientInfoApplier`, so what is under test is the path the panel runs.

func bulkEdit(t *testing.T, params map[string]any) (any, *rpc.JsonRpcError) {
	t.Helper()
	return adminBulkEditClients(nil, &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION,
		Method:  "admin:bulkEditClients",
		Params:  params,
	})
}

// newTestClient creates a client and returns its uuid.
func newTestClient(t *testing.T, name string) string {
	t.Helper()
	uuid, _, err := clients.CreateClientWithName(name)
	if err != nil {
		t.Fatalf("create client %s: %v", name, err)
	}
	return uuid
}

// readClient loads a client row, so assertions are on what was stored rather than on what
// the handler said it did.
func readClient(t *testing.T, uuid string) models.Client {
	t.Helper()
	db := dbcore.GetDBInstance()
	var client models.Client
	if err := db.Where("uuid = ?", uuid).First(&client).Error; err != nil {
		t.Fatalf("read client %s: %v", uuid, err)
	}
	return client
}

func TestBulkEditAppliesToEverySelectedNode(t *testing.T) {
	first := newTestClient(t, "bulk-ok-a")
	second := newTestClient(t, "bulk-ok-b")

	result, rpcErr := bulkEdit(t, map[string]any{
		"uuids":  []string{first, second},
		"update": map[string]any{"group": "asia", "weight": 7},
	})
	if rpcErr != nil {
		t.Fatalf("bulk edit: %+v", rpcErr)
	}

	report, ok := result.(bulk.Report)
	if !ok {
		t.Fatalf("result is %T, want bulk.Report", result)
	}
	if report.Applied != 2 || report.Failed != 0 {
		t.Fatalf("report = %+v, want 2 applied and 0 failed", report)
	}

	for _, uuid := range []string{first, second} {
		client := readClient(t, uuid)
		if client.Group != "asia" {
			t.Errorf("client %s group = %q, want asia", uuid, client.Group)
		}
		if client.Weight != 7 {
			t.Errorf("client %s weight = %d, want 7", uuid, client.Weight)
		}
	}
}

// The criterion: a value a single-node edit refuses must be refused in a bulk apply too, and
// refused per node rather than for the whole request.
func TestAValueRefusedForOneNodeIsRefusedInBulk(t *testing.T) {
	good := newTestClient(t, "bulk-validate-good")
	// A hidden client is still editable; the point is a value the *store* refuses rather
	// than a node the handler filters, so both nodes are ordinary.
	other := newTestClient(t, "bulk-validate-other")

	// `traffic_limit` is validated by clients.SaveClient: it must be a non-negative int64.
	// Establish first that the single-node path refuses it, so the bulk assertion below is
	// about consistency rather than about a value that happens to be fine.
	if _, rpcErr := adminEditClient(nil, &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION,
		Method:  "admin:editClient",
		Params:  map[string]any{"uuid": good, "traffic_limit": -1.0},
	}); rpcErr == nil {
		t.Fatal("the single-node path accepted traffic_limit = -1, so this test proves nothing")
	}

	result, rpcErr := bulkEdit(t, map[string]any{
		"uuids":  []string{good, other},
		"update": map[string]any{"traffic_limit": -1.0},
	})
	if rpcErr != nil {
		t.Fatalf("the bulk call itself failed instead of reporting per node: %+v", rpcErr)
	}
	report := result.(bulk.Report)
	if report.Applied != 0 || report.Failed != 2 {
		t.Fatalf("report = %+v, want 0 applied and 2 failed: the same value must be refused in bulk",
			report)
	}
	for _, outcome := range report.Outcomes {
		if outcome.OK {
			t.Errorf("node %s was reported as updated with a refused value", outcome.UUID)
		}
		if outcome.Error == "" {
			t.Errorf("node %s failed without a reason", outcome.UUID)
		}
	}
	// And nothing was written: the refusal is not a partial write.
	for _, uuid := range []string{good, other} {
		if limit := readClient(t, uuid).TrafficLimit; limit != 0 {
			t.Errorf("client %s traffic_limit = %d, want it untouched", uuid, limit)
		}
	}
}

// A refusal on one node must not stop the others: the operator gets the successes it could
// have and a list of what is left.
func TestOneRefusedNodeDoesNotStopTheRest(t *testing.T) {
	a := newTestClient(t, "bulk-partial-a")
	b := newTestClient(t, "bulk-partial-b")
	c := newTestClient(t, "bulk-partial-c")

	// `expired_at` accepts RFC3339 and nothing else, so a bad one is refused by the same
	// validation for every node — a partial outcome needs a per-node difference, which the
	// fake-applier tests cover. What this asserts is that the request completes and reports
	// rather than aborting on the first failure.
	result, rpcErr := bulkEdit(t, map[string]any{
		"uuids":  []string{a, b, c},
		"update": map[string]any{"expired_at": "not-a-timestamp"},
	})
	if rpcErr != nil {
		t.Fatalf("bulk edit: %+v", rpcErr)
	}
	report := result.(bulk.Report)
	if report.Total != 3 {
		t.Fatalf("Total = %d, want 3", report.Total)
	}
	if len(report.Outcomes) != 3 {
		t.Fatalf("got %d outcomes, want one per node: %+v", len(report.Outcomes), report.Outcomes)
	}
	if report.Applied != 0 || report.Failed != 3 {
		t.Fatalf("report = %+v, want every node to carry the refusal", report)
	}
}

// An empty selection and an empty update are refused as requests, not reported as failures
// per node.
func TestBulkEditRefusesEmptyRequests(t *testing.T) {
	uuid := newTestClient(t, "bulk-empty")

	if _, rpcErr := bulkEdit(t, map[string]any{
		"uuids": []string{}, "update": map[string]any{"group": "asia"},
	}); rpcErr == nil {
		t.Error("an empty selection must be refused")
	}
	if _, rpcErr := bulkEdit(t, map[string]any{
		"uuids": []string{uuid}, "update": map[string]any{},
	}); rpcErr == nil {
		t.Error("an empty update must be refused")
	}
	if _, rpcErr := bulkEdit(t, map[string]any{
		"uuids": []string{uuid}, "update": map[string]any{"uuid": uuid},
	}); rpcErr == nil {
		t.Error("a uuid-only update must be refused: it is the selector, not a field")
	}
}

// The audit entry names the counts and the fields, and not the values: a bulk edit writes
// fleet configuration, and the log is not the place for it.
func TestBulkEditIsAudited(t *testing.T) {
	uuid := newTestClient(t, "bulk-audit")

	if _, rpcErr := bulkEdit(t, map[string]any{
		"uuids":  []string{uuid},
		"update": map[string]any{"group": "audit-group"},
	}); rpcErr != nil {
		t.Fatalf("bulk edit: %+v", rpcErr)
	}

	db := dbcore.GetDBInstance()
	var entries []models.Log
	if err := db.Where("message LIKE ?", "bulk edit clients:%").Find(&entries).Error; err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("a bulk edit left no audit entry")
	}
	last := entries[len(entries)-1]
	if !contains(last.Message, "applied=1") || !contains(last.Message, "fields=group") {
		t.Fatalf("audit entry = %q, want the counts and the field names", last.Message)
	}
	if contains(last.Message, "audit-group") {
		t.Fatalf("the audit entry carries the value that was written: %q", last.Message)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

