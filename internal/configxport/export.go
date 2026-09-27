package configxport

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// Store is the slice of the panel this package reads and writes.
//
// An interface rather than direct database access, for the same reason the SLA and bulk packages
// take one: the decisions worth testing are "what goes in the document" and "what would an import
// do", and both can be driven with a fake. The real implementation lives in the RPC handler, where
// the database is.
type Store interface {
	// ExportClients returns every client as a map of *exportable* fields. The store is responsible
	// for leaving credentials out; the field lists here are what a test asserts against.
	ExportClients(includeSecrets bool) ([]map[string]any, error)
	ExportPingTasks() ([]map[string]any, error)
	ExportMaintenanceWindows() ([]map[string]any, error)
	ExportSettings(includeSecrets bool) (map[string]any, error)
	ExportNotificationConfigs() ([]map[string]any, error)

	// CurrentIDs returns the identifiers the panel holds per entity, for the removal report.
	CurrentIDs(entity string) (map[string]string, error)

	// Existing returns one record per identity, for the planner to compare against.
	Existing(entity string) ([]map[string]any, error)
}

// Applier performs the writes a plan describes.
type Applier interface {
	Store
	// Create inserts a record for an entity.
	Create(entity string, record map[string]any) error
	// Update changes the fields a record differs by.
	Update(entity string, identity string, record map[string]any) error
}

// Entities in the order they are reported and applied.
//
// Clients first: a ping task references nodes, and a window does too. An import that created tasks
// before nodes would leave tasks pointing at nothing, which the validation would then refuse.
var Entities = []string{"clients", "ping_tasks", "maintenance_windows", "settings", "notification_configs"}

// identityField is the field that identifies a record within an entity.
func identityField(entity string) string {
	switch entity {
	case "settings":
		return "key"
	default:
		return "uuid"
	}
}

// Export reads the whole configuration into one document.
//
// `includeSecrets` is threaded through to the store rather than applied here: the store is what knows
// which columns are credentials, and this package's job is to be explicit about which *fields* it
// asks for.
func Export(store Store, includeSecrets bool, sourceVersion string) (Document, error) {
	if store == nil {
		return Document{}, fmt.Errorf("no store")
	}
	document := Document{
		SchemaVersion:   SchemaVersion,
		ExportedAt:      time.Now().UTC(),
		SecretsIncluded: includeSecrets,
		SourceVersion:   sourceVersion,
	}

	var err error
	if document.Clients, err = store.ExportClients(includeSecrets); err != nil {
		return Document{}, fmt.Errorf("export clients: %w", err)
	}
	if document.PingTasks, err = store.ExportPingTasks(); err != nil {
		return Document{}, fmt.Errorf("export ping tasks: %w", err)
	}
	if document.MaintenanceWindows, err = store.ExportMaintenanceWindows(); err != nil {
		return Document{}, fmt.Errorf("export maintenance windows: %w", err)
	}
	if document.Settings, err = store.ExportSettings(includeSecrets); err != nil {
		return Document{}, fmt.Errorf("export settings: %w", err)
	}
	if document.NotificationConfigs, err = store.ExportNotificationConfigs(); err != nil {
		return Document{}, fmt.Errorf("export notification configs: %w", err)
	}
	return document, nil
}

// recordsFor returns the document's records for one entity.
func recordsFor(document Document, entity string) []map[string]any {
	switch entity {
	case "clients":
		return document.Clients
	case "ping_tasks":
		return document.PingTasks
	case "maintenance_windows":
		return document.MaintenanceWindows
	case "notification_configs":
		return document.NotificationConfigs
	case "settings":
		// Settings is a map in the document and a list of records everywhere else, because the
		// planner compares records. The conversion is here so everything downstream sees one shape.
		out := make([]map[string]any, 0, len(document.Settings))
		for key, value := range document.Settings {
			out = append(out, map[string]any{"key": key, "value": value})
		}
		sort.Slice(out, func(i, j int) bool {
			return fmt.Sprint(out[i]["key"]) < fmt.Sprint(out[j]["key"])
		})
		return out
	}
	return nil
}

// identityOf reads a record's identifier.
func identityOf(entity string, record map[string]any) string {
	if entity == "settings" {
		return fmt.Sprint(record["key"])
	}
	return fmt.Sprint(record["uuid"])
}

// nameOf reads a record's display name, for the report.
func nameOf(record map[string]any) string {
	for _, key := range []string{"name", "key", "uuid"} {
		if value, ok := record[key]; ok && value != nil && fmt.Sprint(value) != "" {
			return fmt.Sprint(value)
		}
	}
	return ""
}

// differs returns the fields in which the document's record and the stored one disagree.
//
// Only fields the document mentions are compared. A stored record has columns the export does not
// carry — a client's hardware facts, its token, timestamps — and treating those as differences would
// make every import report an update it should not.
func differs(entity string, wanted, stored map[string]any) []string {
	fields := make([]string, 0, len(wanted))
	for key, want := range wanted {
		if key == identityField(entity) {
			continue
		}
		// Settings compare on their value, which is nested rather than flat.
		if entity == "settings" && key == "value" {
			if !sameJSON(want, stored["value"]) {
				fields = append(fields, key)
			}
			continue
		}
		have, ok := stored[key]
		if !ok {
			// The document carries a field the panel does not store. Reported as a difference so the
			// operator sees it, and as a warning separately: it usually means the document came from
			// a build with a field this one does not have.
			fields = append(fields, key)
			continue
		}
		if !sameJSON(want, have) {
			fields = append(fields, key)
		}
	}
	sort.Strings(fields)
	return fields
}

