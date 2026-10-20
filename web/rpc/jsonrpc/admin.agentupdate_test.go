package jsonrpc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/Aone2233/nekomari/cmd/flags"
	"github.com/Aone2233/nekomari/database/clients"
	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/pkg/rpc"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
	agentrt "github.com/Aone2233/nekomari/web/agent"
)

// The two copies of the v2 protocol must agree on the method names.
//
// The panel and the agent are separate Go modules, so `protocol/v2/jsonrpc.go` exists twice — once at the
// repository root for the panel and once under `agent/`. They cannot share code, and the drift between them
// is invisible in the worst way: the panel dispatches an event, the agent logs `unknown v2 event method`, and
// nothing fails. That is exactly the shape of a rollout request that silently reaches nobody.
//
// Read as text rather than imported, because importing the agent's copy from the panel's module is not
// possible — which is the reason the duplication exists.
func TestTheTwoProtocolCopiesAgreeOnMethodNames(t *testing.T) {
	panel := methodNamesIn(t, repoFile(t, "protocol", "v2", "jsonrpc.go"))
	agent := methodNamesIn(t, repoFile(t, "agent", "protocol", "v2", "jsonrpc.go"))

	if len(panel) == 0 || len(agent) == 0 {
		t.Fatalf("read %d panel and %d agent method names; the paths are probably wrong", len(panel), len(agent))
	}
	for _, name := range panel {
		if !listContains(agent, name) {
			t.Errorf("the panel declares %q but the agent does not, so an event using it reaches nobody", name)
		}
	}
	for _, name := range agent {
		if !listContains(panel, name) {
			t.Errorf("the agent declares %q but the panel does not, so the panel can never send it", name)
		}
	}
	// And the specific one this feature depends on, named so that removing it from either side fails here with
	// a message about rollouts rather than about symmetry.
	if !listContains(panel, "agent.update") || !listContains(agent, "agent.update") {
		t.Error("the agent.update method is missing from one of the protocol copies")
	}
}

// The request and response field names must agree too, because a mismatch is a params bind failure at runtime
// on a node that is otherwise healthy.
func TestTheTwoProtocolCopiesAgreeOnUpdateParams(t *testing.T) {
	panel := structFieldsIn(t, repoFile(t, "protocol", "v2", "jsonrpc.go"), "UpdateParams")
	agent := structFieldsIn(t, repoFile(t, "agent", "protocol", "v2", "jsonrpc.go"), "UpdateParams")

	if len(panel) == 0 {
		t.Fatal("the panel's UpdateParams has no fields, or was not found")
	}
	if strings.Join(panel, ",") != strings.Join(agent, ",") {
		t.Fatalf("UpdateParams differs: panel %v, agent %v", panel, agent)
	}
}

// repoFile locates a file from the repository root, found by walking up to go.mod.
//
// The paths in this test are the point — they name two copies of one protocol in two different Go modules —
// and hardcoding the walk up from this package's directory got it wrong on the first try. Finding the root by
// its marker makes the test independent of how deep the package sits.
func repoFile(t *testing.T, parts ...string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(append([]string{dir}, parts...)...)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s", dir)
		}
		dir = parent
	}
}

func methodNamesIn(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	pattern := regexp.MustCompile(`MethodAgent\w+\s*=\s*"([^"]+)"`)
	names := make([]string, 0, 16)
	for _, match := range pattern.FindAllStringSubmatch(string(data), -1) {
		names = append(names, match[1])
	}
	sort.Strings(names)
	return names
}

func structFieldsIn(t *testing.T, path, structName string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	pattern := regexp.MustCompile(`type ` + structName + ` struct \{([^}]*)\}`)
	match := pattern.FindStringSubmatch(string(data))
	if match == nil {
		return nil
	}
	field := regexp.MustCompile(`json:"([^",]+)`)
	fields := make([]string, 0, 4)
	for _, m := range field.FindAllStringSubmatch(match[1], -1) {
		fields = append(fields, m[1])
	}
	sort.Strings(fields)
	return fields
}

