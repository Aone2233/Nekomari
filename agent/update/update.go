package update

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/Aone2233/nekomari/agent/dnsresolver"
	"github.com/blang/semver"
	binaryupdate "github.com/inconshreveable/go-update"
)

var (
	CurrentVersion string = "0.0.1"
	// Repo 是自动更新所查询的 GitHub 仓库（owner/name）。
	//
	// 注意：这是 fork 的仓库，不是上游！上游把 agent 与面板分在两个仓库，
	// 而 Nekomari 是单仓，发布产物集中在同一个仓库里。
	// 若这里沿用上游的 slug，fork 出来的 agent 会在一次自动更新后把自己
	// 替换成上游二进制，从而静默丢掉本 fork 的全部新功能。
	// 可通过 --update-repo / AGENT_UPDATE_REPO 覆盖。
	Repo         string = "Aone2233/Nekomari"
	updateClient        = dnsresolver.GetHTTPClient(60 * time.Second)
	updateMu     sync.Mutex
)

const (
	snapshotVersionPrefix = "Snapshot-"
	containerMarkerPath   = "/.komari-agent-container"
	githubAPIBaseURL      = "https://api.github.com"
)

type buildTrack int

const (
	stableTrack buildTrack = iota
	snapshotTrack
)

type githubRelease struct {
	TagName     string               `json:"tag_name"`
	Name        string               `json:"name"`
	Body        string               `json:"body"`
	Draft       bool                 `json:"draft"`
	Prerelease  bool                 `json:"prerelease"`
	HTMLURL     string               `json:"html_url"`
	PublishedAt time.Time            `json:"published_at"`
	Assets      []githubReleaseAsset `json:"assets"`
}

