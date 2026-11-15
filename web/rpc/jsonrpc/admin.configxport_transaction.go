package jsonrpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/clientguard"
	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/internal/configxport"
	"github.com/Aone2233/nekomari/internal/dbcache"
	"github.com/Aone2233/nekomari/internal/pingguard"
	"github.com/Aone2233/nekomari/pkg/secretcmp"
	"github.com/Aone2233/nekomari/utils"
	"github.com/Aone2233/nekomari/utils/notifier"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

// The public preview/import handlers use this transactional implementation.
type transactionalConfigImport struct {
	db       *gorm.DB
	document configxport.Document
	settings map[string]any
	changes  bool
	windows  bool
	implicit map[string]configxport.Change
	nodes    []string
}

const maxConfigImportRecords = 2048

func (a *transactionalConfigImport) Existing(entity string) ([]map[string]any, error) {
	if entity == "settings" && a.document.SecretsIncluded {
		settings, err := (exportStore{db: a.db}).ExportSettings(true)
		if err != nil {
			return nil, err
		}
		out := make([]map[string]any, 0, len(settings))
		for key, value := range settings {
			out = append(out, map[string]any{"key": key, "value": value})
		}
		return out, nil
	}
	return (exportStore{db: a.db}).Existing(entity)
}
func (a *transactionalConfigImport) ExportClients(secrets bool) ([]map[string]any, error) {
	return (exportStore{db: a.db}).ExportClients(secrets)
}
func (a *transactionalConfigImport) ExportPingTasks() ([]map[string]any, error) {
	return (exportStore{db: a.db}).ExportPingTasks()
}
func (a *transactionalConfigImport) ExportMaintenanceWindows() ([]map[string]any, error) {
	return (exportStore{db: a.db}).ExportMaintenanceWindows()
}
func (a *transactionalConfigImport) ExportSettings(secrets bool) (map[string]any, error) {
	return (exportStore{db: a.db}).ExportSettings(secrets)
}
func (a *transactionalConfigImport) ExportNotificationConfigs() ([]map[string]any, error) {
	return (exportStore{db: a.db}).ExportNotificationConfigs()
}
func (a *transactionalConfigImport) CurrentIDs(entity string) (map[string]string, error) {
	return (exportStore{db: a.db}).CurrentIDs(entity)
}

func (a *transactionalConfigImport) Create(entity string, record map[string]any) error {
	identity := ""
	if entity == "ping_tasks" || entity == "maintenance_windows" {
		identity, _ = record["config_uid"].(string)
	}
	return a.write(entity, identity, record, true)
}
func (a *transactionalConfigImport) Update(entity, identity string, record map[string]any) error {
	return a.write(entity, identity, record, false)
}

func configImportID(value any) (uint, error) {
	text := fmt.Sprint(value)
	id, err := strconv.ParseUint(text, 10, strconv.IntSize)
	if err != nil || id == 0 || strconv.FormatUint(id, 10) != text {
		return 0, errors.New("invalid numeric identity")
	}
	return uint(id), nil
}

func configImportNodeID(value any) (string, error) {
	id, ok := value.(string)
	parsed, err := uuid.Parse(id)
	if !ok || err != nil || parsed == uuid.Nil || parsed.String() != id {
		return "", errors.New("invalid node identity")
	}
	return id, nil
}

// Referenced nodes must share the credential/deletion fence even when their
// metadata does not appear in the document.
func configImportReferencedNodes(record map[string]any) ([]string, error) {
	value, ok := record["clients"]
	if !ok {
		return nil, nil
	}
	var decoded struct {
		Clients []string `json:"clients"`
	}
	if err := decodeConfigRecord(map[string]any{"clients": value}, &decoded); err != nil {
		return nil, errors.New("invalid node assignments")
	}
	seen := make(map[string]bool, len(decoded.Clients))
	for _, id := range decoded.Clients {
		if _, err := configImportNodeID(id); err != nil || seen[id] {
			return nil, errors.New("invalid or duplicate node assignment")
		}
		seen[id] = true
	}
	return decoded.Clients, nil
}

func configImportFields(record map[string]any, allowed []string, nullable ...string) error {
	fields := make(map[string]bool, len(allowed))
	null := make(map[string]bool, len(nullable))
	for _, field := range allowed {
		fields[field] = true
	}
	for _, field := range nullable {
		null[field] = true
	}
	for key, value := range record {
		if !fields[key] {
			return fmt.Errorf("unsupported field %q", key)
		}
		if value == nil && !null[key] {
			return fmt.Errorf("field %q cannot be null", key)
		}
	}
	return nil
}

