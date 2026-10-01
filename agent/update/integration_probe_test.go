package update

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"testing"
)

// probeCredentialsPresent reports whether this process will talk to api.github.com
// authenticated.
//
// It must agree with the condition update.go itself uses to attach the
// Authorization header (`os.Getenv("GITHUB_TOKEN") != ""`) — a gate that believed
// it had credentials while the request went out anonymous would reproduce exactly
// the bug this gate exists for.
func probeCredentialsPresent() bool {
	return os.Getenv("GITHUB_TOKEN") != ""
}

// integrationProbeSkipReason is what a caller sees when the probe declines to run.
// It names the cause, the incident, and where CI supplies the credential, so
// "skipped" is never mistaken for "passed".
const integrationProbeSkipReason = "GITHUB_TOKEN is not set: this case calls the real api.github.com, and unauthenticated requests are limited to 60/hour per IP — shared CI runner IPs are routinely over that, and the resulting 403 already turned a docs-only PR red. Set GITHUB_TOKEN to run it (CI does: .github/workflows/ci.yml, the \"Test agent\" step)."

// 集成：用真实的 CheckAndUpdateTo 路径跑到「第一个下载」为止，看它到底传了什么给 downloadAsset。
//
// 这是一条真实的网络用例：它列 Aone2233/Nekomari 的 Release，并把 v0.1.45 的
// SHA256SUMS.txt 下下来。没有凭据时 GitHub 只给 60 次/小时/IP，而 CI runner 是共享出口
// IP —— 一个只改文档的 PR 就因为这里的 403 变红。所以：没有凭据就 skip（说清原因），
// 有凭据（CI 注入 secrets.GITHUB_TOKEN，见 .github/workflows/ci.yml 的 "Test agent"）
// 才真的跑，那时限额是 5000 次/小时，这条用例才是可靠的。
func TestProbeRealTargetedUpdateSelection(t *testing.T) {
	if !probeCredentialsPresent() {
		t.Skip(integrationProbeSkipReason)
	}

	owner, repo, err := splitRepoSlug(Repo)
	if err != nil {
		t.Fatal(err)
	}
	releases, err := listGitHubReleases(owner, repo)
	if err != nil {
		t.Fatalf("list: %v (an HTTP 403 'rate limit exceeded' here means the credential did not reach the request; see the \"Test agent\" step in .github/workflows/ci.yml)", err)
	}

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

// TestProbeCredentialsPresentMatchesTheRequestThatIsActuallySent 离线钉住上面那个
// skip 判定与真实请求的一致性：没有 GITHUB_TOKEN 时 listGitHubReleases 不带
// Authorization（60/小时的限额就是这么来的），有 GITHUB_TOKEN 时带 Bearer。
//
// 后一句才是 CI 里这条探针「真的被认证运行」的依据；前一句说明没有凭据时它必然会被
// 限流，所以那种情况下只能 skip。
func TestProbeCredentialsPresentMatchesTheRequestThatIsActuallySent(t *testing.T) {
	const token = "test-only-token-never-sent-anywhere"

	cases := []struct {
		name     string
		token    string
		wantAuth string
	}{
		{name: "credential set", token: token, wantAuth: "Bearer " + token},
		{name: "no credential", token: "", wantAuth: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GITHUB_TOKEN", tc.token)

			original := updateClient
			defer func() { updateClient = original }()
			var gotAuth, gotURL string
			updateClient = &http.Client{Transport: updateTransport(func(r *http.Request) (*http.Response, error) {
				gotAuth = r.Header.Get("Authorization")
				gotURL = r.URL.String()
				// A short page ends listGitHubReleases after the first request.
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader("[]")),
					Header:     make(http.Header),
				}, nil
			})}

			if _, err := listGitHubReleases("Aone2233", "Nekomari"); err != nil {
				t.Fatalf("listGitHubReleases: %v", err)
			}
			if !strings.HasPrefix(gotURL, githubAPIBaseURL+"/repos/Aone2233/Nekomari/releases") {
				t.Fatalf("request went to %q, want the GitHub releases API", gotURL)
			}
			if gotAuth != tc.wantAuth {
				t.Fatalf("Authorization = %q, want %q", gotAuth, tc.wantAuth)
			}
			// The gate and the request must always agree, in both directions.
			if present := probeCredentialsPresent(); present != (tc.wantAuth != "") {
				t.Fatalf("probeCredentialsPresent() = %v while the request carried Authorization %q", present, tc.wantAuth)
			}
		})
	}
}