type githubReleaseAsset struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	Size               int    `json:"size"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type snapshotReleaseCandidate struct {
	TagName     string
	Name        string
	Body        string
	HTMLURL     string
	PublishedAt time.Time
	Asset       githubReleaseAsset
	Checksum    githubReleaseAsset
}

// parseVersion 解析可能带有 v/V 前缀，以及预发布或构建元数据的版本字符串
func parseVersion(ver string) (semver.Version, error) {
	ver = strings.TrimPrefix(ver, "v")
	ver = strings.TrimPrefix(ver, "V")
	return semver.ParseTolerant(ver)
}

// needUpdate 判断是否需要更新
func needUpdate(current, latest semver.Version) bool {
	// 返回最新版本大于当前版本时需要更新
	return latest.Compare(current) > 0
}

func detectBuildTrack(version string) buildTrack {
	if strings.HasPrefix(version, snapshotVersionPrefix) {
		return snapshotTrack
	}
	return stableTrack
}

func expectedAssetName(goos, goarch string) string {
	name := fmt.Sprintf("komari-agent-%s-%s", goos, goarch)
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

func findReleaseAsset(release githubRelease, assetName string) (githubReleaseAsset, bool) {
	for _, asset := range release.Assets {
		if asset.Name == assetName {
			return asset, true
		}
	}
	return githubReleaseAsset{}, false
}

func selectLatestSnapshotRelease(releases []githubRelease, assetName string) (snapshotReleaseCandidate, bool) {
	var latest snapshotReleaseCandidate
	found := false

	for _, release := range releases {
		if release.Draft || !release.Prerelease || !strings.HasPrefix(release.TagName, snapshotVersionPrefix) {
			continue
		}

		asset, ok := findReleaseAsset(release, assetName)
		if !ok {
			continue
		}

		candidate := snapshotReleaseCandidate{
			TagName:     release.TagName,
			Name:        release.Name,
			Body:        release.Body,
			HTMLURL:     release.HTMLURL,
			PublishedAt: release.PublishedAt,
			Asset:       asset,
		}
		candidate.Checksum, _ = findReleaseAsset(release, "SHA256SUMS.txt")

		if !found ||
			candidate.PublishedAt.After(latest.PublishedAt) ||
			(candidate.PublishedAt.Equal(latest.PublishedAt) && candidate.TagName > latest.TagName) {
			latest = candidate
			found = true
		}
	}

	return latest, found
}

func snapshotNeedsUpdate(currentVersion string, latest snapshotReleaseCandidate) bool {
	return currentVersion != latest.TagName
}

func isContainerAgent() bool {
	_, err := os.Stat(containerMarkerPath)
	return err == nil
}

func splitRepoSlug(slug string) (string, string, error) {
	parts := strings.Split(slug, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid repo slug %q, expected owner/name", slug)
	}
	return parts[0], parts[1], nil
}

func listGitHubReleases(owner, repo string) ([]githubRelease, error) {
	var releases []githubRelease

	for page := 1; page <= 3; page++ {
		endpoint := fmt.Sprintf(
			"%s/repos/%s/%s/releases?per_page=100&page=%d",
			githubAPIBaseURL,
			url.PathEscape(owner),
			url.PathEscape(repo),
			page,
		)
		req, err := http.NewRequest(http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create GitHub releases request: %w", err)
		}

		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", "komari-agent")
		if token := os.Getenv("GITHUB_TOKEN"); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}

		resp, err := updateClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("failed to list GitHub releases: %w", err)
		}

		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
			return nil, fmt.Errorf("GitHub releases API returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}

		var pageReleases []githubRelease
		if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&pageReleases); err != nil {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("failed to decode GitHub releases response: %w", err)
		}
		_ = resp.Body.Close()

		releases = append(releases, pageReleases...)
		if len(pageReleases) < 100 {
			return releases, nil
		}
	}
	return releases, nil
}

func currentExecutablePath() (string, error) {
	cmdPath, err := os.Executable()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "windows" && !strings.HasSuffix(cmdPath, ".exe") {
		cmdPath += ".exe"
	}

	stat, err := os.Lstat(cmdPath)
	if err != nil {
		return "", fmt.Errorf("failed to stat %q: %w", cmdPath, err)
	}
	if stat.Mode()&os.ModeSymlink != 0 {
		resolved, err := filepath.EvalSymlinks(cmdPath)
		if err != nil {
			return "", fmt.Errorf("failed to resolve symlink %q for executable: %w", cmdPath, err)
		}
		cmdPath = resolved
	}

	return cmdPath, nil
}

func DoUpdateWorks() {
	ticker_ := time.NewTicker(time.Duration(6) * time.Hour)
	for range ticker_.C {
		CheckAndUpdate()
	}
}

func checkAndUpdate() error {
	if isContainerAgent() {
		log.Println("Agent is running in a container; refresh its image to update.")
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
	latest, found := selectLatestSnapshotRelease(releases, assetName)
	if detectBuildTrack(CurrentVersion) == stableTrack {
		current, err := parseVersion(CurrentVersion)
		if err != nil {
			return fmt.Errorf("invalid current version: %w", err)
		}
		latest, found = selectStableRelease(releases, assetName, current)
	}
	if !found {
		log.Printf("No newer release asset was found for %s.", assetName)
		return nil
	}

	if !snapshotNeedsUpdate(CurrentVersion, latest) {
		log.Println("Current snapshot version is the latest:", CurrentVersion)
		return nil
	}

	cmdPath, err := currentExecutablePath()
	if err != nil {
		return fmt.Errorf("failed to resolve current executable path: %w", err)
	}

	log.Printf("Will update %s from %s to %s\n", cmdPath, CurrentVersion, latest.TagName)
	if err := applyRelease(latest, cmdPath); err != nil {
		return fmt.Errorf("failed to update to %s: %w", latest.TagName, err)
	}

	log.Printf("Successfully updated to version %s\n", latest.TagName)
	os.Exit(42)
	return nil
}

// 检查更新并执行自动更新
func CheckAndUpdate() error {
	if !updateMu.TryLock() {
		return fmt.Errorf("update already in progress")
	}
	defer updateMu.Unlock()
	log.Println("Checking update...")
	return checkAndUpdate()
}

func selectStableRelease(releases []githubRelease, name string, current semver.Version) (snapshotReleaseCandidate, bool) {
	var latest snapshotReleaseCandidate
	best := current
	for _, release := range releases {
		version, err := parseVersion(release.TagName)
		if err != nil || release.Draft || release.Prerelease || !needUpdate(best, version) {
			continue
		}
		asset, ok := findReleaseAsset(release, name)
		if !ok {
			continue
		}
		checksum, _ := findReleaseAsset(release, "SHA256SUMS.txt")
		latest = snapshotReleaseCandidate{TagName: release.TagName, Asset: asset, Checksum: checksum}
		best = version
	}
	return latest, latest.TagName != ""
}

func downloadAsset(asset githubReleaseAsset, limit int64) ([]byte, error) {
	parsed, err := url.Parse(asset.BrowserDownloadURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "github.com" {
		return nil, fmt.Errorf("invalid release asset URL")
	}
	if asset.Size < 0 || int64(asset.Size) > limit {
		return nil, fmt.Errorf("release asset exceeds size limit")
	}
	req, err := http.NewRequest(http.MethodGet, asset.BrowserDownloadURL, nil)
	if err != nil {
		return nil, err
	}
	// Authenticated API downloads support private repositories without sending
	// the GitHub token to an arbitrary release download host.
	if token := os.Getenv("GITHUB_TOKEN"); token != "" && asset.ID > 0 {
		owner, repo, err := splitRepoSlug(Repo)
		if err != nil {
			return nil, err
		}
		req.URL, _ = url.Parse(fmt.Sprintf("%s/repos/%s/%s/releases/assets/%d", githubAPIBaseURL, url.PathEscape(owner), url.PathEscape(repo), asset.ID))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/octet-stream")
	}
	resp, err := updateClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("asset download returned status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("release asset exceeds size limit")
	}
	return data, nil
}

func releaseChecksum(manifest []byte, name string) ([]byte, error) {
	var checksum []byte
	for _, line := range strings.Split(string(manifest), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != name {
			continue
		}
		value, err := hex.DecodeString(fields[0])
		if err != nil || len(value) != 32 || checksum != nil {
			return nil, fmt.Errorf("invalid or duplicate SHA256 checksum")
		}
		checksum = value
	}
	if checksum == nil {
		return nil, fmt.Errorf("release checksum missing for %s", name)
	}
	return checksum, nil
}

func applyRelease(release snapshotReleaseCandidate, target string) error {
	manifest, err := downloadAsset(release.Checksum, 1<<20)
	if err != nil {
		return err
	}
	checksum, err := releaseChecksum(manifest, release.Asset.Name)
	if err != nil {
		return err
	}
	body, err := downloadAsset(release.Asset, 64<<20)
	if err != nil {
		return err
	}
	return binaryupdate.Apply(bytes.NewReader(body), binaryupdate.Options{TargetPath: target, Checksum: checksum})
}