func decodeConfigRecord(record map[string]any, into any) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, into)
}

func configImportUpdates(record map[string]any, identity string) map[string]any {
	updates := make(map[string]any, len(record))
	for key, value := range record {
		if key != identity {
			updates[key] = value
		}
	}
	return updates
}

// Only these presentation settings have reviewed import semantics. Security,
// authentication, provider and metric-store settings must be edited through
// their dedicated flows, even when they appear in an ordinary export.
func mutableConfigImportSetting(key string) bool {
	return key == config.SitenameKey || key == config.DescriptionKey
}

func validateMutableConfigImportSetting(value any) error {
	text, ok := value.(string)
	if !ok || !utf8.ValidString(text) || len(text) > 16*1024 {
		return errors.New("invalid presentation setting")
	}
	return nil
}

// Check every supplied setting, including planner-unchanged values. A
// credential-bearing export may include arbitrary settings; retain them only
// if the current database already contains exactly the same JSON value.
func validateConfigImportSettings(tx *gorm.DB, settings map[string]any) error {
	for key, value := range settings {
		if mutableConfigImportSetting(key) {
			if err := validateMutableConfigImportSetting(value); err != nil {
				return err
			}
			continue
		}
		var current config.ConfigItem
		quiet := tx.Session(&gorm.Session{Logger: logger.Discard})
		if err := quiet.Clauses(clause.Locking{Strength: "UPDATE"}).Where("key = ?", key).Take(&current).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("settings must be changed separately")
			}
			return errors.New("cannot inspect imported settings")
		}
		var stored any
		decoder := json.NewDecoder(strings.NewReader(current.Value))
		decoder.UseNumber()
		if err := decoder.Decode(&stored); err != nil {
			return errors.New("cannot inspect imported settings")
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return errors.New("cannot inspect imported settings")
		}
		wantedJSON, wantedErr := json.Marshal(value)
		storedJSON, storedErr := json.Marshal(stored)
		if wantedErr != nil || storedErr != nil || !secretcmp.Equal(string(wantedJSON), string(storedJSON)) {
			return errors.New("settings must be changed separately")
		}
	}
	return nil
}

func (a *transactionalConfigImport) write(entity, identity string, record map[string]any, create bool) error {
	switch entity {
	case "clients":
		return a.writeClient(identity, record, create)
	case "ping_tasks":
		return a.writePing(identity, record, create)
	case "maintenance_windows":
		return a.writeWindow(identity, record, create)
	case "notification_configs":
		return a.writeNotification(identity, record, create)
	case "settings":
		if !mutableConfigImportSetting(identity) {
			return errors.New("settings must be changed separately")
		}
		if record["key"] != identity {
			return errors.New("invalid setting identity")
		}
		if err := validateMutableConfigImportSetting(record["value"]); err != nil {
			return err
		}
		if a.settings == nil {
			a.settings = make(map[string]any)
		}
		a.settings[identity] = record["value"]
		return nil
	default:
		return fmt.Errorf("unknown configuration entity %q", entity)
	}
}

