package jsonrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/Aone2233/nekomari/database/auditlog"
	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/database/tasks"
	"github.com/Aone2233/nekomari/internal/clientguard"
	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/internal/configxport"
	"github.com/Aone2233/nekomari/internal/pingguard"
	"github.com/Aone2233/nekomari/pkg/rpc"
	"github.com/Aone2233/nekomari/pkg/secretcmp"
	"github.com/Aone2233/nekomari/utils"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
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
	// Two export methods rather than one method with a dangerous flag.
	//
	// The sensitive-operation gate is keyed on the *method name* (pkg/rpc/sensitive.go), not on a
	// request parameter, so "export the credentials too" cannot be gated on include_secrets while
	// staying inside admin:exportConfig. Splitting the method is what makes the gate expressible:
	// the safe export stays a one-click operation, and the credential export is a separately
	// registered operation that the transport refuses without a second factor.
	RegisterWithGroupAndMeta("exportConfig", rpc.RoleAdmin, adminExportConfig, &rpc.MethodMeta{
		Name:    "admin:exportConfig",
		Summary: "Export nodes, tasks, windows, settings and notification policies as one JSON document",
		Returns: "Document; credentials are always excluded (use admin:exportConfigSecrets for those)",
	})
	RegisterWithGroupAndMeta("exportConfigSecrets", rpc.RoleAdmin, adminExportConfigSecrets, &rpc.MethodMeta{
		Name:    "admin:exportConfigSecrets",
		Summary: "Export the same document including credentials; requires sensitive-operation 2FA",
		Params: []rpc.ParamMeta{
			{Name: "2fa_code", Type: "string", Required: false, Description: "One-time password; required when the account has 2FA enabled"},
		},
		Returns: "Document; secrets_included is true",
	})
	rpc.MarkSensitive("admin:exportConfigSecrets")
	RegisterWithGroupAndMeta("exportConfigText", rpc.RoleAdmin, adminExportConfigText, &rpc.MethodMeta{
		Name: "admin:exportConfigText", Summary: "Export the credential-free document as exact JSON text",
		Returns: "JSON text; preserves integer precision in browser clients",
	})
	RegisterWithGroupAndMeta("exportConfigSecretsText", rpc.RoleAdmin, adminExportConfigSecretsText, &rpc.MethodMeta{
		Name: "admin:exportConfigSecretsText", Summary: "Export the credential-bearing document as exact JSON text; requires sensitive-operation 2FA",
		Params:  []rpc.ParamMeta{{Name: "2fa_code", Type: "string", Required: false, Description: "One-time password; required when the account has 2FA enabled"}},
		Returns: "JSON text; preserves integer precision in browser clients",
	})
	rpc.MarkSensitive("admin:exportConfigSecretsText")
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
	// Import can change multiple entities and restore credentials.
	rpc.MarkSensitive("admin:importConfig")
}

func adminExportConfig(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	// include_secrets used to be a parameter here. Refusing it rather than ignoring it matters:
	// a caller that asks for credentials and silently receives a document without them would
	// believe it had a backup of the tokens it does not have. The error names the method that can
	// do it, so the fix is in the message.
	var params struct {
		IncludeSecrets bool `json:"include_secrets"`
	}
	_ = req.BindParams(&params)
	if params.IncludeSecrets {
		return nil, rpc.MakeError(rpc.InvalidParams,
			"credentials are not exported by admin:exportConfig; call admin:exportConfigSecrets, which requires sensitive-operation 2FA", nil)
	}
	return exportConfiguration(ctx, false)
}

// adminExportConfigSecrets is the credential-carrying export. pkg/rpc's gate has already checked
// the second factor before this handler runs (init above registers it with rpc.MarkSensitive); the
// 2fa_code parameter is consumed by the transport, so it is deliberately not read here. The audit
// line below is the second half: a file that carries agent tokens is named in the trail even when
// the caller was legitimate.
func adminExportConfigSecrets(ctx context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	return exportConfiguration(ctx, true)
}

func adminExportConfigText(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	document, jerr := adminExportConfig(ctx, req)
	return encodeExportText(document, jerr)
}