func listContains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// The version comparison has to ignore the `v` that release tags carry, or every rollout re-dispatches to nodes
// already at the target and the operator sees work happening that is not happening.
func TestTheTargetComparisonIgnoresTheReleaseTagPrefix(t *testing.T) {
	cases := []struct {
		reported, target string
		want             bool
		why              string
	}{
		{"0.1.41", "v0.1.41", true, "the agent reports without the tag's v"},
		{"v0.1.41", "0.1.41", true, "and the panel may be given either form"},
		{"0.1.41", "0.1.41", true, "identical"},
		{" 0.1.41 ", "v0.1.41", true, "whitespace from a form field"},
		{"0.1.40", "v0.1.41", false, "a different version is work to do"},
		{"", "v0.1.41", false, "a node that never reported has not reached the target"},
		{"0.1.41", "", false, "an empty target is a clear, not a match"},
	}
	for _, tc := range cases {
		if got := sameReleaseVersion(tc.reported, tc.target); got != tc.want {
			t.Errorf("sameReleaseVersion(%q, %q) = %v, want %v (%s)",
				tc.reported, tc.target, got, tc.want, tc.why)
		}
	}
}

// rolloutOutcomesOf reads the per-node outcomes out of the handler's response.
func rolloutOutcomesOf(t *testing.T, result any, uuid string) agentUpdateOutcome {
	t.Helper()
	payload, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("roll result = %T, want an object", result)
	}
	outcomes, ok := payload["outcomes"].([]agentUpdateOutcome)
	if !ok {
		t.Fatalf("outcomes = %T", payload["outcomes"])
	}
	for _, outcome := range outcomes {
		if outcome.UUID == uuid {
			return outcome
		}
	}
	t.Fatalf("no outcome for %s in %+v", uuid, outcomes)
	return agentUpdateOutcome{}
}

// createRolloutClient inserts one client row into the shared test database.
func createRolloutClient(t *testing.T, uuid, version string) {
	t.Helper()
	flags.DatabaseType = flags.DatabaseTypeSQLite
	flags.DatabaseFile = "file:jsonrpc-agent-update?mode=memory&cache=shared"
	if err := dbcore.GetDBInstance().Create(&models.Client{
		UUID: uuid, Name: uuid, Token: "token-" + uuid, Version: version,
	}).Error; err != nil {
		t.Fatalf("create client %s: %v", uuid, err)
	}
}

// An offline node cannot be handed the target now, but the panel must keep it:
// otherwise a rollout that started while the node was down either silently skips
// it or, on the next release, moves it forward instead of pinning it.
func TestRollAgentUpdatePersistsTargetForAnOfflineNode(t *testing.T) {
	const uuid = "client-roll-offline"
	createRolloutClient(t, uuid, "")

	result, jerr := adminRollAgentUpdate(context.Background(), rpc.NewRequest(1, "admin:rollAgentUpdate", map[string]any{
		"uuids":  []string{uuid},
		"target": "v0.1.41",
	}))
	if jerr != nil {
		t.Fatalf("roll: %v", jerr)
	}

	outcome := rolloutOutcomesOf(t, result, uuid)
	if outcome.Status != updateSaved {
		t.Fatalf("offline outcome = %q (%s), want %q", outcome.Status, outcome.Detail, updateSaved)
	}
	if !strings.Contains(outcome.Detail, "when the node connects") {
		t.Fatalf("offline detail = %q, want it to promise a re-send on connect", outcome.Detail)
	}
	stored, err := clients.GetTargetVersion(uuid)
	if err != nil {
		t.Fatalf("read persisted target: %v", err)
	}
	if stored != "v0.1.41" {
		t.Fatalf("persisted target = %q, want v0.1.41", stored)
	}
}

