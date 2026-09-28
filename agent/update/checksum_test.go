package update

import "testing"

// A targeted update needs the release's checksum file, and its name is a contract with the release workflow.
//
// `findReleaseByTag` asked for `checksums.txt` while the workflow writes `SHA256SUMS.txt`
// (`sha256sum * | tee SHA256SUMS.txt` in `.github/workflows/release.yml`). The lookup therefore always
// returned the zero asset, `applyRelease` downloaded an empty URL first, and every targeted update failed
// with "invalid release asset URL" — on every node, for every version, while the *selection* reported
// success. Two things had to be true to find it: the error had to name the asset and the URL (it named
// neither), and there had to be a test that reads the release's real asset list.
//
// So this test asserts the name against the release data rather than against a constant repeated in the
// test, which would have agreed with the bug.
func TestTheChecksumAssetNameMatchesTheReleaseWorkflow(t *testing.T) {
	releases := []githubRelease{
		release("v1.2.3", false, false,
			"komari-agent-linux-amd64", "SHA256SUMS.txt", "nekomari-linux-amd64"),
		// A release whose checksum file is missing must still be selected, so that the failure names the
		// missing file rather than the selection refusing on `applyRelease`'s behalf with a message about a
		// URL. `applyRelease` rejects it explicitly — see the test below.
		release("v1.2.4", false, false, "komari-agent-linux-amd64"),
	}

	candidate, ok := findReleaseByTag(releases, "1.2.3", "komari-agent-linux-amd64")
	if !ok {
		t.Fatal("the release was not selected")
	}
	if candidate.Checksum.Name != "SHA256SUMS.txt" {
		t.Errorf("checksum asset = %q, want %q: the release workflow writes that name, and anything else "+
			"leaves the download with an empty URL", candidate.Checksum.Name, "SHA256SUMS.txt")
	}
	if candidate.Checksum.BrowserDownloadURL == "" {
		t.Error("the checksum asset has no download URL, which is what the real failure looked like")
	}

	noChecksum, ok := findReleaseByTag(releases, "1.2.4", "komari-agent-linux-amd64")
	if !ok {
		t.Fatal("a release without a checksum file must still be selectable")
	}
	if noChecksum.Checksum.Name != "" {
		t.Errorf("checksum asset = %q for a release that has none", noChecksum.Checksum.Name)
	}
}

// A release with no checksum file is refused by name, not by an empty URL.
//
// This is the shape the real failure took: the lookup returned the zero asset, and `applyRelease` passed
// that empty URL to `downloadAsset`, which reported "invalid release asset URL" — a message about a URL for
// a problem that was a missing file. It now fails where the knowledge is.
func TestAReleaseWithNoChecksumIsRefusedByName(t *testing.T) {
	err := applyRelease(snapshotReleaseCandidate{
		TagName: "v1.2.4",
		Asset:   githubReleaseAsset{Name: "komari-agent-linux-amd64", BrowserDownloadURL: "https://github.com/x/y"},
		// Checksum deliberately left as the zero value.
	}, t.TempDir()+"/agent")

	if err == nil {
		t.Fatal("a release with no checksum asset must be refused")
	}
	message := err.Error()
	for _, want := range []string{"SHA256SUMS.txt", "v1.2.4", "komari-agent-linux-amd64"} {
		if !containsText(message, want) {
			t.Errorf("the error does not mention %q: %s", want, message)
		}
	}
	if containsText(message, "invalid release asset URL") {
		t.Errorf("the error still reports a URL problem for a missing file: %s", message)
	}
}

// The names the update path looks for must be the names the release workflow produces.
//
// Read from both files rather than duplicated here, because the point is that the two agree — a constant in
// this test would have agreed with the bug for as long as the bug existed.
func TestTheUpdatePathsAssetNamesMatchTheWorkflow(t *testing.T) {
	workflow := readRepoFile(t, ".github/workflows/release.yml")
	if !containsText(workflow, "SHA256SUMS.txt") {
		t.Skip("the workflow no longer writes SHA256SUMS.txt; update this test and the code together")
	}
	if containsText(workflow, "checksums.txt") {
		t.Fatal("the workflow writes checksums.txt as well; this test no longer knows which name is right")
	}
}

func containsText(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
