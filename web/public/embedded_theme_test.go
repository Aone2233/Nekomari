package public

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The embedded archive's contract.
//
// The archive at `defaultTheme/dist.tar.zst` is assembled by `script/embed-theme.mjs` from this
// repository's own front-end build (`frontend/dist`), with `frontend/dist/standalone/admin` also
// copied to `admin/` at the archive root and `frontend/komari-theme.json` written in as the theme
// manifest. Until 2026-10-01 it was the build output of a **separate theme repository**; it is now
// the panel's own front end, which is what makes `/install`, `/database-recovery` and the PWA files
// the panel's screens rather than whichever theme is installed.
//
// Every way the archive can be wrong fails the same way: a blank page. A missing asset, a wrong path
// prefix or a manifest that names a file the archive does not contain all look identical from the
// outside, and none of them produces an error in the server log.
//
// The path rule that is easiest to get backwards: an **installed** theme is served from
// `data/theme/<short>/dist/`, while this archive holds the *contents* of a dist directory with that
// prefix stripped (see DistDir handling in public.go). Packing `dist/` itself, rather than what is
// inside it, produces a front end whose every asset is one directory deeper than the HTML expects.
//
// Two failures this file exists for, both caught here rather than in production:
//   - replacing the archive with a theme's build dropped the panel's own standalone pages and its PWA
//     files, because those live in the same archive and are not a theme's to supply;
//   - `/install` was answered with the *installed theme's* document, and the assertion that existed —
//     "not the installed theme's document" — could not see it: with no theme installed, the built-in
//     theme's document and the panel's document were indistinguishable under an exclusion.
//     TestTheEmbeddedArchiveRootDocumentBelongsToThePanel is that missing tooth, and it is positive:
//     it asks for what only the panel's own build can supply.

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
		t.Fatalf("the embedded archive does not load: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("the embedded archive is empty")
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

// The manifest has to describe the theme that is actually embedded — the panel's own front end — and
// it has to name a preview the archive really carries.
//
// The manifest's source is `frontend/komari-theme.json`, whose paths are relative to the theme's own
// `dist/`. The archive has no `dist/`, so `preview` has to be rewritten on the way in; a preview that
// still carries the `dist/` prefix is a theme card with a broken image and nothing in any log.
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
	// A preview that is not in the archive is a theme card with a broken image. The upstream theme's
	// preview sat above dist/, so it was *not* in the archive — the trap this asserts against.
	if manifest.Preview != "" {
		if _, ok := files[manifest.Preview]; !ok {
			t.Errorf("manifest preview %q is not in the archive", manifest.Preview)
		}
	}

	// The archive also carries the manifest at its root, and that is the copy the panel serves from
	// `/themes/default/komari-theme.json` (the one the theme page reads). It is written from the same
	// source as the embedded copy, so the two disagreeing means the archive was assembled against a
	// different manifest than the binary was built with.
	archived, ok := files["komari-theme.json"]
	if !ok {
		t.Fatalf("the archive carries no %q at its root", "komari-theme.json")
	}
	var served embeddedThemeManifest
	if err := json.Unmarshal(archived, &served); err != nil {
		t.Fatalf("the manifest inside the archive is not valid JSON: %v", err)
	}
	if served != manifest {
		t.Errorf("the manifest inside the archive describes a different theme than the embedded one:\n"+
			"archive:  %+v\nembedded: %+v", served, manifest)
	}
}

// The archive has to be the contents of a dist directory, not the directory itself. Getting this
// backwards serves a front end whose every asset 404s, with no error anywhere.
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

	prefixed := make([]string, 0, 8)
	for name := range files {
		if name == DistDir || strings.HasPrefix(name, DistDir+"/") {
			prefixed = append(prefixed, name)
		}
	}
	if len(prefixed) > 0 {
		sort.Strings(prefixed)
		t.Errorf("%d entries are under the %q prefix, so the archive was packed one directory too deep (first: %v)",
			len(prefixed), DistDir+"/", prefixed[:min(5, len(prefixed))])
	}

	// `.vite/manifest.json` is how the server identifies which asset names carry a content hash and
	// can therefore be cached indefinitely. Without it `isHashedAsset` returns false for everything,
	// so assets are still served correctly — they simply lose the long Cache-Control header, and
	// behind Cloudflare every POP re-fetches them on the provider's default (measured at four hours)
	// rather than keeping them until the content changes.
	//
	// Required rather than merely reported since the archive became the panel's own build:
	// `frontend/vite.config.ts` sets `build.manifest = true`, so its absence means a build flag was
	// turned off — a silent performance regression, not a fault that breaks the panel.
	if _, ok := files[".vite/manifest.json"]; !ok {
		t.Error("the embedded archive has no .vite/manifest.json, so no asset can be known to be a build output and none gets a long-lived Cache-Control")
	}
}