func (a *transactionalConfigImport) writeClient(identity string, record map[string]any, create bool) error {
	fields := append([]string{}, exportClientFields...)
	if a.document.SecretsIncluded {
		fields = append(fields, "token")
	}
	if err := configImportFields(record, fields, "expired_at"); err != nil {
		return err
	}
	id, err := configImportNodeID(record["uuid"])
	if err != nil || (!create && id != identity) {
		return errors.New("invalid client identity")
	}
	var current models.Client
	if !create {
		if err := a.db.Where("uuid = ?", id).Take(&current).Error; err != nil {
			return err
		}
		if token, exists := record["token"]; exists {
			text, ok := token.(string)
			if !ok || !secretcmp.Equal(current.Token, text) {
				return errors.New("use rotateClientToken to change an existing agent credential")
			}
		}
	}
	value := current
	if err := decodeConfigRecord(record, &value); err != nil {
		return fmt.Errorf("invalid client record: %w", err)
	}
	if value.TrafficLimit < 0 {
		return errors.New("invalid traffic limit")
	}
	if create {
		if value.Name == "" || len(value.Name) > 100 {
			return errors.New("invalid client name")
		}
		if _, exists := record["token"]; exists {
			if !a.document.SecretsIncluded || value.Token == "" || len(value.Token) > 255 {
				return errors.New("invalid imported agent credential")
			}
		} else {
			value.Token = utils.GenerateToken()
			if value.Token == "" {
				return errors.New("failed to create agent credential")
			}
		}
		// GORM traces failed/slow SQL with bound values. An imported or newly
		// generated agent token must never appear in logs, including on error.
		if err := a.db.Session(&gorm.Session{Logger: logger.Discard}).Create(&value).Error; err != nil {
			return errors.New("failed to create imported node; verify its identity and credential")
		}
		// A struct insert substitutes model defaults for explicitly empty
		// strings; preserve those fields when they were in the document.
		emptyDefaults := make(map[string]any)
		for _, field := range []string{"currency", "traffic_limit_type"} {
			if v, specified := record[field]; specified && v == "" {
				emptyDefaults[field] = ""
			}
		}
		if len(emptyDefaults) != 0 {
			if err := a.db.Model(&models.Client{}).Where("uuid = ?", id).Updates(emptyDefaults).Error; err != nil {
				return err
			}
		}
		a.nodes = append(a.nodes, id)
		// A new node receives every current default-on task. Explicit task
		// assignments are reconciled and validated before commit.
		var defaults []models.PingTask
		if err := a.db.Where("all_clients = ?", true).Find(&defaults).Error; err != nil {
			return err
		}
		for _, task := range defaults {
			if task.AppliesToClient(id) {
				continue
			}
			assigned := append(append(models.StringArray{}, task.Clients...), id)
			updates := map[string]any{"clients": assigned}
			if task.TargetFamily != "" {
				if !pingguard.ValidGeneration(task.PinnedGeneration) {
					return errors.New("invalid family-pinned task generation")
				}
				generation, err := pingguard.NewGeneration()
				if err != nil {
					return err
				}
				updates["pinned_generation"] = generation
			}
			if err := a.db.Model(&models.PingTask{}).Where("id = ?", task.Id).Updates(updates).Error; err != nil {
				return err
			}
			if a.implicit == nil {
				a.implicit = make(map[string]configxport.Change)
			}
			// A renewed runtime generation only fences in-flight probes; it is
			// not a user-editable field in the plan.
			a.implicit[task.ConfigUID] = configxport.Change{Kind: configxport.ChangeUpdate, Entity: "ping_tasks", ID: task.ConfigUID,
				Name: task.Name, Fields: []string{"clients"}, Effect: "default_on_assignment"}
			a.changes = true
		}
		return nil
	}
	updates := configImportUpdates(record, "uuid")
	delete(updates, "token")
	return a.db.Model(&models.Client{}).Where("uuid = ?", id).Updates(updates).Error
}

