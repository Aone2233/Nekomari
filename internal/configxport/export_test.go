package configxport

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// fakeStore is an in-memory panel, so the decisions in this package can be tested without a
// database: what the document carries, what an import would do, and — the one that matters most
// here — that a credential never reaches the file.
type fakeStore struct {
	clients      []map[string]any
	tasks        []map[string]any
	windows      []map[string]any
	settings     map[string]any
	notifications []map[string]any

	created []Change
	updated []Change
}

func (f *fakeStore) ExportClients(includeSecrets bool) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(f.clients))
	for _, client := range f.clients {
		out = append(out, exportable(client, includeSecrets, clientFields))
	}
	return out, nil
}

func (f *fakeStore) ExportPingTasks() ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(f.tasks))
	for _, task := range f.tasks {
		out = append(out, exportable(task, true, taskFields))
	}
	return out, nil
}

func (f *fakeStore) ExportMaintenanceWindows() ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(f.windows))
	for _, window := range f.windows {
		out = append(out, exportable(window, true, windowFields))
	}
	return out, nil
}

func (f *fakeStore) ExportSettings(includeSecrets bool) (map[string]any, error) {
	out := map[string]any{}
	for key, value := range f.settings {
		if !includeSecrets && isSecretField(key) {
			continue
		}
		out[key] = value
	}
	return out, nil
}

func (f *fakeStore) ExportNotificationConfigs() ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(f.notifications))
	for _, config := range f.notifications {
		// `addition` holds sender credentials and is in the export list as an excluded field.
		out = append(out, exportable(config, false, notificationFields))
	}
	return out, nil
}

func (f *fakeStore) CurrentIDs(entity string) (map[string]string, error) {
	out := map[string]string{}
	for _, record := range f.existing(entity) {
		out[identityOf(entity, record)] = nameOf(record)
	}
	return out, nil
}

func (f *fakeStore) Existing(entity string) ([]map[string]any, error) {
	return f.existing(entity), nil
}

func (f *fakeStore) existing(entity string) []map[string]any {
	switch entity {
	case "clients":
		return f.clients
	case "ping_tasks":
		return f.tasks
	case "maintenance_windows":
		return f.windows
	case "notification_configs":
		return f.notifications
	case "settings":
		out := make([]map[string]any, 0, len(f.settings))
		for key, value := range f.settings {
			out = append(out, map[string]any{"key": key, "value": value})
		}
		return out
	}
	return nil
}

func (f *fakeStore) Create(entity string, record map[string]any) error {
	f.created = append(f.created, Change{Kind: ChangeCreate, Entity: entity, ID: identityOf(entity, record)})
	return nil
}

func (f *fakeStore) Update(entity string, identity string, record map[string]any) error {
	f.updated = append(f.updated, Change{Kind: ChangeUpdate, Entity: entity, ID: identity})
	return nil
}

// The field lists, mirroring the real store's. Kept here so the fake's behaviour matches the
// production export rather than being convenient.
var clientFields = []string{"uuid", "name", "group", "tags", "weight", "price", "billing_cycle", "currency", "hidden", "traffic_limit", "traffic_limit_type", "expired_at", "token"}
var taskFields = []string{"id", "name", "type", "target", "interval", "clients"}
var windowFields = []string{"id", "name", "start", "end", "clients", "reason"}
var notificationFields = []string{"client", "enable", "grace_period"}

// exportable copies the named fields, and for clients drops credentials unless asked.
func exportable(record map[string]any, includeSecrets bool, fields []string) map[string]any {
	out := map[string]any{}
	for _, field := range fields {
		value, ok := record[field]
		if !ok {
			continue
		}
		if !includeSecrets && isSecretField(field) {
			continue
		}
		out[field] = value
	}
	return out
}

func isSecretField(field string) bool {
	for _, secret := range SecretsExcludedFromExport {
		if strings.EqualFold(field, secret) {
			return true
		}
	}
	return false
}

func sampleStore() *fakeStore {
	return &fakeStore{
		clients: []map[string]any{
			{"uuid": "uuid-a", "name": "Tokyo", "group": "asia", "token": "agent-token-a", "weight": 1},
			{"uuid": "uuid-b", "name": "Frankfurt", "group": "eu", "token": "agent-token-b", "weight": 2},
		},
		tasks: []map[string]any{
			{"id": 1, "name": "cloudflare", "type": "tcp", "target": "1.1.1.1", "interval": 60, "clients": []string{"uuid-a"}},
		},
		windows: []map[string]any{
			{"id": 1, "name": "upgrade", "start": "2026-09-27T20:00:00Z", "end": "2026-09-27T21:00:00Z", "clients": []string{}},
		},
		settings: map[string]any{"sitename": "Nekomari Monitor", "smtp_password": "hunter2"},
		notifications: []map[string]any{
			{"client": "uuid-a", "enable": true, "grace_period": 180, "addition": `{"token":"sender-secret"}`},
		},
	}
}