// serverHandledReferences are paths the HTML references that the server answers itself, without
// consulting the archive. `/favicon.ico` is the one: public.go handles it separately so a site can
// override the icon without rebuilding the front end, and the file lives in the data directory.
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
	// the front end's own base, so a leading slash has to be stripped before the lookup.
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

// The panel's own front end *is* this archive: its document, the PWA files the build emits, the
// standalone pages and the admin subtree. Replacing the archive with a theme build drops them, and the
// only symptom is a blank page or a 404 on a page that was working.
func TestTheEmbeddedArchiveCarriesThePanelsOwnPages(t *testing.T) {
	files, _ := loadEmbeddedTheme(t)

	if _, ok := files[IndexFile]; !ok {
		t.Errorf("%q is missing from the embedded archive", IndexFile)
	}
	for _, page := range standalonePages {
		entry := fmt.Sprintf("standalone/%s/%s.html", page.name, page.name)
		if _, ok := files[entry]; !ok {
			t.Errorf("%q is missing from the embedded archive: a theme build replaced it rather than being merged into it", entry)
		}
	}
	// The panel's document registers the service worker its build generates, and the PWA manifests are
	// what make the panel installable: without them every first visit logs a 404 and installs nothing.
	for _, name := range []string{
		"sw.js",
		"registerSW.js",
		"manifest.json",
		"manifest.webmanifest",
		"favicon.ico",
	} {
		if _, ok := files[name]; !ok {
			t.Errorf("%q is missing from the embedded archive: it belongs to the panel's own build, not to a theme", name)
		}
	}
	// Workbox's runtime is named after its own version, so it is matched rather than named.
	workbox := false
	for name := range files {
		if strings.HasPrefix(name, "workbox-") && strings.HasSuffix(name, ".js") {
			workbox = true
			break
		}
	}
	if !workbox {
		t.Error("no workbox-*.js in the embedded archive, so the service worker the panel ships cannot run")
	}
	// The admin is the panel's own interface, served out of this subtree for `/admin`
	// (see admin_theme_test.go). A theme build at the archive root would drop it.
	if _, ok := files[AdminDistDir+"/"+IndexFile]; !ok {
		t.Errorf("%q is missing: /admin would render nothing", AdminDistDir+"/"+IndexFile)
	}
}

// panelFrontEndTitle is the title of the panel's own front-end document. It is the string public.go's
// site-name replacer keys on, so it is part of the server's contract with the front end, not
// decoration.
const panelFrontEndTitle = "<title>Nekomari Monitor</title>"

// panelFrontEndEntryPrefix is how the panel's build names its entry scripts:
// `frontend/vite.config.ts` sets `entryFileNames: "assets/entry-[name]-[hash].js"`. A theme build uses
// Vite's default `assets/index-<hash>.js` (the previous default theme, LuminaPlus, does), so this
// prefix is itself evidence of which build a document came from — and the bundle behind it has to be
// the panel's app for that evidence to hold.
const panelFrontEndEntryPrefix = "/assets/entry-"

// panelAppRoutes are routes that exist only in the panel's own front end (frontend/src/routes.ts):
// the installer and the database-recovery screen. A theme has no route for either — it would render
// its own 404. They are matched as the route table writes them into the bundle.
var panelAppRoutes = []string{`path:"/install"`, `path:"/database-recovery"`}

