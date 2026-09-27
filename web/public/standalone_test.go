package public

import (
	"strings"
	"testing"
)

// The standalone SLA report is a contract between three things that fail independently:
// the Vite build writes it, `tools/zstdpack` packs `frontend/dist` into the embedded
// archive, and `static()` serves whatever is in that archive. A break anywhere produces a
// URL that 404s, which is exactly the failure this page exists to avoid repeating:
// v0.1.31 shipped the report as a route in the built-in router, an installed theme
// replaced that router, and the production panel answered its own 404 for a feature whose
// server half was live and tested.
//
// These tests assert the contract rather than the mechanism, so a change to any of the
// three steps fails here instead of in a browser.

const standalonePage = "standalone/sla.html"

// The page must be in the embedded bundle. If the build script stops copying it, or the
// path changes, this is what notices.
func TestStandaloneSlaPageIsEmbedded(t *testing.T) {
	if _, err := loadEmbeddedDist(); err != nil {
		t.Fatalf("the embedded dist archive does not load: %v", err)
	}
	content, ok := defaultDistFiles[standalonePage]
	if !ok {
		names := make([]string, 0, 16)
		for name := range defaultDistFiles {
			if strings.HasPrefix(name, "standalone/") {
				names = append(names, name)
			}
		}
		t.Fatalf("%q is not in the embedded bundle (standalone files present: %v)", standalonePage, names)
	}
	if len(content) == 0 {
		t.Fatalf("%q is embedded but empty", standalonePage)
	}
}

// It must reference its own assets under its own prefix, and nothing that resolves into
// the theme's `/assets/`. A single `chunk-*.js` reference here would be a file the
// installed theme can shadow, which is the specific collision the separate build exists
// to prevent.
func TestStandaloneSlaPageIsSelfContained(t *testing.T) {
	page := string(defaultDistFiles[standalonePage])

	if !strings.Contains(page, "/standalone/sla.js") {
		t.Errorf("%s does not reference its own bundle:\n%s", standalonePage, page)
	}
	if !strings.Contains(page, "/standalone/sla.css") {
		t.Errorf("%s does not reference its own stylesheet:\n%s", standalonePage, page)
	}
	if strings.Contains(page, `"/assets/`) {
		t.Errorf("%s references /assets/, which an installed theme owns:\n%s", standalonePage, page)
	}
	if strings.Contains(page, "registerSW") {
		t.Errorf("%s registers the panel's service worker; the report must not join the app's precache:\n%s",
			standalonePage, page)
	}
}

// The bundle has to be one file with React in it. Chunked output would resolve against
// the theme's assets, and a bundle without React renders nothing.
func TestStandaloneSlaBundleIsWhole(t *testing.T) {
	content, ok := defaultDistFiles["standalone/sla.js"]
	if !ok {
		t.Fatal("standalone/sla.js is not embedded")
	}
	bundle := string(content)

	if len(bundle) < 100_000 {
		t.Errorf("the bundle is %d bytes, too small to contain React and the page", len(bundle))
	}
	if !strings.Contains(bundle, "createRoot") {
		t.Error("the bundle has no createRoot, so it cannot mount anything")
	}
	// A relative chunk import means the build split the page, which the config forbids.
	if strings.Contains(bundle, `from"./chunk`) || strings.Contains(bundle, `from"./assets/`) {
		t.Error("the bundle imports a chunk, so it is not self-contained")
	}
}

// And the stylesheet must be there too: an unstyled page is still a broken page.
func TestStandaloneSlaStylesheetIsEmbedded(t *testing.T) {
	content, ok := defaultDistFiles["standalone/sla.css"]
	if !ok {
		t.Fatal("standalone/sla.css is not embedded")
	}
	if len(content) < 1000 {
		t.Errorf("the stylesheet is %d bytes, too small to be the theme's CSS", len(content))
	}
}

// The main build must still carry its own entry. The standalone build writes to a
// separate directory and is copied in, so a mistake there could plausibly take the panel's
// own index with it.
func TestThePanelEntryIsStillEmbeddedAlongsideIt(t *testing.T) {
	if _, ok := defaultDistFiles[IndexFile]; !ok {
		t.Fatalf("the panel's own %q is missing from the embedded bundle", IndexFile)
	}
}
