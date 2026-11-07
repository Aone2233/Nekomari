package jsonrpc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/internal/configxport"
	"github.com/Aone2233/nekomari/internal/metricstore"
	"github.com/Aone2233/nekomari/internal/pingguard"
	"github.com/google/uuid"
	"gorm.io/gorm"
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
type exportStore struct{ db *gorm.DB }

func (s exportStore) database() *gorm.DB {
	if s.db != nil {
		return s.db
	}
	return dbcore.GetDBInstance()
}

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
	exportWindowFields       = []string{"id", "name", "start", "end", "clients", "reason"}
	exportNotificationFields = []string{"client", "enable", "grace_period"}
)

// Settings are free-form: a key that does not sound secret may contain a DSN,
// a URL with credentials, a nested object, or an internal migration marker.
// Only reviewed scalar settings are included in the ordinary export. All
// other keys require the explicit sensitive export, not a suffix guess.
var ordinaryExportSettings = map[string]string{
	config.SitenameKey:                                    "string",
	config.DescriptionKey:                                 "string",
	config.CorsOriginCheckEnabledKey:                      "bool",
	config.CorsAllowedOriginsKey:                          "string",
	config.WsOriginCheckEnabledKey:                        "bool",
	config.WsAllowedOriginsKey:                            "string",
	config.ThemeKey:                                       "string",
	config.PrivateSiteKey:                                 "bool",
	config.SendIpAddrToGuestKey:                           "bool",
	config.VisitorAuditEnabledKey:                         "bool",
	config.SSRFProtectionEnabledKey:                       "bool",
	config.EulaAcceptedKey:                                "bool",
	config.GeoIpEnabledKey:                                "bool",
	config.GeoIpProviderKey:                               "string",
	config.OAuthEnabledKey:                                "bool",
	config.OAuthProviderKey:                               "string",
	config.DisablePasswordLoginKey:                        "bool",
	config.NotificationEnabledKey:                         "bool",
	config.NotificationMethodKey:                          "string",
	config.ExpireNotificationEnabledKey:                   "bool",
	config.ExpireNotificationLeadDaysKey:                  "number",
	config.LoginNotificationKey:                           "bool",
	config.TrafficLimitPercentageKey:                      "number",
	config.TrafficMonthRotateKey:                          "number",
	metricstore.MetricDBDriverKey:                         "string",
	metricstore.MetricTablePrefixKey:                      "string",
	metricstore.MetricMaxOpenConnsKey:                     "number",
	metricstore.MetricMaxIdleConnsKey:                     "number",
	metricstore.MetricRollupMinuteRetentionMinutesKey:     "number",
	metricstore.MetricRollupFiveMinuteRetentionMinutesKey: "number",
	metricstore.MetricRollupHourRetentionHoursKey:         "number",
}

func (s exportStore) ExportClients(includeSecrets bool) ([]map[string]any, error) {
	db := s.database()
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

func (s exportStore) ExportPingTasks() ([]map[string]any, error) {
	db := s.database()
	if db == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	var rows []models.PingTask
	if err := db.Order("id asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		id, err := uuid.Parse(row.ConfigUID)
		if err != nil || id == uuid.Nil || id.String() != row.ConfigUID {
			return nil, fmt.Errorf("ping task %d has an invalid configuration identity", row.Id)
		}
		if row.TargetFamily == "" && row.PinnedGeneration != "" {
			return nil, fmt.Errorf("ping task %d has an inconsistent family-pinned configuration", row.Id)
		}
		if row.TargetFamily != "" && row.TargetFamily != "ipv4" && row.TargetFamily != "ipv6" {
			return nil, fmt.Errorf("ping task %d has an invalid target family", row.Id)
		}
		if row.TargetFamily != "" && !pingguard.ValidGeneration(row.PinnedGeneration) {
			return nil, fmt.Errorf("ping task %d has an invalid family-pinned generation", row.Id)
		}
		record, err := toRecord(row, exportPingTaskFields)
		if err != nil {
			return nil, fmt.Errorf("ping task %d: %w", row.Id, err)
		}
		record["config_uid"] = row.ConfigUID
		// Runtime generations must not be restored; a future safe importer must
		// issue a fresh one. Preserve the public pin so it cannot be lost.
		if row.TargetFamily != "" {
			record["target_family"] = row.TargetFamily
		}
		out = append(out, record)
	}
	return out, nil
}

func (s exportStore) ExportMaintenanceWindows() ([]map[string]any, error) {
	db := s.database()
	if db == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	var rows []models.MaintenanceWindow
	if err := db.Order("id asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		id, err := uuid.Parse(row.ConfigUID)
		if err != nil || id == uuid.Nil || id.String() != row.ConfigUID {
			return nil, fmt.Errorf("maintenance window %d has an invalid configuration identity", row.ID)
		}
		record, err := toRecord(row, exportWindowFields)
		if err != nil {
			return nil, fmt.Errorf("maintenance window %d: %w", row.ID, err)
		}
		record["config_uid"] = row.ConfigUID
		out = append(out, record)
	}
	return out, nil
}

func (s exportStore) ExportSettings(includeSecrets bool) (map[string]any, error) {
	db := s.database()
	if db == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	var items []config.ConfigItem
	if err := db.Find(&items).Error; err != nil {
		return nil, err
	}
	all := make(map[string]any, len(items))
	for _, item := range items {
		var value any
		decoder := json.NewDecoder(strings.NewReader(item.Value))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err == nil {
			all[item.Key] = value
		}
	}
	delete(all, config.ApiKeyKey)
	if includeSecrets {
		return all, nil
	}
	out := make(map[string]any, len(all))
	for key, value := range all {
		if !isOrdinaryExportSetting(key, value) {
			continue
		}
		out[key] = value
	}
	return out, nil
}

func (s exportStore) ExportNotificationConfigs() ([]map[string]any, error) {
	db := s.database()
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

func (s exportStore) CurrentIDs(entity string) (map[string]string, error) {
	records, err := s.Existing(entity)
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

// identityFieldFor mirrors the exported record identities used by the planner.
func identityFieldFor(entity string) string {
	if entity == "settings" {
		return "key"
	}
	if entity == "ping_tasks" || entity == "maintenance_windows" {
		return "id"
	}
	if entity == "notification_configs" {
		return "client"
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
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err := decoder.Decode(&all); err != nil {
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

func isOrdinaryExportSetting(key string, value any) bool {
	switch ordinaryExportSettings[key] {
	case "string":
		_, ok := value.(string)
		return ok
	case "bool":
		_, ok := value.(bool)
		return ok
	case "number":
		switch v := value.(type) {
		case float64:
			return true
		case json.Number:
			_, err := strconv.ParseFloat(string(v), 64)
			return err == nil
		}
	}
	return false
}

var _ configxport.Store = exportStore{}