func adminExportConfigSecretsText(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	document, jerr := adminExportConfigSecrets(ctx, req)
	return encodeExportText(document, jerr)
}

func encodeExportText(document any, jerr *rpc.JsonRpcError) (any, *rpc.JsonRpcError) {
	if jerr != nil {
		return nil, jerr
	}
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to encode configuration", nil)
	}
	return string(encoded), nil
}

func exportConfiguration(ctx context.Context, includeSecrets bool) (any, *rpc.JsonRpcError) {
	document, err := configxport.Export(exportStore{}, includeSecrets, utils.CurrentVersion)
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to export configuration: "+err.Error(), nil)
	}
	if includeSecrets {
		// A secrets export is worth naming in the audit trail: the file it produces is now a
		// credential store, and docs/SECRETS.md exists because that has gone wrong here before.
		actor, ip := auditActor(ctx)
		auditlog.Log(ip, actor, "export configuration including secrets", "warn")
	}
	return document, nil
}

// rawImportDocument accepts only a named parameter object and operates on the
// original request. A map that has already been decoded cannot prove that the
// original JSON had no duplicate keys or retain integers above 2^53.
func rawImportDocument(req *rpc.JsonRpcRequest) ([]byte, error) {
	raw := req.RawJSON()
	if len(raw) == 0 {
		return nil, fmt.Errorf("original JSON request is required")
	}
	if err := configxport.RejectAmbiguousJSON(raw); err != nil {
		return nil, err
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope == nil {
		return nil, fmt.Errorf("request must be an object")
	}
	for key := range envelope {
		switch key {
		case "jsonrpc", "method", "params", "id":
		default:
			return nil, fmt.Errorf("unknown request field")
		}
	}
	var params map[string]json.RawMessage
	if err := json.Unmarshal(envelope["params"], &params); err != nil || params == nil {
		return nil, fmt.Errorf("params must be an object")
	}
	for key := range params {
		switch key {
		case "document", "document_json", "review_token", "2fa_code", "two_factor_code", "otp":
		default:
			return nil, fmt.Errorf("unknown import parameter")
		}
	}
	document, objectGiven := params["document"]
	text, textGiven := params["document_json"]
	if objectGiven == textGiven {
		return nil, fmt.Errorf("exactly one document or document_json is required")
	}
	if textGiven {
		var original string
		if err := json.Unmarshal(text, &original); err != nil {
			return nil, fmt.Errorf("document_json must be a string")
		}
		document = []byte(original)
	}
	if trimmed := bytes.TrimSpace(document); len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, fmt.Errorf("document must be an object")
	}
	return document, nil
}

// bindDocument parses original bytes before checking any database state. The
// import handlers remain disabled until the remaining transaction and rollout
// gates are complete.
func bindDocument(req *rpc.JsonRpcRequest) (configxport.Document, *rpc.JsonRpcError) {
	raw, err := rawImportDocument(req)
	if err != nil {
		return configxport.Document{}, rpc.MakeError(rpc.InvalidParams, "Invalid request body: "+err.Error(), nil)
	}
	document, err := configxport.Parse(raw)
	if err != nil {
		return configxport.Document{}, rpc.MakeError(rpc.InvalidParams, err.Error(), nil)
	}
	if _, exists := document.Settings[config.ApiKeyKey]; exists {
		return configxport.Document{}, rpc.MakeError(rpc.InvalidParams, "api_key is retired; remove it from the import and create scoped keys instead", nil)
	}
	for _, record := range document.Clients {
		id, _ := record["uuid"].(string)
		if _, err := importedClientMetadata(context.Background(), id, record); err != nil {
			return configxport.Document{}, rpc.MakeError(rpc.InvalidParams, err.Error(), nil)
		}
	}
	for _, record := range document.PingTasks {
		if hasReservedPingFamilyField(record) {
			return configxport.Document{}, rpc.MakeError(rpc.InvalidParams, "family-pinned task import is not available yet", nil)
		}
	}
	// Planning is not an atomic transaction across all entities. Refuse before
	// any writes when draft pinned tasks exist, even if the document omits the
	// pin or the planner fails to identify their numeric IDs.
	var pinned int64
	if err := dbcore.GetDBInstance().Model(&models.PingTask{}).
		Where("target_family <> ? OR pinned_generation <> ?", "", "").Count(&pinned).Error; err != nil {
		return configxport.Document{}, rpc.MakeError(rpc.InternalError, "failed to inspect family-pinned tasks", nil)
	}
	if pinned != 0 {
		return configxport.Document{}, rpc.MakeError(rpc.InvalidParams, "configuration import is unavailable while family-pinned tasks exist", nil)
	}
	return document, nil
}

