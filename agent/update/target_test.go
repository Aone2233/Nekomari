package update

import "testing"

// The decisions in a targeted update, which are the parts that can be wrong without failing loudly: the
// download, checksum and binary replacement are the existing release-tracking code, already in use.

func release(tag string, draft, prerelease bool, assets ...string) githubRelease {
	list := make([]githubReleaseAsset, 0, len(assets))
	for _, name := range assets {
		list = append(list, githubReleaseAsset{Name: name, BrowserDownloadURL: "https://example.invalid/" + name})
	}
	return githubRelease{TagName: tag, Name: tag, Draft: draft, Prerelease: prerelease, Assets: list}
}

// The tag is matched with or without its `v`, because releases are tagged `v0.1.41` while an agent reports
// `0.1.41`, and an operator copying either form into the panel should get the release they meant.
func TestATargetFindsItsReleaseWithOrWithoutTheVPrefix(t *testing.T) {
	releases := []githubRelease{
		release("v0.1.40", false, false, "nekomari-agent-linux-amd64"),
		release("v0.1.41", false, false, "nekomari-agent-linux-amd64"),
	}

	for _, target := range []string{"0.1.41", "v0.1.41", " V0.1.41 "} {
		found, ok := findReleaseByTag(releases, target, "nekomari-agent-linux-amd64")
		if !ok {
			t.Errorf("target %q found no release", target)
			continue
		}
		if found.TagName != "v0.1.41" {
			t.Errorf("target %q resolved to %s, want v0.1.41", target, found.TagName)
		}
	}
}

// An older target has to resolve, or a rollback is not a rollback. This is the case a "latest release"
// selector cannot express and the reason the function exists.
func TestAnOlderTargetResolvesForARollback(t *testing.T) {
	releases := []githubRelease{
		release("v0.1.38", false, false, "nekomari-agent-linux-amd64"),
		release("v0.1.39", false, false, "nekomari-agent-linux-amd64"),
		release("v0.1.40", false, false, "nekomari-agent-linux-amd64"),
	}

	found, ok := findReleaseByTag(releases, "0.1.38", "nekomari-agent-linux-amd64")
	if !ok {
		t.Fatal("an older target must be found")
	}
	if found.TagName != "v0.1.38" {
		t.Fatalf("resolved to %s, want v0.1.38", found.TagName)
	}
}

// Drafts and prereleases are skipped: a target naming one is almost always a typo, and installing an
// unreleased build by accident is worse than refusing.
func TestDraftsAndPrereleasesAreNotTargets(t *testing.T) {
	releases := []githubRelease{
		release("v0.2.0", true, false, "nekomari-agent-linux-amd64"),  // draft
		release("v0.2.0", false, true, "nekomari-agent-linux-amd64"),  // prerelease
		release("v0.2.0-rc1", false, true, "nekomari-agent-linux-amd64"),
	}
	for _, target := range []string{"0.2.0", "v0.2.0", "0.2.0-rc1"} {
		if _, ok := findReleaseByTag(releases, target, "nekomari-agent-linux-amd64"); ok {
			t.Errorf("target %q resolved to a draft or prerelease", target)
		}
	}
}

// A release without an asset for this platform is refused rather than installed as something else. The
// message names the asset, because the two causes — a typo in the target, and a release older than this
// platform's asset — are told apart by knowing which was looked for.
func TestAReleaseWithoutThisPlatformsAssetIsRefused(t *testing.T) {
	releases := []githubRelease{
		release("v0.1.41", false, false, "nekomari-agent-linux-amd64"),
		release("v0.1.41", false, false),
	}
	if _, ok := findReleaseByTag(releases, "0.1.41", "nekomari-agent-windows-arm64.exe"); ok {
		t.Fatal("a release with no asset for this platform must not resolve")
	}
	// And the tests above prove the platform-specific name matters: the same tag does resolve for the asset
	// it actually carries.
	if _, ok := findReleaseByTag(releases, "0.1.41", "nekomari-agent-linux-amd64"); !ok {
		t.Fatal("the asset it does carry must resolve")
	}
}

// A target is compared as a string after normalising the `v`, not as a semantic version: the operator asked
// for a specific release, and pointing at the version already running must stop the loop rather than
// reinstalling it every six hours.
func TestTargetSatisfiedComparesTheRequestedRelease(t *testing.T) {
	previous := CurrentVersion
	t.Cleanup(func() { CurrentVersion = previous })

	CurrentVersion = "0.1.41"
	cases := []struct {
		target string
		want   bool
		why    string
	}{
		{"0.1.41", true, "the same version"},
		{"v0.1.41", true, "the same version with the release's tag prefix"},
		{" v0.1.41 ", true, "whitespace from a configuration file"},
		{"0.1.40", false, "an older target means a rollback is still pending"},
		{"0.1.42", false, "a newer target is pending"},
		{"", true, "no target means nothing to do, so release tracking resumes"},
	}
	for _, tc := range cases {
		if got := TargetSatisfied(tc.target); got != tc.want {
			t.Errorf("TargetSatisfied(%q) = %v, want %v (%s)", tc.target, got, tc.want, tc.why)
		}
	}
}

// The target is read from the environment on every call rather than cached, because an operator's change has
// to take effect on the next check — a cached copy would defer it to the next restart, which is the opposite
// of what a rollout needs.
func TestTheRequestedVersionIsReadEachTime(t *testing.T) {
	t.Setenv("AGENT_TARGET_VERSION", "0.1.41")
	if got := RequestedVersion(); got != "0.1.41" {
		t.Fatalf("RequestedVersion() = %q, want 0.1.41", got)
	}
	t.Setenv("AGENT_TARGET_VERSION", "0.1.42")
	if got := RequestedVersion(); got != "0.1.42" {
		t.Fatalf("RequestedVersion() = %q after a change, want 0.1.42", got)
	}
	t.Setenv("AGENT_TARGET_VERSION", "  ")
	if got := RequestedVersion(); got != "" {
		t.Fatalf("RequestedVersion() = %q for whitespace, want empty", got)
	}
}

// An empty target is an error for the targeted path rather than a silent no-op, because the caller reaching
// it has already decided a target exists; passing one through would mean installing the latest release under
// the guise of installing a chosen one.
func TestAnEmptyTargetIsRefusedByTheTargetedUpdate(t *testing.T) {
	if err := CheckAndUpdateTo(""); err == nil {
		t.Fatal("an empty target must be refused")
	}
	if err := CheckAndUpdateTo("   "); err == nil {
		t.Fatal("a whitespace target must be refused")
	}
}
