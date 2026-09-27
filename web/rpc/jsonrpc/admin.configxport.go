package jsonrpc

import (
	"fmt"
	"strings"
	"sort"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/internal/configxport"
)

// exportStore is the panel's configuration as configxport sees it.
//
// It lives here rather than in internal/configxport because it is the only part that needs a
// database, and the package's decisions are testable without one. The field lists below are the
// *only* place a field becomes exportable: a new column is invisible to an export until it is named,
// which is why a credential cannot leak by default.
//
// Why records travel as `map[string]any` rather than as the models: `configxport` compares a
// document's fields against a store's, and a typed record would have to expose every column to be
// comparable — including the ones this file exists to keep out. The map is built from an explicit
// list, so "not exported" and "does not exist" look the same to the planner, which is the intent.
type exportStore struct{}

// The exportable fields per entity. Named for the same reason `SecretsExcludedFromExport` is:
// adding a column to a model must not add it to a configuration document by itself.
var (
	exportClientFields = []string{
		"uuid", "name", "group", "tags", "weight", "price", "billing_cycle", "currency",
		"expired_at", "hidden", "traffic_limit", "traffic_limit_type", "auto_renewal",
		"public_remark", "remark", "region",
	}
	// A client's `token` is deliberately absent from the list above, and so is anything else that
	// authenticates it. `exportClientFieldsWithSecrets` is what an explicit secrets export uses.
	exportClientSecretFields = []string{"token"}

	exportPingTaskFields = []string{
		"id", "name", "clients", "default_on", "type", "target", "reference", "interval", "weight",
	}
	exportWindowFields = []string{"id", "name", "start", "end", "clients", "reason"}
	exportNotificationFields = []string{"client", "enable", "grace_period"}
)

// settingsSecretSuffixes are the settings keys an export omits unless secrets are requested.
//
// Matched by suffix rather than listed, because settings keys are free-form and new ones appear:
// a list would be a list to forget, and this way a key named `smtp_password` or `oauth_secret` is
// excluded by its name alone.
var settingsSecretSuffixes = []string{"password", "secret", "token", "api_key", "apikey", "private_key"}

func (exportStore) ExportClients(includeSecrets bool) ([]map[string]any, error) {
	db := dbcore.GetDBInstance()
	if db == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	var rows []models.Client
	if err := db.Order("uuid asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	fields := exportClientFields
	if includeSecrets {
		fields = append(append([]string{}, exportClientFields...), exportClientSecretFields...)
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		record, err := toRecord(row, fields)
		if err != nil {
			return nil, fmt.Errorf("client %s: %w", row.UUID, err)
		}
		out = append(out, record)
	}
	return out, nil
}

func (exportStore) ExportPingTasks() ([]map[string]any, error) {
	db := dbcore.GetDBInstance()
	if db == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	var rows []models.PingTask
	if err := db.Order("id asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		record, err := toRecord(row, exportPingTaskFields)
		if err != nil {
			return nil, fmt.Errorf("ping task %d: %w", row.Id, err)
		}
		out = append(out, record)
	}
	return out, nil
}

func (exportStore) ExportMaintenanceWindows() ([]map[string]any, error) {
	db := dbcore.GetDBInstance()
	if db == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	var rows []models.MaintenanceWindow
	if err := db.Order("id asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		record, err := toRecord(row, exportWindowFields)
		if err != nil {
			return nil, fmt.Errorf("maintenance window %d: %w", row.ID, err)
		}
		out = append(out, record)
	}
	return out, nil
}

func (exportStore) ExportSettings(includeSecrets bool) (map[string]any, error) {
	all, err := config.GetAll()
	if err != nil {
		return nil, err
	}
	if includeSecrets {
		return all, nil
	}
	out := make(map[string]any, len(all))
	for key, value := range all {
		if isSecretSetting(key) {
			continue
		}
		out[key] = value
	}
	return out, nil
}

func (exportStore) ExportNotificationConfigs() ([]map[string]any, error) {
	db := dbcore.GetDBInstance()
	if db == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	var rows []models.OfflineNotification
	if err := db.Order("client asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		record, err := toRecord(row, exportNotificationFields)
		if err != nil {
			return nil, fmt.Errorf("notification config %s: %w", row.Client, err)
		}
		out = append(out, record)
	}
	return out, nil
}

func (exportStore) CurrentIDs(entity string) (map[string]string, error) {
	records, err := exportStore{}.Existing(entity)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(records))
	for _, record := range records {
		identity := fmt.Sprint(record[identityFieldFor(entity)])
		out[identity] = fmt.Sprint(record["name"])
	}
	return out, nil
}

func (s exportStore) Existing(entity string) ([]map[string]any, error) {
	switch entity {
	case "clients":
		return s.ExportClients(false)
	case "ping_tasks":
		return s.ExportPingTasks()
	case "maintenance_windows":
		return s.ExportMaintenanceWindows()
	case "notification_configs":
		return s.ExportNotificationConfigs()
	case "settings":
		settings, err := s.ExportSettings(false)
		if err != nil {
			return nil, err
		}
		keys := make([]string, 0, len(settings))
		for key := range settings {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out := make([]map[string]any, 0, len(keys))
		for _, key := range keys {
			out = append(out, map[string]any{"key": key, "value": settings[key]})
		}
		return out, nil
	}
	return nil, fmt.Errorf("unknown entity %q", entity)
}

// identityFieldFor mirrors configxport's identity rule, which is settings-by-key and everything
// else by uuid.
func identityFieldFor(entity string) string {
	if entity == "settings" {
		return "key"
	}
	if entity == "ping_tasks" || entity == "maintenance_windows" {
		return "id"
	}
	return "uuid"
}

// toRecord renders a model's named fields into a record.
//
// Through JSON rather than reflection: the models already carry the field names a document should
// use, and going through `MarshalJSON` means a document and the API agree about a field's name
// without a second spelling to keep in step.
func toRecord(model any, fields []string) (map[string]any, error) {
	encoded, err := jsonMarshal(model)
	if err != nil {
		return nil, err
	}
	var all map[string]any
	if err := jsonUnmarshal(encoded, &all); err != nil {
		return nil, err
	}
	record := make(map[string]any, len(fields))
	for _, field := range fields {
		if value, ok := all[field]; ok {
			record[field] = value
		}
	}
	return record, nil
}

// isSecretSetting reports whether a settings key names a credential.
func isSecretSetting(key string) bool {
	lower := strings.ToLower(key)
	for _, suffix := range settingsSecretSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

var _ configxport.Store = exportStore{}
