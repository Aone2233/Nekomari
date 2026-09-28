package update

import (
	"fmt"
	"runtime"
	"testing"
)

// 集成：用真实的 CheckAndUpdateTo 路径跑到「第一个下载」为止，看它到底传了什么给 downloadAsset。
func TestProbeRealTargetedUpdateSelection(t *testing.T) {
	owner, repo, err := splitRepoSlug(Repo)
	if err != nil { t.Fatal(err) }
	releases, err := listGitHubReleases(owner, repo)
	if err != nil { t.Fatalf("list: %v", err) }

	assetName := expectedAssetName(runtime.GOOS, runtime.GOARCH)
	fmt.Printf("  expectedAssetName=%q\n", assetName)

	cand, ok := findReleaseByTag(releases, "v0.1.45", assetName)
	fmt.Printf("  findReleaseByTag ok=%v tag=%q asset=%q assetURL=%q checksumName=%q checksumURL=%q\n",
		ok, cand.TagName, cand.Asset.Name, cand.Asset.BrowserDownloadURL, cand.Checksum.Name, cand.Checksum.BrowserDownloadURL)

	// 这一步就是 applyRelease 的第一步
	if _, err := downloadAsset(cand.Checksum, 1<<20); err != nil {
		fmt.Printf("  downloadAsset(Checksum) error: %v\n", err)
	} else {
		fmt.Printf("  downloadAsset(Checksum) ok\n")
	}
}