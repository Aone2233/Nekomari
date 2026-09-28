package update

// Exported hooks for the diagnostic probe in ./probe.
//
// These exist so the probe can call the real selection logic — the same functions the update path uses —
// rather than a copy of it that could disagree. They are exported for the probe alone and are not part of
// any supported interface; the update path itself does not use them.

// ListReleasesForProbe exposes listGitHubReleases.
func ListReleasesForProbe() ([]ReleaseForProbe, error) {
	owner, repo, err := splitRepoSlug(Repo)
	if err != nil {
		return nil, err
	}
	releases, err := listGitHubReleases(owner, repo)
	if err != nil {
		return nil, err
	}
	out := make([]ReleaseForProbe, 0, len(releases))
	for _, release := range releases {
		assets := make([]AssetForProbe, 0, len(release.Assets))
		for _, asset := range release.Assets {
			assets = append(assets, AssetForProbe{
				Name: asset.Name, Size: asset.Size, BrowserDownloadURL: asset.BrowserDownloadURL,
			})
		}
		out = append(out, ReleaseForProbe{
			TagName: release.TagName, Draft: release.Draft, Prerelease: release.Prerelease, Assets: assets,
		})
	}
	return out, nil
}

// FindReleaseByTagForProbe exposes findReleaseByTag.
func FindReleaseByTagForProbe(releases []ReleaseForProbe, tag, assetName string) (CandidateForProbe, bool) {
	converted := make([]githubRelease, 0, len(releases))
	for _, release := range releases {
		assets := make([]githubReleaseAsset, 0, len(release.Assets))
		for _, asset := range release.Assets {
			assets = append(assets, githubReleaseAsset{
				Name: asset.Name, Size: asset.Size, BrowserDownloadURL: asset.BrowserDownloadURL,
			})
		}
		converted = append(converted, githubRelease{
			TagName: release.TagName, Draft: release.Draft, Prerelease: release.Prerelease, Assets: assets,
		})
	}
	candidate, ok := findReleaseByTag(converted, tag, assetName)
	return CandidateForProbe{
		TagName:         candidate.TagName,
		AssetName:       candidate.Asset.Name,
		AssetURL:        candidate.Asset.BrowserDownloadURL,
		ChecksumName:    candidate.Checksum.Name,
		ChecksumURL:     candidate.Checksum.BrowserDownloadURL,
	}, ok
}

// ReleaseForProbe is a release as the probe prints it.
type ReleaseForProbe struct {
	TagName    string
	Draft      bool
	Prerelease bool
	Assets     []AssetForProbe
}

// AssetForProbe is one release asset as the probe prints it.
type AssetForProbe struct {
	Name               string
	Size               int
	BrowserDownloadURL string
}

// CandidateForProbe is a selection as the probe prints it.
type CandidateForProbe struct {
	TagName      string
	AssetName    string
	AssetURL     string
	ChecksumName string
	ChecksumURL  string
}
