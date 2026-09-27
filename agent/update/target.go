package update

import (
	"fmt"
	"log"
	"os"
	"runtime"
	"strings"
)

// Targeted updates: install the version the panel asks for, rather than the newest one.
//
// ## Why this exists
//
// The agent has always updated itself to the latest release on a six-hour timer (`DoUpdateWorks`). That is
// fine for a fleet nobody is watching and wrong for one that is: there is no way to hold a node back, no way
// to move a subset first, and no way to undo a bad release except by publishing another one.
//
// Roadmap H6. The panel states a target version and this installs it, so a rollout is something an operator
// does in batches and can reverse. Downgrades are deliberately supported: a rollback that cannot go
// backwards is not a rollback.
//
// ## Where the target comes from
//
// `AGENT_TARGET_VERSION`, which the panel writes into the node's configuration alongside its other settings.
// Not a message over the WebSocket, and that is the design decision worth stating: a target held in
// configuration applies whenever the node next asks — including a node that was offline when the rollout
// started — and reversing it is a single write. A pushed command only reaches nodes that happen to be
// connected, and leaves nothing behind to consult after a restart.
//
// An empty target keeps the old behaviour exactly: update to the latest. So a deployment that never sets one
// sees no change, and a node whose target is cleared resumes tracking releases.

// RequestedVersion is the version the panel asked this node to run, or empty to track releases.
//
// Read from the environment each time rather than cached, because the value changes when an operator decides
// it should and a cached copy would make the change take effect on the next restart — the opposite of what a
// rollout needs.
func RequestedVersion() string {
	return strings.TrimSpace(os.Getenv("AGENT_TARGET_VERSION"))
}

// CheckAndUpdateTo installs a specific released version of this agent.
//
// Returns nil when nothing was done, which covers the cases an operator most wants distinguished in a log:
// the target is already running, the target is not a release, or the asset for this platform is missing.
func CheckAndUpdateTo(target string) error {
	target = strings.TrimSpace(target)
	if target == "" {
		return fmt.Errorf("no target version given")
	}
	if isContainerAgent() {
		// A container's binary is part of its image; replacing it inside the running container would be
		// discarded on the next restart, and the operator would believe the node had been updated.
		log.Println("Agent is running in a container; its version follows the image, so the target is ignored.")
		return nil
	}
	if target == CurrentVersion {
		log.Printf("Already running the requested version %s.", target)
		return nil
	}

	owner, repo, err := splitRepoSlug(Repo)
	if err != nil {
		return err
	}
	releases, err := listGitHubReleases(owner, repo)
	if err != nil {
		return err
	}

	assetName := expectedAssetName(runtime.GOOS, runtime.GOARCH)
	release, found := findReleaseByTag(releases, target, assetName)
	if !found {
		// Named rather than generic: the two ways this happens are a typo in the target and a release that
		// predates this platform's asset, and the message says which was looked for.
		return fmt.Errorf("release %q has no %s asset", target, assetName)
	}

	cmdPath, err := currentExecutablePath()
	if err != nil {
		return fmt.Errorf("failed to resolve current executable path: %w", err)
	}

	log.Printf("Updating %s from %s to the requested %s\n", cmdPath, CurrentVersion, release.TagName)
	if err := applyRelease(release, cmdPath); err != nil {
		return fmt.Errorf("failed to update to %s: %w", release.TagName, err)
	}
	log.Printf("Successfully updated to the requested version %s\n", release.TagName)
	// The same exit code the release-tracking path uses, so whatever supervises the agent restarts it the same
	// way for both kinds of update.
	os.Exit(42)
	return nil
}

// findReleaseByTag locates one release by tag, requiring an asset for this platform.
//
// The tag comparison ignores a leading `v`, because releases are tagged `v0.1.41` while a version string
// reported by an agent is `0.1.41` — and an operator copying either form into the panel should get the
// release they meant. Drafts and prereleases are skipped: a target that names one is almost always a typo,
// and installing an unreleased build by accident is worse than refusing.
func findReleaseByTag(releases []githubRelease, target, assetName string) (snapshotReleaseCandidate, bool) {
	want := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(target), "v"), "V")
	for _, release := range releases {
		if release.Draft || release.Prerelease {
			continue
		}
		tag := strings.TrimPrefix(strings.TrimPrefix(release.TagName, "v"), "V")
		if tag != want {
			continue
		}
		asset, ok := findReleaseAsset(release, assetName)
		if !ok {
			continue
		}
		checksum, _ := findReleaseAsset(release, "checksums.txt")
		return snapshotReleaseCandidate{
			TagName: release.TagName, Name: release.Name, Body: release.Body,
			HTMLURL: release.HTMLURL, PublishedAt: release.PublishedAt,
			Asset: asset, Checksum: checksum,
		}, true
	}
	return snapshotReleaseCandidate{}, false
}

// TargetSatisfied reports whether the running version already matches a target.
//
// Used to decide whether to keep checking. A target is compared as a string after normalising the `v`
// prefix, not as a semantic version: the operator asked for a specific release, and "0.1.41" and "0.1.41"
// are the same release whether or not something considers them equal in an ordering.
func TargetSatisfied(target string) bool {
	target = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(target), "v"), "V")
	current := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(CurrentVersion), "v"), "V")
	return target == "" || target == current
}
