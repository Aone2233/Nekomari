// Package configxport turns the panel's configuration into one JSON document and applies one back.
//
// Why the field lists are explicit rather than struct tags: a document that carries every column
// of every table will eventually carry a credential — this panel has already had five credential
// incidents (docs/SECRETS.md), and a config file is a new place for a sixth. Writing the exported
// fields out by hand means a new field is *absent by default* and has to be added deliberately,
// which is the failure direction that does not leak. It also means `client.token`, a sender's
// credentials and an account's 2FA secret are excluded because nobody listed them, rather than
// because a tag was remembered.
//
// Three rules the import keeps, from the roadmap's acceptance criteria:
//
//   - **A dry run reports what the real import would do, on the same input.** Both run the same
//     planner; the dry run only skips the writes.
//   - **It is idempotent.** Applying the same document twice changes nothing the second time, and
//     says so, because the planner compares what is stored with what the document asks for.
//   - **A document from a newer schema is refused rather than partly applied.** A future version
//     may have redefined a field, and half-applying it would leave a configuration nobody designed.
package configxport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// SchemaVersion is the legacy document format.
//
// Refusing a newer document is deliberate and is the reason this is a version rather than a
// timestamp: an importer that guessed at an unfamiliar shape would apply part of it.
const SchemaVersion = 1

// PinnedSchemaVersion prevents older importers from treating a family-pinned
// task as a legacy task. The newer identity schema also preserves pins, but
// no exported document includes a runtime generation.
const PinnedSchemaVersion = 2

// IdentitySchemaVersion identifies numeric records independently of their
// panel-local IDs, so two installations cannot overwrite unrelated records.
const IdentitySchemaVersion = 3

// Document is the exported configuration.
type Document struct {
	SchemaVersion int       `json:"schema_version"`
	ExportedAt    time.Time `json:"exported_at"`
	// SecretsIncluded says whether the document carries credentials. It is written even when
	// false, so a reader of the file can tell an export that omitted them from one that had none
	// to omit.
	SecretsIncluded bool `json:"secrets_included"`
	// SourceVersion is the panel build that wrote it, for a human reading a diff.
	SourceVersion string `json:"source_version,omitempty"`

	Clients             []map[string]any `json:"clients"`
	PingTasks           []map[string]any `json:"ping_tasks"`
	MaintenanceWindows  []map[string]any `json:"maintenance_windows"`
	Settings            map[string]any   `json:"settings,omitempty"`
	NotificationConfigs []map[string]any `json:"notification_configs"`
}

// ChangeKind is what the planner would do to one record.
type ChangeKind string

const (
	// ChangeCreate is a record the panel does not have.
	ChangeCreate ChangeKind = "create"
	// ChangeUpdate is a record that exists and differs.
	ChangeUpdate ChangeKind = "update"
	// ChangeUnchanged is a record the panel already has in this exact shape. Reported rather than
	// omitted, because "nothing to do" is the answer a second import should give and a silent list
	// would look like the import had failed.
	ChangeUnchanged ChangeKind = "unchanged"
	// ChangeRemove is a record the panel has and the document does not mention.
	//
	// Never applied automatically. Removing a node is not a configuration change to make by
	// re-importing a file: the document is a snapshot, and a node added since it was written would
	// be deleted by an import that treated absence as intent. It is reported so the operator can
	// see the difference and decide, which is the whole reason the dry run exists.
	ChangeRemove ChangeKind = "remove"
)

// Change is one planned action.
type Change struct {
	Kind   ChangeKind `json:"kind"`
	Entity string     `json:"entity"`
	// ID identifies the record within its entity: schema 3 uses a stable UUID
	// for tasks and windows instead of a panel-local numeric ID.
	ID     string   `json:"id"`
	Name   string   `json:"name,omitempty"`
	Fields []string `json:"fields,omitempty"`
	// Effect distinguishes a side effect of importing another record from a
	// direct document update. No secret values or runtime generation are sent.
	Effect string `json:"effect,omitempty"`
}