// sameJSON compares two decoded JSON values by their canonical encoding, so a number that arrived as
// float64 and one stored as int compare equal when they are the same number.
func sameJSON(a, b any) bool {
	left, errLeft := json.Marshal(a)
	right, errRight := json.Marshal(b)
	if errLeft != nil || errRight != nil {
		return false
	}
	return string(left) == string(right)
}

// Plan computes what applying the document would do, without doing any of it.
//
// Both the dry run and the real import call this, which is what makes the promise "the dry run's
// report matches what the real import does" structural rather than a matter of keeping two code
// paths in step.
func Plan(store Store, document Document) (ImportPlan, error) {
	if store == nil {
		return ImportPlan{}, fmt.Errorf("no store")
	}
	plan := ImportPlan{SchemaVersion: document.SchemaVersion, Changes: make([]Change, 0)}

	if !document.SecretsIncluded {
		plan.Warnings = append(plan.Warnings,
			"the document does not carry credentials, so agent tokens and notification credentials are not restored by it")
	}

	for _, entity := range Entities {
		stored, err := store.Existing(entity)
		if err != nil {
			return ImportPlan{}, fmt.Errorf("read stored %s: %w", entity, err)
		}
		byIdentity := make(map[string]map[string]any, len(stored))
		for _, record := range stored {
			byIdentity[identityOf(entity, record)] = record
		}

		wanted := recordsFor(document, entity)
		seen := make(map[string]bool, len(wanted))
		for _, record := range wanted {
			identity := identityOf(entity, record)
			if identity == "" || identity == "<nil>" {
				plan.Warnings = append(plan.Warnings,
					fmt.Sprintf("%s: a record has no %s and cannot be applied", entity, identityField(entity)))
				continue
			}
			seen[identity] = true
			existing, ok := byIdentity[identity]
			if !ok {
				plan.Creates++
				plan.Changes = append(plan.Changes, Change{
					Kind: ChangeCreate, Entity: entity, ID: identity, Name: nameOf(record),
				})
				continue
			}
			fields := differs(entity, record, existing)
			if len(fields) == 0 {
				plan.Unchanged++
				plan.Changes = append(plan.Changes, Change{
					Kind: ChangeUnchanged, Entity: entity, ID: identity, Name: nameOf(record),
				})
				continue
			}
			plan.Updates++
			plan.Changes = append(plan.Changes, Change{
				Kind: ChangeUpdate, Entity: entity, ID: identity, Name: nameOf(record), Fields: fields,
			})
		}

		// Removals are reported and never applied — see ChangeRemove. Sorted, so the report is
		// stable between identical calls.
		removals := make([]string, 0)
		for identity := range byIdentity {
			if !seen[identity] {
				removals = append(removals, identity)
			}
		}
		sort.Strings(removals)
		for _, identity := range removals {
			plan.Removals++
			plan.Changes = append(plan.Changes, Change{
				Kind: ChangeRemove, Entity: entity, ID: identity, Name: nameOf(byIdentity[identity]),
			})
		}
	}

	if plan.Removals > 0 {
		plan.Warnings = append(plan.Warnings,
			fmt.Sprintf("%d record(s) are in the panel and not in the document; an import does not delete them", plan.Removals))
	}
	return plan, nil
}

// Apply performs the plan's creates and updates.
//
// Removals are deliberately not performed: absence in a snapshot is not intent. See ChangeRemove.
func Apply(applier Applier, document Document) (ImportPlan, error) {
	if applier == nil {
		return ImportPlan{}, fmt.Errorf("no applier")
	}
	plan, err := Plan(applier, document)
	if err != nil {
		return ImportPlan{}, err
	}

	for _, change := range plan.Changes {
		switch change.Kind {
		case ChangeCreate:
			record, ok := findRecord(document, change.Entity, change.ID)
			if !ok {
				return plan, fmt.Errorf("%s %s vanished from the document between planning and applying", change.Entity, change.ID)
			}
			if err := applier.Create(change.Entity, record); err != nil {
				return plan, fmt.Errorf("create %s %s: %w", change.Entity, change.ID, err)
			}
		case ChangeUpdate:
			record, ok := findRecord(document, change.Entity, change.ID)
			if !ok {
				return plan, fmt.Errorf("%s %s vanished from the document between planning and applying", change.Entity, change.ID)
			}
			if err := applier.Update(change.Entity, change.ID, record); err != nil {
				return plan, fmt.Errorf("update %s %s: %w", change.Entity, change.ID, err)
			}
		case ChangeUnchanged, ChangeRemove:
			// Nothing to do: unchanged by definition, and removal is never automatic.
		}
	}
	return plan, nil
}

// findRecord locates one record in the document.
func findRecord(document Document, entity, identity string) (map[string]any, bool) {
	for _, record := range recordsFor(document, entity) {
		if identityOf(entity, record) == identity {
			return record, true
		}
	}
	return nil, false
}
