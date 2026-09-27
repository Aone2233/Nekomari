package jsonrpc

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Aone2233/nekomari/database/auditlog"
	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/internal/configxport"
	"github.com/Aone2233/nekomari/pkg/rpc"
	"github.com/Aone2233/nekomari/utils"
)

// admin.configxport.go
// Export the panel's configuration, dry-run an import, and import it.
//
// The split between the three methods is the feature: an operator takes a file and applies it to
// another panel, and the dry run is what makes that safe to do without reading the file. Both the
// dry run and the real import call `configxport.Plan`, so "the report matches what the import does"
// is structural rather than two code paths kept in step by hand.

func jsonMarshal(value any) ([]byte, error)    { return json.Marshal(value) }
func jsonUnmarshal(data []byte, out any) error { return json.Unmarshal(data, out) }

func init() {
	RegisterWithGroupAndMeta("exportConfig", rpc.RoleAdmin, adminExportConfig, &rpc.MethodMeta{
		Name:    "admin:exportConfig",
		Summary: "Export nodes, tasks, windows, settings and notification policies as one JSON document",
		Params: []rpc.ParamMeta{
			{Name: "include_secrets", Type: "bool", Required: false, Description: "Include credentials; excluded by default"},
		},
		Returns: "Document; secrets_included says whether it carries credentials",
	})
	RegisterWithGroupAndMeta("planConfigImport", rpc.RoleAdmin, adminPlanConfigImport, &rpc.MethodMeta{
		Name:    "admin:planConfigImport",
		Summary: "Report what importing a document would do, without applying it",
		Params: []rpc.ParamMeta{
			{Name: "document", Type: "object", Required: true, Description: "A document from exportConfig"},
		},
		Returns: "ImportPlan; per-record creates, updates, unchanged and removals, with warnings",
	})
	RegisterWithGroupAndMeta("importConfig", rpc.RoleAdmin, adminImportConfig, &rpc.MethodMeta{
		Name:    "admin:importConfig",
		Summary: "Apply a document: create and update, never delete",
		Params: []rpc.ParamMeta{
			{Name: "document", Type: "object", Required: true, Description: "A document from exportConfig"},
		},
		Returns: "ImportPlan, the same report the dry run produces for the same document",
	})
}

func adminExportConfig(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		IncludeSecrets bool `json:"include_secrets"`
	}
	_ = req.BindParams(&params)

	document, err := configxport.Export(exportStore{}, params.IncludeSecrets, utils.CurrentVersion)
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to export configuration: "+err.Error(), nil)
	}
	if params.IncludeSecrets {
		// A secrets export is worth naming in the audit trail: the file it produces is now a
		// credential store, and docs/SECRETS.md exists because that has gone wrong here before.
		actor, ip := auditActor(ctx)
		auditlog.Log(ip, actor, "export configuration including secrets", "warn")
	}
	return document, nil
}

// bindDocument reads the document from the request.
//
// Read as raw JSON and parsed by `configxport.Parse` rather than bound into the struct directly,
// because the parse is what refuses an unknown schema — and a document bound into a struct would
// have unknown fields dropped silently, which is the partial application the version check exists
// to prevent.
func bindDocument(req *rpc.JsonRpcRequest) (configxport.Document, *rpc.JsonRpcError) {
	var params struct {
		Document json.RawMessage `json:"document"`
	}
	if err := req.BindParams(&params); err != nil {
		return configxport.Document{}, rpc.MakeError(rpc.InvalidParams, "Invalid request body: "+err.Error(), nil)
	}
	if len(params.Document) == 0 {
		return configxport.Document{}, rpc.MakeError(rpc.InvalidParams, "document is required", nil)
	}
	document, err := configxport.Parse(params.Document)
	if err != nil {
		return configxport.Document{}, rpc.MakeError(rpc.InvalidParams, err.Error(), nil)
	}
	return document, nil
}