// ImportPlan is what an import would do.
type ImportPlan struct {
	SchemaVersion int `json:"schema_version"`
	// ReviewToken binds an executable preview to the document and panel state.
	// It is omitted when the caller did not request a reviewed import.
	ReviewToken string   `json:"review_token,omitempty"`
	Creates     int      `json:"creates"`
	Updates     int      `json:"updates"`
	Unchanged   int      `json:"unchanged"`
	Removals    int      `json:"removals"`
	Changes     []Change `json:"changes"`
	// Warnings are things the operator should know but that are not refusals: a document that
	// omits secrets, an empty entity, a field the panel does not recognise.
	Warnings []string `json:"warnings,omitempty"`
}

// Parse reads a document and refuses one this build cannot understand.
func Parse(data []byte) (Document, error) {
	var document Document
	if err := RejectAmbiguousJSON(data); err != nil {
		return Document{}, err
	}
	// encoding/json also matches struct fields case-insensitively. Such aliases
	// could override a displayed canonical key even though they differ by case.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return Document{}, fmt.Errorf("not a configuration document: expected an object")
	}
	for key := range fields {
		switch key {
		case "schema_version", "exported_at", "secrets_included", "source_version", "clients", "ping_tasks", "maintenance_windows", "settings", "notification_configs":
		default:
			return Document{}, fmt.Errorf("not a configuration document: unknown envelope field %q", key)
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		return Document{}, fmt.Errorf("not a configuration document: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Document{}, fmt.Errorf("not a configuration document: unexpected trailing data")
	}
	if document.SchemaVersion <= 0 {
		return Document{}, fmt.Errorf("the document has no schema_version, so its shape is unknown")
	}
	if document.SchemaVersion > IdentitySchemaVersion {
		return Document{}, fmt.Errorf(
			"the document is schema_version %d and this panel understands %d: refusing rather than applying part of a format it does not know",
			document.SchemaVersion, IdentitySchemaVersion)
	}
	return document, nil
}

// Duplicate keys can make the displayed document disagree with the values
// the importer sees. Bound nesting before the normal typed decode as well.
func RejectAmbiguousJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value func(int) error
	value = func(depth int) error {
		if depth > 64 {
			return fmt.Errorf("configuration document is nested too deeply")
		}
		token, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("invalid configuration document")
		}
		delim, composite := token.(json.Delim)
		if !composite {
			return nil
		}
		switch delim {
		case '{':
			seen := make(map[string]struct{})
			for decoder.More() {
				key, err := decoder.Token()
				name, ok := key.(string)
				if err != nil || !ok {
					return fmt.Errorf("invalid configuration document")
				}
				if _, duplicate := seen[name]; duplicate {
					return fmt.Errorf("configuration document contains a duplicate key")
				}
				seen[name] = struct{}{}
				if err := value(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			for decoder.More() {
				if err := value(depth + 1); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("invalid configuration document")
		}
		if _, err := decoder.Token(); err != nil {
			return fmt.Errorf("invalid configuration document")
		}
		return nil
	}
	if err := value(0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("not a configuration document: unexpected trailing data")
	}
	return nil
}

// SecretsExcludedFromExport lists the fields an export omits unless secrets are requested.
//
// It is here to be *asserted against*: a test greps an export for each name, so adding a field to
// the export lists without thinking about whether it is a credential fails, and the list doubles as
// the answer to "what does this feature consider a secret".
var SecretsExcludedFromExport = []string{
	"token",         // a client's agent token
	"two_factor",    // an account's TOTP secret
	"passwd",        // an account's password hash
	"addition",      // a notification sender's credentials
	"secret",        // OIDC client secret
	"client_secret", // the same, spelled out
	"smtp_password", // mail credentials, when they live in settings
	"password",      // any other password
	"api_key",       // provider keys
}
