package public

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The embedded default theme's own contract.
//
// This exists because the archive at `defaultTheme/dist.tar.zst` is produced outside this repository
// — it is the build output of a separate theme project — and every way it can be wrong fails the
// same way: a blank page. A missing asset, a wrong path prefix or a manifest that names a file the
// archive does not contain all look identical from the outside, and none of them produces an error
// in the server log.
//
// The path rule these pin down is the one that differs from an installed theme and is easiest to get
// backwards: **an installed theme is served from `data/theme/<short>/dist/`, while the embedded
// archive holds the *contents* of `dist/` and has that prefix stripped** (see DistDir handling in
// public.go). Packing `dist/` itself, rather than what is inside it, produces a theme whose every
// asset is one directory deeper than the HTML expects.
//
// There is also a regression this caught on the way in: replacing the archive with the new theme's
// build dropped the panel's own standalone pages from it, because those live in the same archive and
// come from the panel's build rather than the theme's.

// embeddedThemeManifest is the subset of the embedded manifest these tests read.
type embeddedThemeManifest struct {
	Name        string `json:"name"`
	Short       string `json:"short"`
	Version     string `json:"version"`
	Author      string `json:"author"`
	URL         string `json:"url"`
	License     string `json:"license"`
	Copyright   string `json:"copyright"`
	Preview     string `json:"preview"`
	Description string `json:"description"`
}

func loadEmbeddedTheme(t *testing.T) (map[string][]byte, embeddedThemeManifest) {
	t.Helper()
	files, err := loadEmbeddedDist()
	if err != nil {
		t.Fatalf("the embedded theme archive does not load: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("the embedded theme archive is empty")
	}
	raw, err := PublicFS.ReadFile("defaultTheme/komari-theme.json")
	if err != nil {
		t.Fatalf("the embedded theme manifest is missing from the binary: %v", err)
	}
	var manifest embeddedThemeManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("the embedded theme manifest is not valid JSON: %v", err)
	}
	return files, manifest
}

// The manifest has to describe the theme that is actually embedded, and — because the licence's one
// condition is that its notice travels with the software — it has to carry the attribution.
func TestEmbeddedThemeManifestDescribesTheEmbeddedTheme(t *testing.T) {
	files, manifest := loadEmbeddedTheme(t)

	if manifest.Short != DefaultTheme {
		t.Errorf("manifest short = %q, want %q: public.go resolves the default theme by this name",
			manifest.Short, DefaultTheme)
	}
	if manifest.Name == "" || manifest.Version == "" || manifest.Author == "" {
		t.Errorf("manifest must name the theme, its version and its author: %+v", manifest)
	}
	if manifest.URL == "" {
		t.Error("manifest must link to the theme's source")
	}
	// The attribution requirement, not decoration: the theme is MIT and this archive is distributed
	// inside the binary. See docs/THIRD-PARTY-LICENSES.md.
	if manifest.License == "" {
		t.Error("manifest must state the embedded theme's licence")
	}
	if !strings.Contains(strings.ToLower(manifest.Copyright), "shanyang") {
		t.Errorf("manifest must carry the theme author's copyright notice, got %q", manifest.Copyright)
	}

	// And it must not still be describing the theme it replaced.
	if manifest.Name == "Komari" || manifest.Author == "Akizon77" {
		t.Error("the manifest still describes the previous default theme")
	}

	// A preview that is not in the archive is a theme card with a broken image. The upstream theme's
	// preview sits above dist/, so it is *not* in the archive — the trap this asserts against.
	if manifest.Preview != "" {
		if _, ok := files[manifest.Preview]; !ok {
			t.Errorf("manifest preview %q is not in the archive", manifest.Preview)
		}
	}
}

// The archive has to be the contents of a dist directory, not the directory itself. Getting this
// backwards serves a theme whose every asset 404s, with no error anywhere.
func TestTheEmbeddedArchiveHasItsDistPrefixStripped(t *testing.T) {
	files, _ := loadEmbeddedTheme(t)

	if _, ok := files[IndexFile]; !ok {
		names := make([]string, 0, 20)
		for name := range files {
			names = append(names, name)
			if len(names) == 20 {
				break
			}
		}
		sort.Strings(names)
		t.Fatalf("%q is not at the archive root; the archive holds the contents of dist/, not dist/ itself.\nFirst entries: %v",
			IndexFile, names)
	}
	if _, ok := files["dist/"+IndexFile]; ok {
		t.Fatalf("the archive contains %q, so it was packed one directory too deep", "dist/"+IndexFile)
	}
	// `.vite/manifest.json` is how the server identifies which asset names carry a content hash and
	// can therefore be cached indefinitely. Without it `isHashedAsset` returns false for everything,
	// so assets are still served correctly — they simply lose the long Cache-Control header, and
	// behind Cloudflare every POP re-fetches them on the provider's default (measured at four hours)
	// rather than keeping them until the content changes.
	//
	// The embedded theme does not have one: the theme project's Vite config does not enable
	// `build.manifest`, while the panel's own build does. So this is reported rather than required —
	// it is a performance regression a build flag would fix, not a fault that breaks the panel, and
	// asserting a failure here would be asserting something untrue.
	if _, ok := files[".vite/manifest.json"]; !ok {
		t.Log("no .vite/manifest.json in the embedded theme: hashed assets will be served without " +
			"long-lived Cache-Control. Fixable in the theme's vite config with build.manifest = true.")
	}
}

