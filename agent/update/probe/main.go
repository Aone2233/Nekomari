// Diagnostic probe for a targeted agent update that fails on one node and succeeds on others.
//
// Not a test: it calls the same unexported functions the real update path does, against the real GitHub
// API, and prints what they return. Built to answer one question that a generic error message could not —
// the failing node reported `invalid release asset URL: name="" url=""`, which means the release was found
// and its asset list was empty, while the same build found nine assets from a workstation.
//
// Build and run on the node in question:
//
//     cd agent && GOFLAGS=-mod=mod go run ./update/probe -tag v0.1.44 -asset komari-agent-linux-amd64
//
// It only reads: no download, no replacement, no service restart.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/Aone2233/nekomari/agent/update"
)

func main() {
	tag := flag.String("tag", "", "release tag to look for, e.g. v0.1.44")
	asset := flag.String("asset", "", "asset name to look for")
	flag.Parse()

	if *tag == "" || *asset == "" {
		fmt.Fprintln(os.Stderr, "usage: probe -tag <tag> -asset <asset>")
		os.Exit(2)
	}

	releases, err := update.ListReleasesForProbe()
	if err != nil {
		fmt.Printf("  listGitHubReleases failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  releases returned: %d\n", len(releases))

	found := false
	for _, r := range releases {
		if r.TagName != *tag {
			continue
		}
		found = true
		fmt.Printf("  tag=%s draft=%v prerelease=%v assets=%d\n", r.TagName, r.Draft, r.Prerelease, len(r.Assets))
		for _, a := range r.Assets {
			fmt.Printf("    asset name=%q size=%d url=%q\n", a.Name, a.Size, a.BrowserDownloadURL)
		}
	}
	if !found {
		fmt.Printf("  tag %q is not in the first pages returned\n", *tag)
	}

	cand, ok := update.FindReleaseByTagForProbe(releases, *tag, *asset)
	fmt.Printf("  selection: ok=%v tag=%q assetName=%q url=%q checksumName=%q checksumURL=%q\n",
		ok, cand.TagName, cand.AssetName, cand.AssetURL, cand.ChecksumName, cand.ChecksumURL)
	if ok && cand.AssetName == "" {
		fmt.Println("  ^ the selection reports success with an empty asset, which is what the failing node saw")
	}
}