// A node that already runs the target is still pinned: "no work to do now" is not
// the same as "this node may move on at the next release".
func TestRollAgentUpdatePinsANodeAlreadyAtTarget(t *testing.T) {
	const uuid = "client-roll-at-target"
	createRolloutClient(t, uuid, "0.1.41")

	result, jerr := adminRollAgentUpdate(context.Background(), rpc.NewRequest(1, "admin:rollAgentUpdate", map[string]any{
		"uuids":  []string{uuid},
		"target": "v0.1.41",
	}))
	if jerr != nil {
		t.Fatalf("roll: %v", jerr)
	}
	if outcome := rolloutOutcomesOf(t, result, uuid); outcome.Status != updateAtTarget {
		t.Fatalf("at-target outcome = %q (%s), want %q", outcome.Status, outcome.Detail, updateAtTarget)
	}
	stored, err := clients.GetTargetVersion(uuid)
	if err != nil {
		t.Fatalf("read persisted target: %v", err)
	}
	if stored != "v0.1.41" {
		t.Fatalf("persisted target = %q, want v0.1.41", stored)
	}
}

// A dry run is a rehearsal: it must not leave the fleet pinned.
func TestRollAgentUpdateDryRunDoesNotPersistTheTarget(t *testing.T) {
	const uuid = "client-roll-dry-run"
	createRolloutClient(t, uuid, "")

	result, jerr := adminRollAgentUpdate(context.Background(), rpc.NewRequest(1, "admin:rollAgentUpdate", map[string]any{
		"uuids":   []string{uuid},
		"target":  "v0.1.41",
		"dry_run": true,
	}))
	if jerr != nil {
		t.Fatalf("dry run: %v", jerr)
	}
	if outcome := rolloutOutcomesOf(t, result, uuid); outcome.Status != updateDispatched {
		t.Fatalf("dry-run outcome = %q (%s)", outcome.Status, outcome.Detail)
	}
	stored, err := clients.GetTargetVersion(uuid)
	if err != nil {
		t.Fatalf("read persisted target: %v", err)
	}
	if stored != "" {
		t.Fatalf("dry run persisted target = %q, want empty", stored)
	}
}

// Clearing the target is a persisted write too: the node must come back as
// "tracks releases" rather than keep the old pin forever.
func TestRollAgentUpdateClearsThePersistedTarget(t *testing.T) {
	const uuid = "client-roll-clear"
	createRolloutClient(t, uuid, "")
	if err := clients.SetTargetVersion(uuid, "v0.1.40"); err != nil {
		t.Fatalf("seed target: %v", err)
	}

	result, jerr := adminRollAgentUpdate(context.Background(), rpc.NewRequest(1, "admin:rollAgentUpdate", map[string]any{
		"uuids":  []string{uuid},
		"target": "",
	}))
	if jerr != nil {
		t.Fatalf("clear: %v", jerr)
	}
	if outcome := rolloutOutcomesOf(t, result, uuid); outcome.Status != updateSaved {
		t.Fatalf("clear outcome for an offline node = %q (%s), want %q", outcome.Status, outcome.Detail, updateSaved)
	}
	stored, err := clients.GetTargetVersion(uuid)
	if err != nil {
		t.Fatalf("read persisted target: %v", err)
	}
	if stored != "" {
		t.Fatalf("cleared target = %q, want empty", stored)
	}
}

// agentVersions must expose the persisted target next to the reported version,
// so the page can show "pinned to X, running Y" without a second call.
func TestAgentVersionsExposesThePersistedTarget(t *testing.T) {
	const pinned = "client-versions-pinned"
	const free = "client-versions-free"
	createRolloutClient(t, pinned, "0.1.41")
	createRolloutClient(t, free, "0.1.41")
	if err := clients.SetTargetVersion(pinned, "v0.1.40"); err != nil {
		t.Fatalf("seed target: %v", err)
	}
	if err := clients.SetTargetVersion(free, ""); err != nil {
		t.Fatalf("clear target: %v", err)
	}

	result, jerr := adminAgentVersions(context.Background(), nil)
	if jerr != nil {
		t.Fatalf("agent versions: %v", jerr)
	}
	payload, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("versions result = %T", result)
	}
	rows, ok := payload["rows"].([]agentVersionRow)
	if !ok {
		t.Fatalf("rows = %T", payload["rows"])
	}
	seen := map[string]agentVersionRow{}
	for _, row := range rows {
		seen[row.UUID] = row
	}
	if row := seen[pinned]; row.TargetVersion != "v0.1.40" {
		t.Fatalf("pinned row target = %q, want v0.1.40", row.TargetVersion)
	}
	if row := seen[free]; row.TargetVersion != "" {
		t.Fatalf("unpinned row target = %q, want empty", row.TargetVersion)
	}
}

