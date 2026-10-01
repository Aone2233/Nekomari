package public

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Aone2233/nekomari/internal/config"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// The admin interface's artefact contract (roadmap H7).
//
// The panel's admin pages used to come from the embedded default theme, which meant swapping that theme
// silently removed them: the theme it was swapped for declares no admin route, and everything under
// `/admin` is its service-worker recovery screen. A fresh installation had no way to administer the panel.
// The admin is now built separately (`frontend/vite.admin.config.ts`, base `/admin/`) and served out of the
// `admin/` subtree of the embedded archive — which `script/embed-theme.mjs` produces by copying
// `frontend/dist/standalone/admin` to the archive root, so `/admin/...` and `/standalone/admin/...` carry
// the same build.
//
// These tests are about the half that fails silently: a wrong path serves a *document* whose script 404s,
// which is a blank page and no error in the log. So both halves are asserted — the document is reachable at
// `/admin`, and its assets resolve from the same subtree.

func newRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	t.Chdir(t.TempDir())
	// An in-memory config database, the same arrangement `public_test.go` uses. The theme key is left
	// unset, so `getConfig` answers with its own default — which is `DefaultTheme`, the situation this
	// exists to verify: a fresh installation with no theme chosen.
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open config db: %v", err)
	}
	config.SetDb(db)

	router := gin.New()
	Static(router.Group("/"), func(handlers ...gin.HandlerFunc) {
		router.NoRoute(handlers...)
	})
	return router
}

func get(t *testing.T, router *gin.Engine, path string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	router.ServeHTTP(recorder, request)
	return recorder
}

// The document has to be served for `/admin` and for any path under it, because the admin's own routes are
// handled client-side: the server cannot know which of them exist.
func TestTheAdminDocumentIsServedForItsPrefix(t *testing.T) {
	router := newRouter(t)

	for _, path := range []string{"/admin", "/admin/", "/admin/servers", "/admin/dashboard", "/admin/settings/site", "/terminal", "/terminal/"} {
		recorder := get(t, router, path)
		if recorder.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, recorder.Code)
			continue
		}
		body := recorder.Body.String()
		if !strings.Contains(body, "<div id=\"root\">") {
			t.Errorf("GET %s did not serve the admin document:\n%s", path, body[:min(200, len(body))])
		}
		// The document must load its own bundle from `/admin/`, not from the theme root: a document whose
		// script 404s is a blank page and no error anywhere.
		if !strings.Contains(body, "src=\"/admin/assets/") {
			t.Errorf("GET %s served a document that does not load its bundle from /admin/assets/:\n%s",
				path, body[:min(300, len(body))])
		}
	}
}

// The bundle and its chunks have to be reachable, and that is a separate failure from the document being
// reachable: the document is one file the route names explicitly, its assets are looked up by name from the
// archive.
func TestTheAdminBundleAndItsChunksResolve(t *testing.T) {
	files, _ := loadEmbeddedDist()
	router := newRouter(t)

	// Every asset the admin document references, plus one lazily-imported chunk, so the check is not just
	// "the entry script exists".
	document := get(t, router, "/admin/").Body.String()
	references := regexpAssetReferences(document)
	if len(references) == 0 {
		t.Fatal("the admin document references no assets, so this test would pass vacuously")
	}

	checkedChunk := false
	for _, reference := range references {
		recorder := get(t, router, reference)
		if recorder.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200: a referenced asset that 404s renders nothing", reference, recorder.Code)
			continue
		}
		if strings.Contains(recorder.Header().Get("Content-Type"), "text/html") {
			t.Errorf("GET %s served HTML, so the asset route fell through to the SPA branch", reference)
		}
	}
	// A lazy chunk from the admin's own output, found by name rather than by parsing the bundle.
	var checked string
	for name := range files {
		if strings.HasPrefix(name, "admin/assets/") && strings.HasSuffix(name, ".js") &&
			!strings.Contains(document, name) {
			checked = name
			break
		}
	}
	if checked != "" {
		if recorder := get(t, router, "/"+checked); recorder.Code == http.StatusOK {
			checkedChunk = true
		} else {
			t.Errorf("GET /%s = %d, want 200: a lazily imported admin chunk is not reachable", checked, recorder.Code)
		}
	}
	if !checkedChunk {
		t.Log("no separate admin chunk found to check; the entry's own assets were verified")
	}
}