// panelFrontEndEntry returns the module script a document loads, e.g.
// "/assets/entry-index-uglIRtEP.js", or "" when it loads none.
func panelFrontEndEntry(document string) string {
	match := regexp.MustCompile(`<script[^>]*\btype="module"[^>]*\bsrc="([^"]+)"`).FindStringSubmatch(document)
	if match == nil {
		return ""
	}
	return match[1]
}

// panelDocumentProblems reports why document is not a build of the panel's own front end. It returns
// reasons rather than failing so the check itself can be exercised against a theme's document by
// TestThePanelOwnershipCheckRejectsAThemeDocument.
//
// Why the assertion is positive and not "not the installed theme's document": production answered
// `/install` with the *built-in* theme's document, and an exclusion cannot tell that apart from the
// panel's own document when no theme is installed — before 2026-10-01 the built-in theme *was* that
// document, so the check passed while the defect shipped. Only the panel's build carries the panel's
// title, the panel's entry naming and the panel's own routes.
func panelDocumentProblems(files map[string][]byte, document string) []string {
	var problems []string

	if !strings.Contains(document, panelFrontEndTitle) {
		problems = append(problems, fmt.Sprintf(
			"it does not carry %s, which is the panel front end's own title", panelFrontEndTitle))
	}

	entry := panelFrontEndEntry(document)
	switch {
	case entry == "":
		return append(problems, "it loads no module entry at all")
	case !strings.HasPrefix(entry, panelFrontEndEntryPrefix):
		return append(problems, fmt.Sprintf(
			"it loads %q, but the panel's build names its entries %s*", entry, panelFrontEndEntryPrefix))
	}

	content, ok := files[strings.TrimPrefix(entry, "/")]
	if !ok {
		return append(problems, fmt.Sprintf("it loads %q, which is not in the embedded archive", entry))
	}
	for _, route := range panelAppRoutes {
		if !bytes.Contains(content, []byte(route)) {
			problems = append(problems, fmt.Sprintf(
				"%q is not the panel's app: it has no %s route", entry, route))
		}
	}
	return problems
}

// assertDocumentIsThePanelsOwnFrontEnd fails the test unless document is a build of the panel's own
// front end. source names the response or file in the failure message.
func assertDocumentIsThePanelsOwnFrontEnd(t *testing.T, files map[string][]byte, source, document string) {
	t.Helper()
	for _, problem := range panelDocumentProblems(files, document) {
		t.Errorf("%s is not the panel's own front end: %s", source, problem)
	}
}

// The root document of the embedded archive must be the panel's own front end, not a theme's.
//
// This is the tooth the `/install` defect got past. public.go answers a panel-owned path (`/install`,
// `/database-recovery`, `/admin`, `/terminal`) from the *built-in* front end, and the assertion that
// existed only required "the installed theme's document does not answer it" — which the built-in
// theme's document satisfies. The root document is now the panel's own build, so the positive
// assertion below is both available and the only one that distinguishes the two.
func TestTheEmbeddedArchiveRootDocumentBelongsToThePanel(t *testing.T) {
	files, _ := loadEmbeddedTheme(t)

	document, ok := files[IndexFile]
	if !ok {
		t.Fatalf("%q is missing from the embedded archive", IndexFile)
	}
	assertDocumentIsThePanelsOwnFrontEnd(t, files, "the embedded archive's "+IndexFile, string(document))
}

// The check above must fail for a theme's document, or it is not the tooth it claims to be.
//
// The document below is shaped like a real theme build — the previous default theme's, in fact: its
// own title, its own entry name (`assets/index-<hash>.js`, Vite's default), and a bundle with no panel
// route. The second case is the one an exclusion-based check could never catch: a theme that copies
// the panel's title still loads a theme's entry.
func TestThePanelOwnershipCheckRejectsAThemeDocument(t *testing.T) {
	const themeDocument = `<!DOCTYPE html><html lang="zh-CN"><head><meta charset="UTF-8" />` +
		`<title>Komari-Theme-LuminaPlus</title>` +
		`<script type="module" crossorigin src="/assets/index-Bz70e-cf.js"></script>` +
		`</head><body><div id="root" class="relative z-30"></div></body></html>`
	files := map[string][]byte{
		IndexFile: []byte(themeDocument),
		"assets/index-Bz70e-cf.js": []byte(
			`import{createRoot}from"./chunk-react-B6sZ2b7g.js";createRoot(document.getElementById("root"));`),
	}

	if problems := panelDocumentProblems(files, themeDocument); len(problems) == 0 {
		t.Fatal("a theme document passed the panel-ownership check, so the check has no teeth")
	}

	impersonating := strings.Replace(themeDocument, "Komari-Theme-LuminaPlus", "Nekomari Monitor", 1)
	if problems := panelDocumentProblems(files, impersonating); len(problems) == 0 {
		t.Fatal("a theme document that copies the panel's title passed the check: the title alone is not the criterion")
	}
}

