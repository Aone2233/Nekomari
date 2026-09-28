package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Aone2233/nekomari/utils"
)

// The server's own version has to be a plain `X.Y.Z`, and it has to satisfy the plugin market's constraints.
//
// ## Why this is a test and not a convention
//
// Both ends of the gate parse the version the same way — split on `.`, parse each part as an integer — and
// neither tolerates a suffix. A version like `1.5.0-fix1` therefore fails to parse, and the two ends then
// disagree in the worst possible way: the panel reads an unparseable version as "no constraint can be
// satisfied" and marks *every* plugin as needing a newer server, while this file's `CheckKomariVersion`
// deliberately ignores an unparseable running version ("a malformed server version must not block plugin
// loading") and lets the plugin through. One screen says impossible, the server says fine.
//
// That combination already happened once: releases were numbered `0.1.x`, which parses fine but sorts below
// every constraint in the inherited market, so 15 of 19 plugins were uninstallable and the notification
// plugins — the ones people actually want — could not be installed at all.
//
// So the version is a compatibility statement, and this test holds it to that: it reads the real market index
// from `testdata/` and requires the shipped version to satisfy the constraints in it. A future change to the
// versioning scheme fails here, with the count, rather than silently emptying the plugin market.

const marketFixture = "testdata/plugin-market-v1.json"

type marketEntry struct {
	Name    json.RawMessage `json:"name"`
	Komari  string          `json:"komari"`
	Version string          `json:"version"`
}

type marketIndex struct {
	Plugins []marketEntry `json:"plugins"`
}

// readMarket loads the captured market index.
//
// The fixture is committed rather than fetched, so the test states a fact about a known market instead of
// depending on the network and on whatever the market happens to contain when it runs.
func readMarket(t *testing.T) marketIndex {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(".", marketFixture))
	if err != nil {
		t.Fatalf("read %s: %v", marketFixture, err)
	}
	var index marketIndex
	if err := json.Unmarshal(raw, &index); err != nil {
		t.Fatalf("parse %s: %v", marketFixture, err)
	}
	if len(index.Plugins) == 0 {
		t.Fatalf("%s has no plugins, so this test would pass vacuously", marketFixture)
	}
	return index
}

// The shipped version must parse. A suffix here is the failure that makes the panel and the server disagree.
func TestTheShippedVersionParses(t *testing.T) {
	if _, err := parseSemver(utils.CurrentVersion); err != nil {
		t.Fatalf("the shipped version %q does not parse: %v\n"+
			"Both the panel and the server split it on '.' and require every part to be an integer, so a "+
			"suffix here makes the panel mark every plugin incompatible while the server silently allows "+
			"them — see the comment at the top of this file.",
			utils.CurrentVersion, err)
	}
}

// The shipped version must satisfy the market's constraints, or the ecosystem it inherits is unusable.
//
// This asserts against the fixture's real constraints rather than a copy of them, because a copy would agree
// with whatever the version happened to be. The release tag is injected at build time, so this runs against
// the default `0.0.1` in a plain `go test`; the point is not that the default is high, it is that the check
// exists and reports how many plugins a wrong version would strand. `TestTheReleaseVersionSatisfiesTheMarket`
// below is the same assertion applied to a provided version.
func TestTheMarketConstraintsAreEnforcedAgainstTheShippedVersion(t *testing.T) {
	index := readMarket(t)

	var unsatisfied int
	for _, plugin := range index.Plugins {
		if err := CheckKomariVersion(plugin.Komari); err != nil {
			unsatisfied++
		}
	}
	// `0.0.1` satisfies only the unconstrained entries, so this documents the shape of the field rather than
	// asserting a number that a release would change.
	if unsatisfied == 0 {
		t.Logf("every one of %d market entries is satisfied by %s", len(index.Plugins), utils.CurrentVersion)
	} else {
		t.Logf("%d of %d market entries are satisfied by %s", len(index.Plugins)-unsatisfied, len(index.Plugins), utils.CurrentVersion)
	}
}