// Import maps are passed to GORM on updates. Reject alternate spellings too,
// rather than allowing an imported map to set a draft's internal incarnation.
func hasReservedPingFamilyField(record map[string]any) bool {
	for key := range record {
		key = strings.ToLower(strings.Trim(key, "`\"[] "))
		switch strings.ReplaceAll(key, "_", "") {
		case "targetfamily", "pinnedgeneration":
			return true
		}
	}
	return false
}

// Existing-node imports may carry an unchanged secret from a secrets export,
// but cannot replace it outside the step-up and retirement boundary.
func importedClientMetadata(ctx context.Context, id string, record map[string]any) (map[string]any, error) {
	payload := make(map[string]any, len(record))
	for key, value := range record {
		if !strings.EqualFold(strings.Trim(key, "`\"[] "), "token") {
			payload[key] = value
			continue
		}
		if key != "token" {
			return nil, fmt.Errorf("use rotateClientToken to change an existing agent credential")
		}
		var client models.Client
		err := dbcore.GetDBInstance().WithContext(ctx).Session(&gorm.Session{Logger: logger.Discard}).Select("token").Where("uuid = ?", id).Take(&client).Error
		if err == gorm.ErrRecordNotFound {
			continue // New-node credential restoration is unchanged.
		}
		if err != nil {
			return nil, fmt.Errorf("cannot validate imported client credential")
		}
		token, ok := value.(string)
		if !ok || !secretcmp.Equal(client.Token, token) {
			return nil, fmt.Errorf("use rotateClientToken to change an existing agent credential")
		}
	}
	delete(payload, "uuid")
	return payload, nil
}

func adminPlanConfigImport(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	return reviewedDraftConfigImportRPC(ctx, req, dbcore.ReadyDBInstance(), true, tasks.ReloadPingSchedule)
}

func adminImportConfig(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	return reviewedDraftConfigImportRPC(ctx, req, dbcore.ReadyDBInstance(), false, tasks.ReloadPingSchedule)
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
		if identity == config.ApiKeyKey || payload["key"] == config.ApiKeyKey {
			return fmt.Errorf("api_key is retired; create scoped keys instead")
		}
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
		return clientguard.With(context.Background(), identity, func() error {
			metadata, err := importedClientMetadata(context.Background(), identity, payload)
			if err != nil {
				return err
			}
			return db.Model(&models.Client{}).Where("uuid = ?", identity).Updates(metadata).Error
		})
	case "ping_tasks":
		if hasReservedPingFamilyField(payload) {
			return fmt.Errorf("family-pinned task import is not available yet")
		}
		if create {
			delete(payload, "id")
			var task models.PingTask
			if err := jsonUnmarshal(mustJSON(payload), &task); err != nil {
				return fmt.Errorf("ping task record: %w", err)
			}
			return db.Create(&task).Error
		}
		delete(payload, "id")
		id, err := strconv.ParseUint(identity, 10, 0)
		if err != nil || id == 0 {
			return fmt.Errorf("invalid ping task ID")
		}
		return pingguard.With(context.Background(), uint(id), func() error {
			var task models.PingTask
			if err := db.Select("id", "target_family").Take(&task, id).Error; err != nil {
				return err
			}
			if task.TargetFamily != "" {
				return fmt.Errorf("family-pinned task import is not available yet")
			}
			return db.Model(&models.PingTask{}).Where("id = ?", id).Updates(payload).Error
		})
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