// serverHandledReferences are paths the HTML references that the server answers itself, without
// consulting the archive. `/favicon.ico` is the one: public.go handles it separately so a site can
// override the icon without rebuilding the theme, and the file lives in the data directory.
var serverHandledReferences = map[string]bool{
	"/favicon.ico": true,
}

// Every asset the HTML references must exist. A missing one is a blank page, and the failure is
// invisible until someone loads the panel.
func TestEveryAssetTheEmbeddedIndexReferencesExists(t *testing.T) {
	files, _ := loadEmbeddedTheme(t)

	index, ok := files[IndexFile]
	if !ok {
		t.Skip("index.html is missing, which the previous test already reports")
	}

	// src/href attributes, plus the modulepreload links Vite emits. Both are absolute paths under
	// the theme's own base, so a leading slash has to be stripped before the lookup.
	pattern := regexp.MustCompile(`(?:src|href)="(/[^"]+)"`)
	matches := pattern.FindAllStringSubmatch(string(index), -1)
	if len(matches) == 0 {
		t.Fatal("index.html references no assets at all, so this test would pass vacuously")
	}
	checked := 0
	for _, match := range matches {
		reference := match[1]
		if strings.HasPrefix(reference, "//") || strings.HasPrefix(reference, "http") {
			continue
		}
		// Data URIs and anchors are not files.
		if strings.HasPrefix(reference, "data:") || strings.HasPrefix(reference, "#") {
			continue
		}
		if serverHandledReferences[reference] {
			continue
		}
		name := strings.TrimPrefix(reference, "/")
		checked++
		if _, ok := files[name]; !ok {
			t.Errorf("index.html references %q, which is not in the archive", reference)
		}
	}
	if checked == 0 {
		t.Fatal("every reference was skipped, so this test proved nothing")
	}
}

// The panel's own standalone pages live in this archive too, because the archive is the panel's
// default theme *and* the source of every static file it serves. Replacing it with a theme build
// drops them, and the only symptom is a 404 on a page that was working.
func TestTheEmbeddedArchiveCarriesThePanelsOwnPages(t *testing.T) {
	files, _ := loadEmbeddedTheme(t)

	for _, page := range standalonePages {
		entry := fmt.Sprintf("standalone/%s/%s.html", page.name, page.name)
		if _, ok := files[entry]; !ok {
			t.Errorf("%q is missing from the embedded archive: a theme build replaced it rather than being merged into it", entry)
		}
	}
	if _, ok := files[IndexFile]; !ok {
		t.Errorf("%q is missing from the embedded archive", IndexFile)
	}
}

// The embedded theme must carry the background images its own saved configuration names.
//
// This is the defect that made the front page's background disappear: a rebuild of the theme produced a
// `dist/assets/` holding only the built bundle and the video, the eight background images had been added to the
// installed deployment by hand and were not in the theme's source, and replacing the theme with that build
// removed them. The page then asked for `/assets/bg-desktop-light.v2.jpeg` and got a 404 — a missing background
// and one console error, with nothing in any log to say why.
//
// Asserted by name against the configuration, not "some images exist": the paths come from the theme's saved
// settings, so a rename on either side is what this catches.
func TestTheEmbeddedThemeCarriesItsBackgroundImages(t *testing.T) {
	files, _ := loadEmbeddedTheme(t)

	// The paths the deployed theme's configuration refers to. Kept as a list here because the assertion is about
	// what a saved configuration will ask for, and that configuration lives in a database this test has no
	// access to — the same reason the theme's own settings are read from its manifest elsewhere.
	wanted := []string{
		"assets/bg-desktop-light.v2.jpeg",
		"assets/bg-desktop-dark.v2.jpg",
		"assets/bg-mobile-light.v2.jpg",
		"assets/bg-mobile-dark.v2.jpg",
	}
	for _, name := range wanted {
		content, ok := files[name]
		if !ok {
			t.Errorf("%q is not in the embedded archive, so a deployment whose theme settings point at it would show no background", name)
			continue
		}
		if len(content) < 10_000 {
			t.Errorf("%q is %d bytes, too small to be a background image", name, len(content))
		}
	}
}

// The archive must be small enough to ship and large enough to be a page. Both bounds are loose; the
// point is to catch an archive that is empty, or one that accidentally swallowed a build directory.
func TestTheEmbeddedArchiveIsAPlausibleSize(t *testing.T) {
	files, _ := loadEmbeddedTheme(t)

	var total int64
	for _, content := range files {
		total += int64(len(content))
	}
	// The theme's own images are the bulk of it: several JPEG backgrounds around 0.3 MB each.
	const minBytes = 1 << 20  // 1 MiB
	const maxBytes = 40 << 20 // 40 MiB
	if total < minBytes {
		t.Errorf("the embedded archive holds %d bytes, which is too little to be a theme", total)
	}
	if total > maxBytes {
		t.Errorf("the embedded archive holds %d bytes, which is too much to embed in a binary", total)
	}
}
