package public

import (
	"net/http"
	"strings"
	"testing"
)

// What the server says about caching, asserted through the routes that serve the files.
//
// ## Why this exists
//
// `serveAsset` set `Cache-Control: public, max-age=31536000, immutable` for build outputs and **nothing at
// all** for everything else, and the comment claimed the others were "left as they were". Left as they were
// meant no header, which meant Cloudflare substituted its own default — four hours, as the same comment says
// elsewhere in the file. That decision produced a real outage: the theme's background images are files under
// `public/` with no content hash, they were briefly missing while the theme was replaced, and the 404 was
// cached at the edge. After the files were back the page still had no background for anyone whose browser had
// the cached failure, and only a hard refresh cleared it.
//
// A file whose name does not change when its content does must not be given a long lifetime by anything —
// neither this server nor a cache in front of it. `no-cache` (revalidate every time, with the ETag already
// set above it) is the answer, and it has to be *stated*, because an absent header is an invitation for the
// next cache in the chain to decide.

// A build output is immutable: its name carries its content hash, so a long lifetime cannot go stale.
func TestHashedAssetsAreImmutable(t *testing.T) {
	router := newRouter(t)

	// Taken from the embedded archive's Vite manifest rather than hardcoded, so this keeps testing what the
	// theme actually ships as its build outputs change. The archive holds the *contents* of the theme's dist
	// with no prefix, which is why the key has no `dist/` in it.
	files, _ := loadEmbeddedTheme(t)
	raw, ok := files[".vite/manifest.json"]
	if !ok {
		t.Fatal("the embedded archive has no .vite/manifest.json, so no asset can be known to be a build output")
	}
	assets := hashedAssetsFromManifest(raw)
	if len(assets) == 0 {
		t.Fatal("the embedded theme has no build outputs; this test would pass vacuously")
	}
	// The manifest in the archive is keyed relative to the theme's dist, with no `dist/` prefix, so the
	// request path is the asset's own path under the theme — which is how the theme serves it.
	var bundle string
	for name := range assets {
		if strings.HasSuffix(name, ".js") {
			bundle = name
			break
		}
	}
	if bundle == "" {
		t.Fatal("no JavaScript entry in the embedded manifest")
	}

	response := get(t, router, "/themes/"+DefaultTheme+"/"+bundle)
	if response.Code != http.StatusOK {
		t.Fatalf("GET %s = %d", bundle, response.Code)
	}
	got := response.Header().Get("Cache-Control")
	if !strings.Contains(got, "immutable") || !strings.Contains(got, "max-age=31536000") {
		t.Errorf("a build output got Cache-Control %q, want a long immutable lifetime", got)
	}
}

// A file whose name does not carry its content must revalidate, and must say so.
//
// The background images are the case that failed: `/assets/bg-desktop-light.v2.jpeg` — the `v2` is a manual
// version marker, not a content hash, so the file can and did change under a name clients already had.
func TestAssetsWithoutAContentHashRevalidate(t *testing.T) {
	router := newRouter(t)

	cases := []string{
		"/assets/bg-desktop-light.v2.jpeg",
		"/assets/bg-desktop-dark.v2.jpg",
		"/assets/bg-mobile-light.v2.jpg",
		"/assets/bg-mobile-dark.v2.jpg",
	}
	for _, path := range cases {
		response := get(t, router, path)
		if response.Code != http.StatusOK {
			t.Errorf("GET %s = %d, so the caching header cannot be judged", path, response.Code)
			continue
		}
		got := response.Header().Get("Cache-Control")
		if got == "" {
			t.Errorf("GET %s sent no Cache-Control at all, which lets the next cache in the chain choose — "+
				"that is what cached a 404 for four hours", path)
			continue
		}
		if strings.Contains(got, "max-age=31536000") || strings.Contains(got, "immutable") {
			t.Errorf("GET %s got Cache-Control %q: the name has no content hash, so it may not be immutable",
				path, got)
		}
		if !strings.Contains(got, "no-cache") {
			t.Errorf("GET %s got Cache-Control %q, want it to require revalidation", path, got)
		}
		// Revalidation is only cheap with a validator, and the ETag is what makes it a 304 instead of a
		// re-download.
		if response.Header().Get("ETag") == "" {
			t.Errorf("GET %s requires revalidation but sends no ETag, so every revalidation re-downloads", path)
		}
	}
}

// The shell is deliberately short-lived rather than stored, and this records which of the two it is.
//
// An earlier version of this test demanded `no-store` for `/`, which was wrong: the shell is served by its
// own handler with `max-age=60, must-revalidate` — short enough that a deployment is visible within a minute,
// and revalidated so a stale copy is never *used*. Asserting a policy the server does not have would have
// been a test of my expectation rather than of the code. What matters here is that the shell names a policy
// at all and that it is short: the failure this file exists for was an *absent* header.
func TestTheShellNamesAShortLifetime(t *testing.T) {
	router := newRouter(t)

	response := get(t, router, "/")
	if response.Code != http.StatusOK {
		t.Fatalf("GET / = %d", response.Code)
	}
	got := response.Header().Get("Cache-Control")
	if got == "" {
		t.Error("the shell sent no Cache-Control, which lets the next cache in the chain choose")
	}
	if strings.Contains(got, "immutable") || strings.Contains(got, "max-age=31536000") {
		t.Errorf("the shell got Cache-Control %q: a long lifetime there defers every deployment", got)
	}
}