// Every image the archive's own documents name must be in the archive.
//
// This replaces an assertion about the previous theme's four background JPEGs, which this archive does
// not contain and does not serve. What it keeps is the failure that assertion was written for: a
// rebuild replaces a theme's assets, and an image a document or a saved configuration still points at
// goes missing — a broken image and one console error, with nothing in any log to say why. The
// documents checked here are the ones the panel itself ships: its shell, its two PWA manifests and the
// theme manifest's preview.
func TestTheEmbeddedArchiveCarriesEveryImageItsDocumentsReference(t *testing.T) {
	files, manifest := loadEmbeddedTheme(t)

	// archive name -> the document that names it.
	referenced := map[string]string{}
	for _, document := range []string{IndexFile, "manifest.json", "manifest.webmanifest"} {
		content, ok := files[document]
		if !ok {
			t.Errorf("%q is missing from the archive, so the images it references cannot be checked", document)
			continue
		}
		for _, reference := range imageReferences(string(content)) {
			referenced[reference] = document
		}
	}
	if manifest.Preview != "" {
		referenced[manifest.Preview] = "komari-theme.json (preview)"
	}

	if len(referenced) == 0 {
		t.Fatal("no document in the archive names an image, so this test would pass vacuously")
	}
	names := make([]string, 0, len(referenced))
	for name := range referenced {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if serverHandledReferences["/"+name] {
			continue
		}
		if _, ok := files[name]; !ok {
			t.Errorf("%s references %q, which is not in the archive: the image would be missing, with nothing logged",
				referenced[name], name)
		}
	}
}

// imageReferences pulls the archive-relative paths of the images a document names.
//
// Both the HTML shell and the PWA manifests use absolute paths, but they spell the attribute
// differently (`src="/x.png"` against `"src": "/x.png"`), and only `src`/`href` are read: the shell
// carries commented-out `content="/images/komari-preview.png"` social-card references that name files
// the archive was never meant to contain.
func imageReferences(document string) []string {
	var out []string
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?:src|href)="(/[^"]+)"`),
		regexp.MustCompile(`"(?:src|href)"\s*:\s*"(/[^"]+)"`),
	}
	isImage := regexp.MustCompile(`(?i)\.(ico|png|jpe?g|webp|gif|svg|avif)$`)
	for _, pattern := range patterns {
		for _, match := range pattern.FindAllStringSubmatch(document, -1) {
			reference := match[1]
			if !isImage.MatchString(reference) {
				continue
			}
			out = append(out, strings.TrimPrefix(reference, "/"))
		}
	}
	return out
}

// The archive must be small enough to ship and large enough to be a front end. Both bounds are loose;
// the point is to catch an archive that is empty, or one that accidentally swallowed a build
// directory.
func TestTheEmbeddedArchiveIsAPlausibleSize(t *testing.T) {
	files, _ := loadEmbeddedTheme(t)

	var total int64
	for _, content := range files {
		total += int64(len(content))
	}
	// The panel's bundle and the Monaco language chunks the editor loads on demand are the bulk of it.
	const minBytes = 1 << 20  // 1 MiB
	const maxBytes = 40 << 20 // 40 MiB
	if total < minBytes {
		t.Errorf("the embedded archive holds %d bytes, which is too little to be the panel's front end", total)
	}
	if total > maxBytes {
		t.Errorf("the embedded archive holds %d bytes, which is too much to embed in a binary", total)
	}
}