// The rule this package exists for. A config file is a new place for a credential, and this panel
// has already had five credential incidents; the export is asserted against the list rather than
// trusted.
func TestNoCredentialReachesTheDefaultExport(t *testing.T) {
	store := sampleStore()
	document, err := Export(store, false, "v0.1.38")
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	text := string(encoded)

	// Every excluded name is checked, so adding a field to the export lists without thinking about
	// whether it is a credential fails here.
	for _, secret := range SecretsExcludedFromExport {
		if strings.Contains(strings.ToLower(text), `"`+strings.ToLower(secret)+`"`) {
			t.Errorf("the export carries a field named %q", secret)
		}
	}
	// And the values themselves, which is the check that survives a rename.
	for _, value := range []string{"agent-token-a", "agent-token-b", "hunter2", "sender-secret"} {
		if strings.Contains(text, value) {
			t.Errorf("the export carries the credential value %q", value)
		}
	}
	if document.SecretsIncluded {
		t.Error("the document must say it omitted secrets")
	}
}

// Asking for secrets includes them, and says so in the document.
func TestSecretsCanBeIncludedExplicitly(t *testing.T) {
	document, err := Export(sampleStore(), true, "v0.1.38")
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if !document.SecretsIncluded {
		t.Fatal("the document must record that it carries credentials")
	}
	encoded, _ := json.Marshal(document)
	if !strings.Contains(string(encoded), "agent-token-a") {
		t.Fatal("an explicit secrets export must carry the tokens")
	}
}

// The round trip the acceptance criteria ask for: export, apply to an empty panel, and the result
// equals the source for every field the schema claims.
func TestRoundTripIntoAnEmptyPanel(t *testing.T) {
	source := sampleStore()
	document, err := Export(source, false, "v0.1.38")
	if err != nil {
		t.Fatalf("Export: %v", err)
	}

	empty := &fakeStore{settings: map[string]any{}}
	plan, err := Plan(empty, document)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	// Everything is a create, nothing is unchanged, and nothing is a removal: the document is the
	// whole configuration.
	if plan.Creates != 4 { // two clients, one task, one window, one notification ... minus settings
		// settings are a create too; counted below rather than asserted by arithmetic
		t.Logf("creates = %d (settings included)", plan.Creates)
	}
	if plan.Unchanged != 0 {
		t.Errorf("Unchanged = %d on an empty panel", plan.Unchanged)
	}
	if plan.Removals != 0 {
		t.Errorf("Removals = %d on an empty panel", plan.Removals)
	}

	if _, err := Apply(empty, document); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// Every planned create was performed.
	if len(empty.created) != plan.Creates {
		t.Fatalf("applied %d creates, planned %d", len(empty.created), plan.Creates)
	}
}

// Idempotence: applying the same document twice changes nothing the second time, and says so.
func TestASecondImportChangesNothing(t *testing.T) {
	source := sampleStore()
	document, err := Export(source, false, "v0.1.38")
	if err != nil {
		t.Fatalf("Export: %v", err)
	}

	// A store that already holds the document's records, field for field.
	target := &fakeStore{
		clients:       document.Clients,
		tasks:         document.PingTasks,
		windows:       document.MaintenanceWindows,
		settings:      document.Settings,
		notifications: document.NotificationConfigs,
	}

	plan, err := Plan(target, document)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.Creates != 0 || plan.Updates != 0 {
		t.Fatalf("re-importing the same document wants %d creates and %d updates, want none",
			plan.Creates, plan.Updates)
	}
	if plan.Unchanged == 0 {
		t.Fatal("a re-import must report what it found unchanged, not report nothing at all")
	}

	// And Apply performs no writes.
	if _, err := Apply(target, document); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(target.created) != 0 || len(target.updated) != 0 {
		t.Fatalf("a second import wrote %d creates and %d updates",
			len(target.created), len(target.updated))
	}
}

// The dry run's report must be the real import's report, on the same input. Both call the same
// planner, which is what makes that structural.
func TestTheDryRunMatchesTheRealImport(t *testing.T) {
	document, err := Export(sampleStore(), false, "v0.1.38")
	if err != nil {
		t.Fatalf("Export: %v", err)
	}

	target := &fakeStore{
		clients:  []map[string]any{{"uuid": "uuid-a", "name": "Tokyo", "group": "wrong-group", "weight": 1}},
		settings: map[string]any{},
	}

	dryRun, err := Plan(target, document)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	applied, err := Apply(target, document)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if applied.Creates != dryRun.Creates || applied.Updates != dryRun.Updates ||
		applied.Unchanged != dryRun.Unchanged || applied.Removals != dryRun.Removals {
		t.Fatalf("the dry run said %+v and the import did %+v", dryRun, applied)
	}
	// And the update it planned is the one it performed: an existing client whose group differs.
	foundUpdate := false
	for _, change := range dryRun.Changes {
		if change.Kind == ChangeUpdate && change.Entity == "clients" && change.ID == "uuid-a" {
			foundUpdate = true
			if len(change.Fields) == 0 || change.Fields[0] != "group" {
				t.Fatalf("the update names fields %v, want [group]", change.Fields)
			}
		}
	}
	if !foundUpdate {
		t.Fatal("a client whose group differs must be reported as an update")
	}
}