// seedPendingClear records a clear that the node has not received yet.
func seedPendingClear(t *testing.T, uuid string) {
	t.Helper()
	if err := clients.SetAgentUpdateState(uuid, "", true); err != nil {
		t.Fatalf("seed pending clear for %s: %v", uuid, err)
	}
}

// requireRolloutState fails unless the persisted rollout state matches.
func requireRolloutState(t *testing.T, uuid, wantTarget string, wantPending bool) {
	t.Helper()
	target, pending, err := clients.AgentUpdateState(uuid)
	if err != nil {
		t.Fatalf("read rollout state: %v", err)
	}
	if target != wantTarget || pending != wantPending {
		t.Fatalf("rollout state = (%q, %v), want (%q, %v)", target, pending, wantTarget, wantPending)
	}
}

// Clearing the pin while the node is offline must leave a debt the reconnect can
// pay: an empty target alone is indistinguishable from "never pinned", so without
// the marker that node would keep running the old pin until it restarted.
func TestRollAgentUpdateMarksAPendingClearForAnOfflineNode(t *testing.T) {
	const uuid = "client-roll-pending-offline"
	createRolloutClient(t, uuid, "0.1.40")
	if err := clients.SetAgentUpdateState(uuid, "v0.1.40", false); err != nil {
		t.Fatalf("seed pin: %v", err)
	}

	result, jerr := adminRollAgentUpdate(context.Background(), rpc.NewRequest(1, "admin:rollAgentUpdate", map[string]any{
		"uuids":  []string{uuid},
		"target": "",
	}))
	if jerr != nil {
		t.Fatalf("roll: %v", jerr)
	}
	if outcome := rolloutOutcomesOf(t, result, uuid); outcome.Status != updateSaved {
		t.Fatalf("offline clear outcome = %q (%s), want %q", outcome.Status, outcome.Detail, updateSaved)
	}
	requireRolloutState(t, uuid, "", true)
}

// A clear delivered to a connected node settles the debt immediately: nothing is
// left to re-send when that node next connects.
func TestRollAgentUpdateClearsThePendingMarkerForAnOnlineNode(t *testing.T) {
	const uuid = "client-roll-pending-online"
	createRolloutClient(t, uuid, "0.1.40")
	if err := clients.SetAgentUpdateState(uuid, "v0.1.40", false); err != nil {
		t.Fatalf("seed pin: %v", err)
	}
	agentrt.MarkV2Client(uuid)
	t.Cleanup(func() {
		agentrt.DeleteConnectedClients(uuid)
		agentrt.TakeV2Events(uuid, nil, 0)
	})

	result, jerr := adminRollAgentUpdate(context.Background(), rpc.NewRequest(1, "admin:rollAgentUpdate", map[string]any{
		"uuids":  []string{uuid},
		"target": "",
	}))
	if jerr != nil {
		t.Fatalf("roll: %v", jerr)
	}
	if outcome := rolloutOutcomesOf(t, result, uuid); outcome.Status != updateDispatched {
		t.Fatalf("online clear outcome = %q (%s), want %q", outcome.Status, outcome.Detail, updateDispatched)
	}
	requireRolloutState(t, uuid, "", false)

	// The node really was told: the clear is on its event queue, with an empty
	// target, which is what makes the agent forget the pin.
	events := agentrt.TakeV2Events(uuid, nil, 0)
	if len(events) != 1 {
		t.Fatalf("queued %d events for the node, want 1", len(events))
	}
	if events[0].Method != v2.MethodAgentUpdate {
		t.Fatalf("queued event method = %q, want %q", events[0].Method, v2.MethodAgentUpdate)
	}
	params, ok := events[0].Params.(v2.UpdateParams)
	if !ok {
		t.Fatalf("queued params = %T, want v2.UpdateParams", events[0].Params)
	}
	if params.Target != "" {
		t.Fatalf("queued target = %q, want empty", params.Target)
	}
}

