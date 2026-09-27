package jsonrpc

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
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