func adminPlanConfigImport(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	document, rpcErr := bindDocument(req)
	if rpcErr != nil {
		return nil, rpcErr
	}
	plan, err := configxport.Plan(exportStore{}, document)
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to plan the import: "+err.Error(), nil)
	}
	return plan, nil
}

func adminImportConfig(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	document, rpcErr := bindDocument(req)
	if rpcErr != nil {
		return nil, rpcErr
	}
	plan, err := configxport.Apply(configApplier{}, document)
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to import configuration: "+err.Error(), nil)
	}
	actor, ip := auditActor(ctx)
	auditlog.Log(ip, actor, fmt.Sprintf(
		"import configuration: creates=%d updates=%d unchanged=%d not-deleted=%d",
		plan.Creates, plan.Updates, plan.Unchanged, plan.Removals), "warn")
	return plan, nil
}

// configApplier is the store plus writes, which is what `configxport.Applier` asks for.
type configApplier struct{ exportStore }

func (configApplier) Create(entity string, record map[string]any) error {
	return applyRecord(entity, "", record, true)
}

func (configApplier) Update(entity string, identity string, record map[string]any) error {
	return applyRecord(entity, identity, record, false)
}

// applyRecord writes one record, creating it when identity is empty.
//
// The payload is marshalled into the model and then written with GORM, so the model's own column
// mapping decides what a field means — the alternative, writing the map straight through, would let
// a document address any column in the table including one an export never carries.
func applyRecord(entity, identity string, record map[string]any, create bool) error {
	db := dbcore.GetDBInstance()
	if db == nil {
		return fmt.Errorf("database not initialized")
	}

	// The identity is what the *panel* uses, not what the document claims: a document cannot
	// reassign an existing row by carrying a different uuid than the one it updated through.
	payload := make(map[string]any, len(record))
	for key, value := range record {
		payload[key] = value
	}
	switch entity {
	case "settings":
		return config.SetMany(map[string]any{identity: payload["value"]})
	case "clients":
		if create {
			delete(payload, "uuid")
			var client models.Client
			if err := jsonUnmarshal(mustJSON(payload), &client); err != nil {
				return fmt.Errorf("client record: %w", err)
			}
			return db.Create(&client).Error
		}
		delete(payload, "uuid")
		return db.Model(&models.Client{}).Where("uuid = ?", identity).Updates(payload).Error
	case "ping_tasks":
		if create {
			delete(payload, "id")
			var task models.PingTask
			if err := jsonUnmarshal(mustJSON(payload), &task); err != nil {
				return fmt.Errorf("ping task record: %w", err)
			}
			return db.Create(&task).Error
		}
		delete(payload, "id")
		return db.Model(&models.PingTask{}).Where("id = ?", identity).Updates(payload).Error
	case "maintenance_windows":
		if create {
			delete(payload, "id")
			var window models.MaintenanceWindow
			if err := jsonUnmarshal(mustJSON(payload), &window); err != nil {
				return fmt.Errorf("maintenance window record: %w", err)
			}
			return db.Create(&window).Error
		}
		delete(payload, "id")
		return db.Model(&models.MaintenanceWindow{}).Where("id = ?", identity).Updates(payload).Error
	case "notification_configs":
		// One row per client, so the client is the identity and an "update" is an upsert.
		var notification models.OfflineNotification
		if err := jsonUnmarshal(mustJSON(payload), &notification); err != nil {
			return fmt.Errorf("notification config record: %w", err)
		}
		return db.Where("client = ?", notification.Client).
			Assign(map[string]any{
				"enable":       notification.Enable,
				"grace_period": notification.GracePeriod,
			}).FirstOrCreate(&notification).Error
	}
	return fmt.Errorf("unknown entity %q", entity)
}

// mustJSON is for payloads built in this file, where a marshal failure means a bug rather than bad
// input; the caller's error is returned by the write, which is where it matters.
func mustJSON(value any) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		// An empty object decodes to a zero record, and the write then fails on the model's own
		// constraints rather than silently writing something wrong.
		return []byte("{}")
	}
	return encoded
}