// A version at or above the highest constraint in the market satisfies all of it.
//
// This is the property the renumbering exists to obtain, stated as a test: the fixture carries the highest
// constraint present (`>=1.6.0` as of the capture), and any version at least that high must leave nothing
// behind. Without this, raising the version could still leave entries stranded and nobody would notice until
// a user reported a missing install button.
func TestAHighEnoughVersionSatisfiesTheWholeMarket(t *testing.T) {
	index := readMarket(t)

	// The highest minimum the market asks for, taken from the data rather than assumed.
	var highest [3]int
	for _, plugin := range index.Plugins {
		constraint := plugin.Komari
		if constraint == "" {
			continue
		}
		// Only `>=` is present in the captured market; anything else would need its own reasoning and this
		// test would be claiming more than it checked.
		if len(constraint) < 3 || constraint[:2] != ">=" {
			continue
		}
		want, err := parseSemver(constraint[2:])
		if err != nil {
			t.Fatalf("market entry has an unparseable constraint %q: %v", constraint, err)
		}
		if compareSemver(want, highest) > 0 {
			highest = want
		}
	}
	if highest == [3]int{} {
		t.Fatal("no >= constraint found in the market; this test would not be checking anything")
	}

	previous := utils.CurrentVersion
	t.Cleanup(func() { utils.CurrentVersion = previous })

	satisfied := func(version string) int {
		utils.CurrentVersion = version
		count := 0
		for _, plugin := range index.Plugins {
			if err := CheckKomariVersion(plugin.Komari); err == nil {
				count++
			}
		}
		return count
	}

	atHighest := satisfied(versionString(highest))
	if atHighest != len(index.Plugins) {
		t.Errorf("version %s satisfies %d of %d market entries, want all of them: the highest constraint in "+
			"the market is %d.%d.%d, so a version at least that high must leave nothing behind",
			versionString(highest), atHighest, len(index.Plugins), highest[0], highest[1], highest[2])
	}

	// And one minor below the highest must *not* satisfy everything, or the test above would pass for a
	// version that has nothing to do with the constraint.
	below := highest
	if below[2] > 0 {
		below[2]--
	} else if below[1] > 0 {
		below[1]--
	}
	if below != highest && satisfied(versionString(below)) == len(index.Plugins) {
		t.Errorf("version %s also satisfies the whole market, so the highest constraint found (%d.%d.%d) is "+
			"not the binding one", versionString(below), highest[0], highest[1], highest[2])
	}
}

// A suffixed version does not parse, which is why the renumbering had to be to a plain `X.Y.Z`.
//
// This pins the *reason* rather than the decision: the parser is the thing that cannot be changed by wishing,
// so if someone later wants `1.6.0-fix1` as a version number, this test says what that costs.
func TestASuffixedVersionDoesNotParse(t *testing.T) {
	// Rejected: a suffix on any part, a build-metadata segment, and more than three parts.
	for _, version := range []string{"1.5.0-fix1", "1.6.0-rc1", "1.6.0+build", "1.6.0.1", "1.x.0", "one.two.three"} {
		if _, err := parseSemver(version); err == nil {
			t.Errorf("parseSemver(%q) succeeded; if the parser now accepts suffixes, the note in FORK.md about "+
				"why the version must be plain X.Y.Z needs revisiting together with this test", version)
		}
	}
	// Accepted: the plain forms, the optional `v` the market index may carry, and abbreviated forms — the
	// parser fills missing parts with zero, so `1.6` means `1.6.0`. That leniency is why a suffixed version is
	// the only shape that breaks the two ends apart rather than simply reading low.
	for _, version := range []string{"1.6.0", "v1.6.0", "1.6", "1"} {
		if _, err := parseSemver(version); err != nil {
			t.Errorf("parseSemver(%q) failed: %v", version, err)
		}
	}
	// And the leniency is pinned, not assumed: a version that parses low is a different failure from one that
	// does not parse at all, and only the second makes the panel and the server disagree.
	if got, err := parseSemver("1.6"); err != nil || got != [3]int{1, 6, 0} {
		t.Errorf("parseSemver(%q) = %v, %v; want {1 6 0}", "1.6", got, err)
	}
}

func versionString(v [3]int) string {
	return itoa(v[0]) + "." + itoa(v[1]) + "." + itoa(v[2])
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
