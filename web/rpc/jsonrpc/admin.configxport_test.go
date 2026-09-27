package jsonrpc

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/clients"
	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/configxport"
	"github.com/Aone2233/nekomari/pkg/rpc"
)

// H4's acceptance criteria against the real database TestMain wires.
//
// The package's own tests prove the rules with a fake store; these prove that the store the panel
// actually uses obeys them — that no credential reaches the file, that a dry run's report is the
// import's report, and that importing twice changes nothing the second time.

func exportConfig(t *testing.T, includeSecrets bool) map[string]any {
	t.Helper()
	result, rpcErr := adminExportConfig(nil, &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION, Method: "admin:exportConfig",
		Params: map[string]any{"include_secrets": includeSecrets},
	})
	if rpcErr != nil {
		t.Fatalf("export: %+v", rpcErr)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return document
}

// importDocument round-trips a document through the RPC the way a caller would, so the schema check
// and the binding are part of what is tested.
func importDocument(t *testing.T, document map[string]any, dryRun bool) configxport.ImportPlan {
	t.Helper()
	method := "admin:importConfig"
	if dryRun {
		method = "admin:planConfigImport"
	}
	handler := adminImportConfig
	if dryRun {
		handler = adminPlanConfigImport
	}
	result, rpcErr := handler(nil, &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION, Method: method,
		Params: map[string]any{"document": document},
	})
	if rpcErr != nil {
		t.Fatalf("%s: %+v", method, rpcErr)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var plan configxport.ImportPlan
	if err := json.Unmarshal(encoded, &plan); err != nil {
		t.Fatalf("unmarshal plan: %v", err)
	}
	return plan
}

// seedConfig creates one of each exportable thing, with a credential on the client it must not
// leak.
func seedConfig(t *testing.T) {
	t.Helper()
	db := dbcore.GetDBInstance()
	for _, table := range []any{&models.Client{}, &models.PingTask{}, &models.MaintenanceWindow{},
		&models.OfflineNotification{}} {
		if err := db.Where("1 = 1").Delete(table).Error; err != nil {
			t.Fatalf("clear: %v", err)
		}
	}

	uuid, token, err := clients.CreateClientWithName("export-check-node")
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	if token == "" {
		t.Fatal("the created client has no token, so the secrets check would be vacuous")
	}
	if err := db.Model(&models.Client{}).Where("uuid = ?", uuid).
		Updates(map[string]any{"group": "asia", "weight": 4}).Error; err != nil {
		t.Fatalf("set client fields: %v", err)
	}
	if err := db.Create(&models.PingTask{
		Name: "export-check-task", Type: "tcp", Target: "1.1.1.1", Interval: 60,
	}).Error; err != nil {
		t.Fatalf("create task: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := db.Create(&models.MaintenanceWindow{
		Name: "export-check-window", Start: now.Add(time.Hour), End: now.Add(2 * time.Hour),
	}).Error; err != nil {
		t.Fatalf("create window: %v", err)
	}
	if err := db.Create(&models.OfflineNotification{
		Client: uuid, Enable: true, GracePeriod: 300,
	}).Error; err != nil {
		t.Fatalf("create notification: %v", err)
	}
}

// The rule the feature exists around, checked against the store the panel uses rather than a fake.
func TestExportCarriesNoCredentialByDefault(t *testing.T) {
	seedConfig(t)

	document := exportConfig(t, false)
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	text := string(encoded)

	var stored models.Client
	if err := dbcore.GetDBInstance().First(&stored).Error; err != nil {
		t.Fatalf("read the seeded client: %v", err)
	}
	if stored.Token == "" {
		t.Fatal("the seeded client has no token")
	}
	if strings.Contains(text, stored.Token) {
		t.Fatal("the default export carries the agent token")
	}
	// And the field is absent, not present-and-empty: the distinction matters to a reader deciding
	// whether the export was meant to carry it.
	if strings.Contains(text, `"token"`) {
		t.Fatal("the default export names the token field at all")
	}
	if document["secrets_included"] != false {
		t.Fatalf("secrets_included = %v, want false", document["secrets_included"])
	}

	// An explicit secrets export does carry it, and says so.
	withSecrets := exportConfig(t, true)
	encoded, err = json.Marshal(withSecrets)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(encoded), stored.Token) {
		t.Fatal("an explicit secrets export must carry the token")
	}
	if withSecrets["secrets_included"] != true {
		t.Fatalf("secrets_included = %v, want true", withSecrets["secrets_included"])
	}
}

