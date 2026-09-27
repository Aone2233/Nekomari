package public

import (
	"fmt"
	"strings"
	"testing"
)

// The standalone pages' artefact contract: the Vite build writes each page, `tools/zstdpack`
// packs `frontend/dist` into the embedded archive, and `static()` serves whatever is in that
// archive. Three steps that fail independently, and a break anywhere produces a URL that
// 404s — which is exactly the failure this arrangement exists to stop repeating: v0.1.31
// shipped the SLA report as a route in the built-in router, an installed theme replaced that
// router, and the production panel answered its own 404 for a feature whose server half was
// live and tested.
//
// Written as a table rather than per page, so a new page is a row. The source-level half of
// the same contract is `frontend/script/standalone-pages.test.mjs`.

// standalonePage describes one page's built output.
type standalonePage struct {
	// name is the directory under standalone/ and the file stem.
	name string
	// minBundleBytes is a floor that catches a bundle which built but lost its dependencies:
	// React alone is a few hundred kilobytes, so a page far below that is not self-contained.
	minBundleBytes int
}

var standalonePages = []standalonePage{
	{name: "sla", minBundleBytes: 200_000},
	{name: "bulk", minBundleBytes: 200_000},
}

// Each page must be in the embedded bundle. If the build script stops copying it, or the path
// changes, this is what notices.
func TestStandalonePagesAreEmbedded(t *testing.T) {
	if _, err := loadEmbeddedDist(); err != nil {
		t.Fatalf("the embedded dist archive does not load: %v", err)
	}
	for _, page := range standalonePages {
		entry := fmt.Sprintf("standalone/%s/%s.html", page.name, page.name)
		content, ok := defaultDistFiles[entry]
		if !ok {
			present := make([]string, 0, 16)
			for name := range defaultDistFiles {
				if strings.HasPrefix(name, "standalone/") {
					present = append(present, name)
				}
			}
			t.Errorf("%q is not in the embedded bundle (standalone files present: %v)", entry, present)
			continue
		}
		if len(content) == 0 {
			t.Errorf("%q is embedded but empty", entry)
		}
	}
}

// Each page must reference its own assets under its own prefix, and nothing that resolves into
// the theme's `/assets/`. A single `chunk-*.js` reference would be a file the installed theme
// can shadow, which is the collision the separate build exists to prevent.
func TestStandalonePagesAreSelfContained(t *testing.T) {
	for _, page := range standalonePages {
		entry := fmt.Sprintf("standalone/%s/%s.html", page.name, page.name)
		content := string(defaultDistFiles[entry])
		if content == "" {
			continue // the previous test already reported the absence
		}

		bundle := fmt.Sprintf("/standalone/%s/%s.js", page.name, page.name)
		style := fmt.Sprintf("/standalone/%s/%s.css", page.name, page.name)
		if !strings.Contains(content, bundle) {
			t.Errorf("%s does not reference its own bundle %q:\n%s", entry, bundle, content)
		}
		if !strings.Contains(content, style) {
			t.Errorf("%s does not reference its own stylesheet %q:\n%s", entry, style, content)
		}
		if strings.Contains(content, `"/assets/`) {
			t.Errorf("%s references /assets/, which an installed theme owns:\n%s", entry, content)
		}
		if strings.Contains(content, "registerSW") {
			t.Errorf("%s registers the panel's service worker; a standalone page must not join the app's precache:\n%s", entry, content)
		}
	}
}

// Each bundle has to be one file with React in it. Chunked output would resolve against the
// theme's assets, and a bundle without React renders nothing.
func TestStandaloneBundlesAreWhole(t *testing.T) {
	for _, page := range standalonePages {
		entry := fmt.Sprintf("standalone/%s/%s.js", page.name, page.name)
		content, ok := defaultDistFiles[entry]
		if !ok {
			t.Errorf("%q is not embedded", entry)
			continue
		}
		bundle := string(content)
		if len(bundle) < page.minBundleBytes {
			t.Errorf("%s is %d bytes, below the %d floor: too small to contain React and the page",
				entry, len(bundle), page.minBundleBytes)
		}
		if !strings.Contains(bundle, "createRoot") {
			t.Errorf("%s has no createRoot, so it cannot mount anything", entry)
		}
		// A relative chunk import means the build split the page, which the config forbids.
		if strings.Contains(bundle, `from"./chunk`) || strings.Contains(bundle, `from"./assets/`) {
			t.Errorf("%s imports a chunk, so it is not self-contained", entry)
		}
	}
}

// Each stylesheet must be there too: an unstyled page is still a broken page.
func TestStandaloneStylesheetsAreEmbedded(t *testing.T) {
	for _, page := range standalonePages {
		entry := fmt.Sprintf("standalone/%s/%s.css", page.name, page.name)
		content, ok := defaultDistFiles[entry]
		if !ok {
			t.Errorf("%q is not embedded", entry)
			continue
		}
		if len(content) < 1000 {
			t.Errorf("%s is %d bytes, too small to be the theme's CSS", entry, len(content))
		}
	}
}

// The main build must still carry its own entry. The standalone builds write to separate
// directories and are copied in, so a mistake there could plausibly take the panel's own index
// with it — and that failure would be a blank panel, not a missing report.
func TestThePanelEntrySurvivesTheStandaloneBuilds(t *testing.T) {
	if _, ok := defaultDistFiles[IndexFile]; !ok {
		t.Fatalf("the panel's own %q is missing from the embedded bundle", IndexFile)
	}
}

// No standalone page may claim a path outside its own directory. `static()` prefers the theme
// directory over the embedded bundle, so a page at a path a theme might legally use is a page
// that disappears when a theme installs a file there.
func TestStandaloneFilesStayUnderTheirPrefix(t *testing.T) {
	for name := range defaultDistFiles {
		if !strings.Contains(name, "standalone") {
			continue
		}
		if !strings.HasPrefix(name, "standalone/") {
			t.Errorf("%q mentions standalone but is not under the standalone/ prefix", name)
		}
	}
}