func (a *transactionalConfigImport) writePing(identity string, record map[string]any, create bool) error {
	fields := append(append([]string{}, exportPingTaskFields...), "target_family", "config_uid")
	if err := configImportFields(record, fields); err != nil {
		return err
	}
	uid, err := configImportNodeID(record["config_uid"])
	if err != nil || uid != identity {
		return errors.New("invalid task configuration identity")
	}
	var current models.PingTask
	if !create {
		if err := a.db.Where("config_uid = ?", uid).Take(&current).Error; err != nil {
			return err
		}
	}
	value := current
	payload := configImportUpdates(record, "id")
	delete(payload, "config_uid")
	if err := decodeConfigRecord(payload, &value); err != nil {
		return fmt.Errorf("invalid task record: %w", err)
	}
	if value.Name == "" || len(value.Name) > 255 || value.Target == "" || len(value.Target) > 255 || len(value.Reference) > 255 {
		return errors.New("invalid ping task configuration")
	}
	if !create && value.TargetFamily != current.TargetFamily {
		return errors.New("changing target family requires a new task")
	}
	if err := utils.ValidatePinnedPingInterval(value.Interval); err != nil {
		return err
	}
	if value.TargetFamily != "" {
		if !create && (!pingguard.ValidGeneration(current.PinnedGeneration) || value.Target != current.Target || value.Type != current.Type || value.Reference != current.Reference) {
			return errors.New("changing pinned measurement identity requires a new task")
		}
		if err := utils.ValidatePinnedPingTarget(value.Target, value.Type, value.TargetFamily); err != nil {
			return err
		}
	} else {
		if !create && (value.Target != current.Target || value.Type != current.Type || value.Reference != current.Reference) {
			return errors.New("changing measurement identity requires a new task")
		}
		if err := utils.ValidatePingTaskTargetFamilyWithDB(a.db, value.Target, value.DefaultOn, value.Clients); err != nil {
			return err
		}
	}
	if create && value.TargetFamily == "" {
		switch value.Type {
		case "icmp", "tcp", "http", "auto", "dual":
		default:
			return errors.New("invalid ping task type")
		}
	}
	if err := a.validateNodes(value.Clients); err != nil {
		return err
	}
	if create {
		var reservedUID int64
		if err := a.db.Model(&models.PingTaskDeletion{}).Where("config_uid = ?", uid).Count(&reservedUID).Error; err != nil {
			return err
		}
		if reservedUID != 0 {
			return errors.New("deleted task identity cannot be imported again")
		}
		value.Id = 0
		value.ConfigUID = uid
		if value.TargetFamily != "" {
			value.PinnedGeneration, err = pingguard.NewGeneration()
			if err != nil {
				return err
			}
		}
		if err := a.db.Create(&value).Error; err != nil {
			return err
		}
		var reserved int64
		if err := a.db.Model(&models.PingTaskDeletion{}).Where("task_id = ?", value.Id).Count(&reserved).Error; err != nil {
			return err
		}
		if reserved != 0 {
			return errors.New("generated ping task ID is reserved")
		}
		// The plan was computed before newly created nodes received default-on
		// assignments. Match a later identical import to that plan.
		if _, assigned := payload["clients"]; !assigned && value.DefaultOn {
			return errors.New("new default-on task must specify its assignments")
		}
		a.changes = true
		return nil
	}
	updates := payload
	delete(updates, "target_family")
	if _, ok := updates["clients"]; ok {
		updates["clients"] = value.Clients
	}
	if _, ok := updates["default_on"]; ok {
		delete(updates, "default_on")
		updates["all_clients"] = value.DefaultOn
	}
	if current.TargetFamily != "" {
		generation, err := pingguard.NewGeneration()
		if err != nil {
			return err
		}
		updates["pinned_generation"] = generation
	}
	if err := a.db.Model(&models.PingTask{}).Where("id = ?", current.Id).Updates(updates).Error; err != nil {
		return err
	}
	a.changes = true
	return nil
}

func (a *transactionalConfigImport) validateNodes(ids []string) error {
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if _, err := configImportNodeID(id); err != nil || seen[id] {
			return errors.New("invalid or duplicate node assignment")
		}
		seen[id] = true
	}
	if len(ids) != 0 {
		var count int64
		if err := a.db.Model(&models.Client{}).Where("uuid IN ?", []string(ids)).Count(&count).Error; err != nil {
			return err
		}
		if count != int64(len(ids)) {
			return errors.New("assigned node does not exist")
		}
	}
	return nil
}

func (a *transactionalConfigImport) writeWindow(identity string, record map[string]any, create bool) error {
	if err := configImportFields(record, append(append([]string{}, exportWindowFields...), "config_uid")); err != nil {
		return err
	}
	uid, err := configImportNodeID(record["config_uid"])
	if err != nil || uid != identity {
		return errors.New("invalid window configuration identity")
	}
	var current models.MaintenanceWindow
	if !create {
		if err := a.db.Where("config_uid = ?", uid).Take(&current).Error; err != nil {
			return err
		}
	}
	value := current
	updates := configImportUpdates(record, "id")
	delete(updates, "config_uid")
	if err := decodeConfigRecord(updates, &value); err != nil {
		return err
	}
	if value.Name == "" || len(value.Name) > 100 || len(value.Reason) > 255 || value.Start.IsZero() || !value.End.After(value.Start) {
		return errors.New("invalid maintenance window")
	}
	if err := a.validateNodes(value.Clients); err != nil {
		return err
	}
	a.windows = true
	if create {
		value.ID = 0
		value.ConfigUID = uid
		return a.db.Create(&value).Error
	}
	if _, ok := updates["clients"]; ok {
		updates["clients"] = value.Clients
	}
	return a.db.Model(&models.MaintenanceWindow{}).Where("id = ?", current.ID).Updates(updates).Error
}

