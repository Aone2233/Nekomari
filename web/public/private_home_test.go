package public

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Aone2233/nekomari/internal/config"
)

func TestPrivateEmeraldHomeNavigation(t *testing.T) {
	router := newRouter(t)
	for _, test := range []struct {
		name, theme, method, path, cookie, mode, dest string
		authorized, private, redirect, noStore        bool
	}{
		{name: "anonymous navigation", theme: "emerald-insights", private: true, mode: "navigate", dest: "document", redirect: true, noStore: true},
		{name: "language preference", theme: "emerald-insights", private: true, mode: "navigate", dest: "document", cookie: "language=en", redirect: true, noStore: true},
		{name: "session", theme: "emerald-insights", private: true, mode: "navigate", dest: "document", cookie: (&http.Cookie{Name: "session_token", Value: t.Name()}).String(), noStore: true},
		{name: "share cookie", theme: "emerald-insights", private: true, mode: "navigate", dest: "document", cookie: "temp_key=present", noStore: true},
		{name: "share link", theme: "emerald-insights", private: true, mode: "navigate", dest: "document", path: "/?temp_key=present", noStore: true},
		{name: "theme query", theme: "emerald-insights", private: true, mode: "navigate", dest: "document", path: "/?view=compact", noStore: true},
		{name: "authorization", theme: "emerald-insights", private: true, mode: "navigate", dest: "document", authorized: true, noStore: true},
		{name: "shell probe", theme: "emerald-insights", private: true, noStore: true},
		{name: "HEAD probe", theme: "emerald-insights", private: true, method: "HEAD", mode: "navigate", dest: "document", noStore: true},
		{name: "iframe", theme: "emerald-insights", private: true, mode: "navigate", dest: "iframe", noStore: true},
		{name: "fetch", theme: "emerald-insights", private: true, mode: "cors", dest: "empty", noStore: true},
		{name: "public site", theme: "emerald-insights", mode: "navigate", dest: "document"},
		{name: "different private theme", theme: "default", private: true, mode: "navigate", dest: "document"},
		{name: "deep link", theme: "emerald-insights", private: true, mode: "navigate", dest: "document", path: "/instance/example"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := config.Set(config.ThemeKey, test.theme); err != nil {
				t.Fatal(err)
			}
			if err := config.Set(config.PrivateSiteKey, test.private); err != nil {
				t.Fatal(err)
			}
			path := test.path
			if path == "" {
				path = "/"
			}
			request := httptest.NewRequest(test.method, path, nil)
			request.Header.Set("Sec-Fetch-Mode", test.mode)
			request.Header.Set("Sec-Fetch-Dest", test.dest)
			request.Header.Set("Cookie", test.cookie)
			if test.authorized {
				request.Header.Set("Authorization", "present")
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if test.redirect {
				if response.Code != http.StatusFound || response.Header().Get("Location") != "/admin" || strings.Contains(response.Body.String(), "<script") {
					t.Fatalf("unexpected redirect: %d %v", response.Code, response.Header())
				}
			} else if response.Code != http.StatusOK || !strings.HasPrefix(response.Header().Get("Content-Type"), "text/html") {
				t.Fatalf("expected shell, got %d %v", response.Code, response.Header())
			}
			cache := shellCacheControl
			if test.noStore {
				cache = "private, no-store"
			}
			if got := response.Header().Get("Cache-Control"); got != cache {
				t.Fatalf("cache policy %q, want %q", got, cache)
			}
		})
	}
}
