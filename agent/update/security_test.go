package update

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type updateTransport func(*http.Request) (*http.Response, error)

func (f updateTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestVerifiedReleaseApplication(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	original := updateClient
	defer func() { updateClient = original }()
	body := "new test executable"
	hash := sha256.Sum256([]byte(body))
	manifest := fmt.Sprintf("%x  komari-agent-test\n", hash)
	updateClient = &http.Client{Transport: updateTransport(func(r *http.Request) (*http.Response, error) {
		data := body
		if strings.HasSuffix(r.URL.Path, "SHA256SUMS.txt") {
			data = manifest
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(data)), Header: make(http.Header)}, nil
	})}
	candidate := snapshotReleaseCandidate{Asset: githubReleaseAsset{Name: "komari-agent-test", BrowserDownloadURL: "https://github.com/test/binary"}, Checksum: githubReleaseAsset{BrowserDownloadURL: "https://github.com/test/SHA256SUMS.txt"}}
	target := filepath.Join(t.TempDir(), "agent")
	if err := os.WriteFile(target, []byte("original"), 0755); err != nil {
		t.Fatal(err)
	}
	body = "corrupt binary"
	if err := applyRelease(candidate, target); err == nil {
		t.Fatal("checksum mismatch accepted")
	}
	unchanged, _ := os.ReadFile(target)
	if string(unchanged) != "original" {
		t.Fatal("failed verification changed existing executable")
	}
	body = "new test executable"
	if err := applyRelease(candidate, target); err != nil {
		t.Fatal(err)
	}
	updated, _ := os.ReadFile(target)
	if string(updated) != body {
		t.Fatal("verified update not installed")
	}
}

func TestReleaseValidation(t *testing.T) {
	for _, url := range []string{"http://github.com/test", "https://example.com/test", ""} {
		if _, err := downloadAsset(githubReleaseAsset{BrowserDownloadURL: url}, 1024); err == nil {
			t.Fatal("untrusted download URL accepted")
		}
	}
	if _, err := releaseChecksum([]byte(""), "agent"); err == nil {
		t.Fatal("missing checksum accepted")
	}
	current, _ := parseVersion("v0.1.12")
	releases := []githubRelease{testRelease("v0.1.11", false, false, time.Time{}, "agent"), testRelease("v0.1.13", false, false, time.Time{}, "agent"), testRelease("v0.2.0", true, false, time.Time{}, "agent")}
	got, ok := selectStableRelease(releases, "agent", current)
	if !ok || got.TagName != "v0.1.13" {
		t.Fatalf("stable selection: %+v", got)
	}
}
