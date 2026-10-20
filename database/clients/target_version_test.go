package clients

import (
	"testing"

	"github.com/Aone2233/nekomari/cmd/flags"
	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
)

// newTargetVersionClient creates one client row in the shared test database and
// returns its UUID. The rollout target is a per-node column, so every case needs
// a real row to write it to.
func newTargetVersionClient(t *testing.T, uuid string) {
	t.Helper()
	flags.DatabaseType = flags.DatabaseTypeSQLite
	flags.DatabaseFile = "file:clients-target-version?mode=memory&cache=shared"
	db := dbcore.GetDBInstance()
	if err := db.Create(&models.Client{UUID: uuid, Token: "token-" + uuid, Name: uuid}).Error; err != nil {
		t.Fatalf("create client %s: %v", uuid, err)
	}
}

// A pinned target must survive as a row, because it is what makes a rollout
// outlive a panel restart. An empty target clears the pin.
func TestTargetVersionRoundTripsAndClears(t *testing.T) {
	uuid := "client-target-version-roundtrip"
	newTargetVersionClient(t, uuid)

	stored, err := GetTargetVersion(uuid)
	if err != nil {
		t.Fatalf("read cleared target: %v", err)
	}
	if stored != "" {
		t.Fatalf("fresh client target = %q, want empty", stored)
	}

	if err := SetTargetVersion(uuid, "v0.1.41"); err != nil {
		t.Fatalf("set target: %v", err)
	}
	stored, err = GetTargetVersion(uuid)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if stored != "v0.1.41" {
		t.Fatalf("target = %q, want v0.1.41", stored)
	}

	// The value is written to the column, not kept in a package variable: a
	// second read through the model must see it.
	var row models.Client
	if err := dbcore.GetDBInstance().Where("uuid = ?", uuid).First(&row).Error; err != nil {
		t.Fatalf("load client: %v", err)
	}
	if row.TargetVersion != "v0.1.41" {
		t.Fatalf("persisted column = %q, want v0.1.41", row.TargetVersion)
	}

	// Whitespace-only input is a clear, matching the agent's own trimming.
	if err := SetTargetVersion(uuid, "   "); err != nil {
		t.Fatalf("clear target: %v", err)
	}
	stored, err = GetTargetVersion(uuid)
	if err != nil {
		t.Fatalf("read cleared target: %v", err)
	}
	if stored != "" {
		t.Fatalf("cleared target = %q, want empty", stored)
	}
}

// An unknown UUID must be an error rather than a silent "no pin": a stale
// selection would otherwise look like a node that simply follows releases.
func TestTargetVersionRejectsUnknownClients(t *testing.T) {
	if err := SetTargetVersion("client-target-version-missing", "v0.1.41"); err == nil {
		t.Fatal("setting a target on a missing client succeeded")
	}
	if _, err := GetTargetVersion("client-target-version-missing"); err == nil {
		t.Fatal("reading a missing client succeeded")
	}
}

// The pending marker is the difference between "the target is empty" and "the
// node has been told the target is empty". An empty target alone cannot express
// the second state, and without it a node that was offline during a clear keeps
// running the old pin until it restarts.
func TestAgentUpdateStateTracksPendingClear(t *testing.T) {
	uuid := "client-target-version-pending"
	newTargetVersionClient(t, uuid)

	target, pending, err := AgentUpdateState(uuid)
	if err != nil {
		t.Fatalf("read fresh state: %v", err)
	}
	if target != "" || pending {
		t.Fatalf("fresh state = (%q, %v), want empty and settled", target, pending)
	}

	if err := SetAgentUpdateState(uuid, "", true); err != nil {
		t.Fatalf("record pending clear: %v", err)
	}
	target, pending, err = AgentUpdateState(uuid)
	if err != nil {
		t.Fatalf("read pending state: %v", err)
	}
	if target != "" || !pending {
		t.Fatalf("state = (%q, %v), want an empty target with a pending clear", target, pending)
	}

	// An explicit pin settles the debt: the operator has said what the node runs,
	// so there is no reset left to deliver.
	if err := SetTargetVersion(uuid, "v0.1.41"); err != nil {
		t.Fatalf("pin target: %v", err)
	}
	target, pending, err = AgentUpdateState(uuid)
	if err != nil {
		t.Fatalf("read pinned state: %v", err)
	}
	if target != "v0.1.41" || pending {
		t.Fatalf("state = (%q, %v), want the pin and no pending clear", target, pending)
	}

	// The marker can also be flipped on its own: this is what the connection
	// handler does after the clear actually reached the node.
	if err := SetTargetVersionClearPending(uuid, true); err != nil {
		t.Fatalf("set pending marker: %v", err)
	}
	if _, pending, err = AgentUpdateState(uuid); err != nil || !pending {
		t.Fatalf("pending marker not set: %v, %v", pending, err)
	}
	if err := SetTargetVersionClearPending(uuid, false); err != nil {
		t.Fatalf("clear pending marker: %v", err)
	}
	if _, pending, err = AgentUpdateState(uuid); err != nil || pending {
		t.Fatalf("pending marker not cleared: %v, %v", pending, err)
	}
}

// Every state helper must refuse an unknown node instead of reporting a settled
// state for a UUID that no longer exists.
func TestAgentUpdateStateRejectsUnknownClients(t *testing.T) {
	const missing = "client-target-version-missing-state"
	if _, _, err := AgentUpdateState(missing); err == nil {
		t.Fatal("reading the rollout state of a missing client succeeded")
	}
	if err := SetAgentUpdateState(missing, "", true); err == nil {
		t.Fatal("writing the rollout state of a missing client succeeded")
	}
	if err := SetTargetVersionClearPending(missing, true); err == nil {
		t.Fatal("setting the pending marker on a missing client succeeded")
	}
}