// The archive has to carry the admin's own manifest, or its hashed assets are served without the long-lived
// cache header — the same defect found in the theme.
// The admin's hashed assets must get the long-lived cache header, which means the server has to read the
// *admin's* manifest rather than the theme's.
//
// Getting that wrong is silent: assets are still served correctly, they just lose `Cache-Control`, so every
// POP re-fetches files that never change. The first version passed `themeHashedAssets(DefaultTheme)`, which
// reads `dist/.vite/manifest.json` — the theme's — and returned an empty set for every admin asset.
func TestTheAdminHashedAssetsAreCachedForALongTime(t *testing.T) {
	router := newRouter(t)

	document := get(t, router, "/admin/").Body.String()
	references := regexpAssetReferences(document)
	if len(references) == 0 {
		t.Fatal("the admin document references no assets")
	}

	for _, reference := range references {
		recorder := get(t, router, reference)
		if recorder.Code != http.StatusOK {
			continue // reachability is another test's assertion
		}
		header := recorder.Header().Get("Cache-Control")
		if !strings.Contains(header, "max-age=31536000") {
			t.Errorf("GET %s has Cache-Control %q, want a long-lived one: the admin manifest is probably not being read",
				reference, header)
		}
	}
}
func TestTheAdminArchiveCarriesItsManifest(t *testing.T) {
	files, _ := loadEmbeddedDist()

	if _, ok := files["admin/index.html"]; !ok {
		t.Fatal("admin/index.html is not in the archive")
	}
	if _, ok := files["admin/.vite/manifest.json"]; !ok {
		t.Error("admin/.vite/manifest.json is not in the archive, so admin assets lose their long-lived Cache-Control")
	}
	// And it is the admin's manifest rather than the theme's, so this cannot be satisfied by the wrong file.
	//
	// It asserts on the *source* name, `admin.html`, while the check above asserts on the output name,
	// `index.html`: Vite keys its manifest by input path and names the emitted document after that same
	// input, with the rename to `index.html` done by the build script afterwards. Two names for one file is
	// a trap worth naming rather than glossing over — the first version of this assertion used the output
	// name and failed on a correct archive.
	manifest, ok := files["admin/.vite/manifest.json"]
	if ok && !strings.Contains(string(manifest), "admin.html") {
		t.Error("the archive's admin manifest does not describe admin.html, so it is not the admin's build output")
	}
}

// The embedded theme's preview has to be reachable at the path its own manifest names.
//
// This is the failure that took two releases to clear: the manifest said `preview.png`, the archive carried it,
// and `/themes/default/preview.png` still answered 404 — because `openAsset`'s embedded branch only looked for
// names under `dist/`, so a file at the archive root was invisible to the very route that serves it. The
// visible symptom was a broken image on the theme page and one 404 in the console, on a page that otherwise
// rendered.
func TestTheEmbeddedThemesPreviewIsReachable(t *testing.T) {
	router := newRouter(t)

	raw, err := PublicFS.ReadFile("defaultTheme/komari-theme.json")
	if err != nil {
		t.Fatalf("read the embedded manifest: %v", err)
	}
	var manifest struct {
		Preview string `json:"preview"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("parse the embedded manifest: %v", err)
	}
	if manifest.Preview == "" {
		t.Fatal("the embedded manifest names no preview")
	}

	recorder := get(t, router, "/themes/default/"+manifest.Preview)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /themes/default/%s = %d, want 200: the theme page's preview image would be broken",
			manifest.Preview, recorder.Code)
	}
	if bytes := recorder.Body.Len(); bytes < 1024 {
		t.Errorf("the preview is %d bytes, too small to be an image", bytes)
	}
}

// regexpAssetReferences pulls `src="/..."` and `href="/..."` out of a document.
func regexpAssetReferences(document string) []string {
	out := make([]string, 0, 8)
	for _, attribute := range []string{`src="`, `href="`} {
		rest := document
		for {
			index := strings.Index(rest, attribute)
			if index < 0 {
				break
			}
			rest = rest[index+len(attribute):]
			end := strings.Index(rest, `"`)
			if end < 0 {
				break
			}
			value := rest[:end]
			if strings.HasPrefix(value, "/admin/assets/") {
				out = append(out, value)
			}
		}
	}
	return out
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