func (a *transactionalConfigImport) writeNotification(identity string, record map[string]any, create bool) error {
	if err := configImportFields(record, exportNotificationFields); err != nil {
		return err
	}
	id, err := configImportNodeID(record["client"])
	if err != nil || (!create && identity != id) {
		return errors.New("invalid notification identity")
	}
	if err := a.validateNodes([]string{id}); err != nil {
		return err
	}
	var value models.OfflineNotification
	if !create {
		if err := a.db.Where("client = ?", id).Take(&value).Error; err != nil {
			return err
		}
	}
	if err := decodeConfigRecord(record, &value); err != nil || value.GracePeriod < 0 {
		return errors.New("invalid notification configuration")
	}
	if create {
		zeroGrace := false
		if _, specified := record["grace_period"]; specified {
			zeroGrace = value.GracePeriod == 0
		}
		if err := a.db.Create(&value).Error; err != nil {
			return err
		}
		// GORM applies the model default to a zero-valued struct even when the
		// document explicitly requested no grace period.
		if zeroGrace {
			return a.db.Model(&models.OfflineNotification{}).Where("client = ?", id).Update("grace_period", 0).Error
		}
		return nil
	}
	updates := configImportUpdates(record, "client")
	return a.db.Model(&models.OfflineNotification{}).Where("client = ?", id).Updates(updates).Error
}

// draftImportConfig plans and applies inside one database transaction while
// holding node guards before task guards. Preview executes the same writes and
// validation but forces rollback, so it cannot promise an unexecutable plan.
func draftImportConfig(ctx context.Context, db *gorm.DB, document configxport.Document, preview bool, reload func() error) (configxport.ImportPlan, error) {
	return draftImportConfigWithReview(ctx, db, document, preview, reload, nil, nil)
}

// reviewedDraftImportConfig is a test integration point without an audit
// record. Apply must present the exact token from its preview.
func reviewedDraftImportConfig(ctx context.Context, db *gorm.DB, document configxport.Document, preview bool, token string, reload func() error) (configxport.ImportPlan, error) {
	if (preview && token != "") || (!preview && token == "") {
		return configxport.ImportPlan{}, errConfigImportPreviewStale
	}
	return draftImportConfigWithReview(ctx, db, document, preview, reload, &token, nil)
}

// The public handler supplies an audit record committed in the same
// transaction as an applied import.
func reviewedDraftImportConfigWithAudit(ctx context.Context, db *gorm.DB, document configxport.Document, preview bool, token string, reload func() error, audit *models.Log) (configxport.ImportPlan, error) {
	if (preview && token != "") || (!preview && (token == "" || audit == nil)) {
		return configxport.ImportPlan{}, errConfigImportPreviewStale
	}
	return draftImportConfigWithReview(ctx, db, document, preview, reload, &token, audit)
}