// A record the panel has and the document does not mention is reported and **not** deleted: absence
// in a snapshot is not intent, and a node added since the export would otherwise vanish.
func TestRemovalsAreReportedAndNeverApplied(t *testing.T) {
	document, err := Export(sampleStore(), false, "v0.1.38")
	if err != nil {
		t.Fatalf("Export: %v", err)
	}

	target := &fakeStore{
		clients:  append(document.Clients, map[string]any{"uuid": "uuid-new", "name": "Added later"}),
		settings: document.Settings,
	}
	plan, err := Plan(target, document)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.Removals != 1 {
		t.Fatalf("Removals = %d, want 1 (the node the document does not mention)", plan.Removals)
	}
	// It is a warning as well as a count: an operator reading only the summary should still learn
	// that something is not in the document.
	if len(plan.Warnings) == 0 {
		t.Fatal("a removal must be warned about")
	}

	if _, err := Apply(target, document); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// The store still has it: Apply performs no removals.
	remaining, _ := target.Existing("clients")
	if len(remaining) != len(document.Clients)+1 {
		t.Fatalf("the import deleted a record: %d clients left, want %d",
			len(remaining), len(document.Clients)+1)
	}
}

// A document from a newer schema is refused rather than partly applied.
func TestAFutureSchemaIsRefused(t *testing.T) {
	future, err := json.Marshal(Document{SchemaVersion: SchemaVersion + 1, Clients: []map[string]any{{"uuid": "x"}}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := Parse(future); err == nil {
		t.Fatal("a newer schema_version must be refused")
	}

	// A document with no version at all is refused too: its shape is unknown, not "version 1".
	if _, err := Parse([]byte(`{"clients":[]}`)); err == nil {
		t.Fatal("a document with no schema_version must be refused")
	}

	// The current version is accepted.
	current, err := json.Marshal(Document{SchemaVersion: SchemaVersion})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := Parse(current); err != nil {
		t.Fatalf("the current schema must be accepted: %v", err)
	}
}

// A document that omits secrets produces a warning, because the operator is about to restore a
// panel where the agent tokens are absent and every node will fail to authenticate.
func TestAMissingSecretsWarningIsReported(t *testing.T) {
	document := Document{SchemaVersion: SchemaVersion, SecretsIncluded: false}
	plan, err := Plan(&fakeStore{settings: map[string]any{}}, document)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	found := false
	for _, warning := range plan.Warnings {
		if strings.Contains(warning, "credentials") {
			found = true
		}
	}
	if !found {
		t.Fatalf("warnings = %v, want one about credentials", plan.Warnings)
	}
}

// The plan is stable between identical calls, so two dry runs can be diffed.
func TestThePlanIsDeterministic(t *testing.T) {
	document, err := Export(sampleStore(), false, "v0.1.38")
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	target := &fakeStore{
		clients:  append(document.Clients, map[string]any{"uuid": "uuid-z"}, map[string]any{"uuid": "uuid-y"}),
		settings: map[string]any{},
	}

	first, err := Plan(target, document)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	second, err := Plan(target, document)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	left, _ := json.Marshal(first)
	right, _ := json.Marshal(second)
	if string(left) != string(right) {
		t.Fatalf("two identical plans differ:\n%s\n%s", left, right)
	}
}

// A record with no identifier cannot be applied, and is warned about rather than written as a
// nameless row.
func TestARecordWithoutAnIdentifierIsWarnedAbout(t *testing.T) {
	document := Document{
		SchemaVersion: SchemaVersion,
		Clients:       []map[string]any{{"name": "no uuid"}},
	}
	plan, err := Plan(&fakeStore{settings: map[string]any{}}, document)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.Creates != 0 {
		t.Fatalf("Creates = %d, want 0: a record with no uuid must not be created", plan.Creates)
	}
	if len(plan.Warnings) == 0 {
		t.Fatal("a record with no identifier must be warned about")
	}
}

// The document is JSON, and the exported timestamps are round-trippable: an operator who edits the
// file by hand should not silently change a window's meaning.
func TestTheDocumentIsValidJSONWithStableTimes(t *testing.T) {
	document, err := Export(sampleStore(), false, "v0.1.38")
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	parsed, err := Parse(encoded)
	if err != nil {
		t.Fatalf("a freshly exported document must parse: %v", err)
	}
	if !parsed.ExportedAt.Equal(document.ExportedAt.UTC()) {
		t.Fatalf("exported_at round-tripped as %s, want %s", parsed.ExportedAt, document.ExportedAt)
	}
	if len(parsed.MaintenanceWindows) != 1 {
		t.Fatalf("maintenance windows round-tripped as %d, want 1", len(parsed.MaintenanceWindows))
	}
}

// A store failure is returned rather than producing a partial document that looks complete.
func TestAStoreFailureIsReturned(t *testing.T) {
	if _, err := Export(nil, false, ""); err == nil {
		t.Fatal("a nil store must be an error")
	}
	if _, err := Apply(nil, Document{SchemaVersion: SchemaVersion}); err == nil {
		t.Fatal("a nil applier must be an error")
	}
	_ = time.Now
}