// The three criteria in one pass, because they are the same operation seen three ways.
func TestDryRunMatchesTheImportAndASecondImportChangesNothing(t *testing.T) {
	seedConfig(t)
	document := exportConfig(t, false)

	// 1. A dry run on a panel that already holds this configuration: everything unchanged.
	dryRun := importDocument(t, document, true)
	if dryRun.Creates != 0 {
		t.Errorf("the dry run wants to create %d records on the panel they came from", dryRun.Creates)
	}
	if dryRun.Unchanged == 0 {
		t.Error("the dry run reported nothing unchanged, so it is not comparing anything")
	}

	// 2. The real import agrees with the dry run, field for field.
	applied := importDocument(t, document, false)
	if applied.Creates != dryRun.Creates || applied.Updates != dryRun.Updates ||
		applied.Unchanged != dryRun.Unchanged || applied.Removals != dryRun.Removals {
		t.Fatalf("the dry run said %+v and the import did %+v", dryRun, applied)
	}

	// 3. A second import changes nothing either, and still says so.
	second := importDocument(t, document, false)
	if second.Creates != 0 || second.Updates != 0 {
		t.Fatalf("a second import wants %d creates and %d updates", second.Creates, second.Updates)
	}
	if second.Unchanged != applied.Unchanged {
		t.Fatalf("the second import found %d unchanged, the first %d", second.Unchanged, applied.Unchanged)
	}
}

// An edit is applied: a document whose group differs from the panel's updates it, and only it.
func TestAnImportAppliesAChange(t *testing.T) {
	seedConfig(t)
	document := exportConfig(t, false)

	// Change the group in the document, the way an operator editing the file would.
	clientsOut, _ := document["clients"].([]any)
	if len(clientsOut) == 0 {
		t.Fatal("the export has no clients")
	}
	client := clientsOut[0].(map[string]any)
	client["group"] = "changed-by-import"

	plan := importDocument(t, document, true)
	if plan.Updates != 1 {
		t.Fatalf("the dry run found %d updates, want 1 (the client whose group differs)", plan.Updates)
	}
	// And it names the field, so an operator can see what would change without reading the file.
	foundField := false
	for _, change := range plan.Changes {
		if change.Kind == configxport.ChangeUpdate && change.Entity == "clients" {
			for _, field := range change.Fields {
				if field == "group" {
					foundField = true
				}
			}
		}
	}
	if !foundField {
		t.Fatalf("the update does not name the group field: %+v", plan.Changes)
	}

	if _, rpcErr := adminImportConfig(nil, &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION, Method: "admin:importConfig",
		Params: map[string]any{"document": document},
	}); rpcErr != nil {
		t.Fatalf("import: %+v", rpcErr)
	}

	var stored models.Client
	if err := dbcore.GetDBInstance().Where("uuid = ?", client["uuid"]).First(&stored).Error; err != nil {
		t.Fatalf("read back: %v", err)
	}
	if stored.Group != "changed-by-import" {
		t.Fatalf("group = %q after the import, want the document's value", stored.Group)
	}
	// The unmentioned field is untouched: only fields the document carries are compared and written.
	if stored.Weight != 4 {
		t.Fatalf("weight = %d, want 4: the import changed a field the document did not", stored.Weight)
	}
}

// A document from a future schema is refused by the RPC, not only by the parser: the refusal has to
// reach the caller.
func TestAFutureSchemaIsRefusedByTheRPC(t *testing.T) {
	document := map[string]any{"schema_version": configxport.SchemaVersion + 1}
	if _, rpcErr := adminPlanConfigImport(nil, &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION, Method: "admin:planConfigImport",
		Params: map[string]any{"document": document},
	}); rpcErr == nil {
		t.Fatal("a newer schema_version must be refused")
	}
	// A missing document is a bad request rather than an empty import that would report success.
	if _, rpcErr := adminPlanConfigImport(nil, &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION, Method: "admin:planConfigImport", Params: map[string]any{},
	}); rpcErr == nil {
		t.Fatal("a missing document must be refused")
	}
}

// A node the panel has and the document does not mention is reported and survives the import.
func TestAnImportDoesNotDeleteANodeTheDocumentOmits(t *testing.T) {
	seedConfig(t)
	document := exportConfig(t, false)

	extra := newTestClient(t, "added-after-the-export")

	plan := importDocument(t, document, true)
	if plan.Removals == 0 {
		t.Fatal("the node added after the export must be reported as not-in-the-document")
	}
	if _, rpcErr := adminImportConfig(nil, &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION, Method: "admin:importConfig",
		Params: map[string]any{"document": document},
	}); rpcErr != nil {
		t.Fatalf("import: %+v", rpcErr)
	}

	var count int64
	dbcore.GetDBInstance().Model(&models.Client{}).Where("uuid = ?", extra).Count(&count)
	if count != 1 {
		t.Fatal("the import deleted a node the document did not mention")
	}
}