// Pinning a node again settles any pending clear: the operator has just said what
// the node should run, so there is no reset left to deliver.
func TestRollAgentUpdatePinSettlesAPendingClear(t *testing.T) {
	const uuid = "client-roll-pending-superseded"
	createRolloutClient(t, uuid, "")
	seedPendingClear(t, uuid)

	result, jerr := adminRollAgentUpdate(context.Background(), rpc.NewRequest(1, "admin:rollAgentUpdate", map[string]any{
		"uuids":  []string{uuid},
		"target": "v0.1.41",
	}))
	if jerr != nil {
		t.Fatalf("roll: %v", jerr)
	}
	if outcome := rolloutOutcomesOf(t, result, uuid); outcome.Status != updateSaved {
		t.Fatalf("offline pin outcome = %q (%s), want %q", outcome.Status, outcome.Detail, updateSaved)
	}
	requireRolloutState(t, uuid, "v0.1.41", false)
}

// A dry run must not touch the debt either: rehearsing a clear cannot retire a
// retry that has not happened.
func TestRollAgentUpdateDryRunKeepsAPendingClear(t *testing.T) {
	const uuid = "client-roll-pending-dry-run"
	createRolloutClient(t, uuid, "")
	seedPendingClear(t, uuid)

	result, jerr := adminRollAgentUpdate(context.Background(), rpc.NewRequest(1, "admin:rollAgentUpdate", map[string]any{
		"uuids":   []string{uuid},
		"target":  "",
		"dry_run": true,
	}))
	if jerr != nil {
		t.Fatalf("dry run: %v", jerr)
	}
	if outcome := rolloutOutcomesOf(t, result, uuid); outcome.Status != updateDispatched {
		t.Fatalf("dry-run outcome = %q (%s)", outcome.Status, outcome.Detail)
	}
	requireRolloutState(t, uuid, "", true)
}

// agentVersions exposes the pending debt next to the target, but only when it is
// set: the existing row shape must not change for a fleet with nothing pending.
func TestAgentVersionsExposesAPendingClear(t *testing.T) {
	const pending = "client-versions-pending-clear"
	const settled = "client-versions-settled"
	createRolloutClient(t, pending, "0.1.40")
	createRolloutClient(t, settled, "0.1.41")
	seedPendingClear(t, pending)

	result, jerr := adminAgentVersions(context.Background(), nil)
	if jerr != nil {
		t.Fatalf("agent versions: %v", jerr)
	}
	payload, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("versions result = %T", result)
	}
	rows, ok := payload["rows"].([]agentVersionRow)
	if !ok {
		t.Fatalf("rows = %T", payload["rows"])
	}
	seen := map[string]agentVersionRow{}
	for _, row := range rows {
		seen[row.UUID] = row
	}
	if row := seen[pending]; !row.PendingClear {
		t.Fatalf("pending row = %+v, want PendingClear", row)
	}
	if row := seen[settled]; row.PendingClear {
		t.Fatalf("settled row = %+v, want no pending clear", row)
	}

	settledRow, err := json.Marshal(seen[settled])
	if err != nil {
		t.Fatalf("marshal settled row: %v", err)
	}
	if strings.Contains(string(settledRow), "pending_clear") {
		t.Fatalf("settled row exposes pending_clear: %s", settledRow)
	}
}