func draftImportConfigWithReview(ctx context.Context, db *gorm.DB, document configxport.Document, preview bool, reload func() error, review *string, audit *models.Log) (configxport.ImportPlan, error) {
	var plan configxport.ImportPlan
	if ctx == nil || db == nil || reload == nil {
		return plan, errors.New("invalid import context")
	}
	if len(document.Clients)+len(document.PingTasks)+len(document.MaintenanceWindows)+len(document.Settings)+len(document.NotificationConfigs) > maxConfigImportRecords {
		return plan, errors.New("configuration document has too many records")
	}
	// Old exports identify tasks and windows only by panel-local numbers. A
	// matching number on another panel is not evidence of the same record.
	// Other entities have portable identities and can use the same planner.
	reviewDocument := document
	if document.SchemaVersion == configxport.SchemaVersion || document.SchemaVersion == configxport.PinnedSchemaVersion {
		if len(document.PingTasks) != 0 || len(document.MaintenanceWindows) != 0 {
			return plan, errConfigImportLegacyIdentity
		}
		document.SchemaVersion = configxport.IdentitySchemaVersion
	} else if document.SchemaVersion != configxport.IdentitySchemaVersion {
		return plan, errors.New("unsupported configuration schema")
	}
	if _, ok := document.Settings[config.ApiKeyKey]; ok {
		return plan, errors.New("api_key is retired")
	}
	for key, value := range document.Settings {
		if key == "" || (!document.SecretsIncluded && !isOrdinaryExportSetting(key, value)) {
			return plan, errors.New("invalid or undeclared secret setting")
		}
	}
	nodes := make([]string, 0, len(document.Clients))
	clientFields := append([]string{}, exportClientFields...)
	if document.SecretsIncluded {
		clientFields = append(clientFields, "token")
	}
	for _, record := range document.Clients {
		if err := configImportFields(record, clientFields, "expired_at"); err != nil {
			return plan, err
		}
		id, err := configImportNodeID(record["uuid"])
		if err != nil {
			return plan, err
		}
		nodes = append(nodes, id)
	}
	for _, record := range document.PingTasks {
		fields := append(append([]string{}, exportPingTaskFields...), "target_family", "config_uid")
		if err := configImportFields(record, fields); err != nil {
			return plan, err
		}
		for key := range record {
			if hasReservedPingFamilyField(map[string]any{key: record[key]}) && (key != "target_family" || document.SchemaVersion < configxport.PinnedSchemaVersion) {
				return plan, errors.New("invalid or unversioned family field")
			}
		}
		if _, err := configImportNodeID(record["config_uid"]); err != nil {
			return plan, err
		}
		// Skipping validation for a planner-unchanged record must not let a
		// syntactically different value masquerade as an omitted family.
		if value, exists := record["target_family"]; exists {
			family, ok := value.(string)
			if !ok || (family != "" && family != "ipv4" && family != "ipv6") {
				return plan, errors.New("invalid target family")
			}
		}
		if _, exists := record["clients"]; !exists {
			return plan, errors.New("each imported ping task must specify its assignments")
		}
		if _, ok := record["id"]; ok {
			if _, err := configImportID(record["id"]); err != nil {
				return plan, err
			}
		}
		assigned, err := configImportReferencedNodes(record)
		if err != nil {
			return plan, err
		}
		nodes = append(nodes, assigned...)
	}
	for _, record := range document.MaintenanceWindows {
		if err := configImportFields(record, append(append([]string{}, exportWindowFields...), "config_uid")); err != nil {
			return plan, err
		}
		if _, err := configImportNodeID(record["config_uid"]); err != nil {
			return plan, err
		}
		if _, ok := record["id"]; ok {
			if _, err := configImportID(record["id"]); err != nil {
				return plan, err
			}
		}
		assigned, err := configImportReferencedNodes(record)
		if err != nil {
			return plan, err
		}
		nodes = append(nodes, assigned...)
	}
	for _, record := range document.NotificationConfigs {
		if err := configImportFields(record, exportNotificationFields); err != nil {
			return plan, err
		}
		id, err := configImportNodeID(record["client"])
		if err != nil {
			return plan, err
		}
		nodes = append(nodes, id)
	}
	var event config.ConfigEvent
	var needsReload, changedWindows bool
	rollbackPreview := errors.New("configuration preview rollback")
	apply := func() error {
		return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			reviewToken := ""
			if review != nil {
				var err error
				reviewToken, err = configImportReviewToken(tx, reviewDocument)
				if err != nil {
					return err
				}
				if !preview && !validConfigImportReview(*review, reviewToken) {
					return errConfigImportPreviewStale
				}
			}
			applier := &transactionalConfigImport{db: tx, document: document}
			// Secret setting values are only used when performing a write; the
			// general-purpose plan never needs to load or log them as SQL.
			if document.SecretsIncluded {
				applier.db = tx.Session(&gorm.Session{Logger: logger.Discard})
			}
			if err := validateConfigImportSettings(tx, document.Settings); err != nil {
				return err
			}
			for _, record := range document.Clients {
				token, exists := record["token"]
				if !exists {
					continue
				}
				var client models.Client
				id, _ := configImportNodeID(record["uuid"])
				if err := tx.Session(&gorm.Session{Logger: logger.Discard}).Select("uuid", "token").Where("uuid = ?", id).Take(&client).Error; errors.Is(err, gorm.ErrRecordNotFound) {
					continue
				} else if err != nil {
					return err
				}
				text, ok := token.(string)
				if !ok || !secretcmp.Equal(client.Token, text) {
					return errors.New("use rotateClientToken to change an existing agent credential")
				}
			}
			var err error
			plan, err = configxport.Apply(applier, document)
			if err != nil {
				return err
			}
			plan.SchemaVersion = reviewDocument.SchemaVersion
			plan.ReviewToken = reviewToken
			if reviewDocument.SchemaVersion != document.SchemaVersion {
				plan.Warnings = append(plan.Warnings, "legacy schema: documents with tasks or maintenance windows cannot be safely imported; numeric IDs are local to their original panel")
			}
			// Only tasks absent from the document may retain automatic
			// assignments made while creating nodes. Reconcile explicitly
			// imported assignments before checking the default-on invariant.
			explicitTasks := make(map[string]map[string]any, len(document.PingTasks))
			for _, record := range document.PingTasks {
				uid, _ := record["config_uid"].(string)
				explicitTasks[uid] = record
			}
			for uid := range applier.implicit {
				record, mentioned := explicitTasks[uid]
				if !mentioned {
					continue
				}
				delete(applier.implicit, uid)
				var current models.PingTask
				if err := tx.Where("config_uid = ?", uid).Take(&current).Error; err != nil {
					return err
				}
				assigned, err := configImportReferencedNodes(record)
				if err != nil {
					return err
				}
				if !sameConfigAssignments(current.Clients, assigned) {
					if err := applier.writePing(uid, record, false); err != nil {
						return err
					}
				}
			}
			// A default-on flag enabled by this same document was not visible
			// during node creation. Refuse an import that would skip those new
			// nodes instead of silently depending on entity application order.
			if len(applier.nodes) != 0 {
				for uid := range explicitTasks {
					var task models.PingTask
					if err := tx.Where("config_uid = ?", uid).Take(&task).Error; err != nil {
						return err
					}
					if task.DefaultOn {
						for _, id := range applier.nodes {
							if !task.AppliesToClient(id) {
								return errors.New("a new node is excluded from an imported default-on task")
							}
						}
					}
				}
				// An omitted default-on task can also acquire a new node. Do
				// not silently extend a legacy mixed-family hostname task.
				for uid := range applier.implicit {
					var task models.PingTask
					if err := tx.Where("config_uid = ?", uid).Take(&task).Error; err != nil {
						return err
					}
					if task.TargetFamily == "" {
						if err := utils.ValidatePingTaskTargetFamilyWithDB(tx, task.Target, task.DefaultOn, task.Clients); err != nil {
							return err
						}
					} else if !pingguard.ValidGeneration(task.PinnedGeneration) {
						return errors.New("invalid family-pinned task generation")
					}
				}
			}
			if len(applier.implicit) != 0 {
				ids := make([]string, 0, len(applier.implicit))
				for id := range applier.implicit {
					ids = append(ids, id)
				}
				sort.Strings(ids)
				for _, id := range ids {
					plan.Changes = append(plan.Changes, applier.implicit[id])
					plan.Updates++
				}
				plan.Warnings = append(plan.Warnings, "creating nodes also updates the listed omitted default-on tasks; missing records are never deleted")
			}
			if len(applier.settings) != 0 {
				event, err = config.SetManyTx(tx, applier.settings)
				if err != nil {
					return err
				}
			}
			needsReload = applier.changes
			changedWindows = applier.windows
			if preview {
				return rollbackPreview
			}
			if audit != nil {
				entry := *audit
				entry.ID = 0
				entry.Time = time.Now().UTC()
				if err := tx.Create(&entry).Error; err != nil {
					return errors.New("cannot persist configuration import audit")
				}
			}
			return nil
		})
	}
	withTasks := func() error {
		// Node creation can add default-on assignments even for tasks omitted
		// by the document. Hold every existing task guard until commit.
		var taskIDs []uint
		if err := db.WithContext(ctx).Model(&models.PingTask{}).Pluck("id", &taskIDs).Error; err != nil {
			return err
		}
		if len(taskIDs) == 0 {
			return apply()
		}
		return pingguard.WithMany(ctx, taskIDs, apply)
	}
	withNodes := func() error {
		if len(nodes) == 0 {
			return withTasks()
		}
		return clientguard.WithMany(ctx, nodes, withTasks)
	}
	err := pingguard.WithConfigImport(ctx, func() error { return pingguard.WithDefaultRoster(ctx, withNodes) })
	if preview && errors.Is(err, rollbackPreview) {
		return plan, nil
	}
	if err != nil {
		return plan, err
	}
	dbcache.InvalidateAll()
	config.PublishCommitted(event)
	if changedWindows {
		notifier.InvalidateMaintenanceWindows()
	}
	if needsReload {
		if err := reload(); err != nil {
			return plan, &configImportReloadError{err: err}
		}
	}
	return plan, nil
}

func sameConfigAssignments(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

type configImportReloadError struct{ err error }

func (e *configImportReloadError) Error() string {
	return "configuration committed but ping schedule reload failed: " + e.err.Error()
}
func (e *configImportReloadError) Unwrap() error { return e.err }
