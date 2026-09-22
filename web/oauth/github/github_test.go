package github

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Aone2233/nekomari/web/oauth/internal/oauthutil"
	"github.com/gin-gonic/gin"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestGitHubIdentityIsExactOrRefused records the contrast with the generic
// provider's numeric path (docs/OAUTH-INTEGRATION.md): GitHub's id is decoded
// into an int, so a large id survives verbatim and an id that cannot be
// represented is refused outright instead of being rounded into a collision.
func TestGitHubIdentityIsExactOrRefused(t *testing.T) {
	previous := oauthutil.Client
	t.Cleanup(func() { oauthutil.Client = previous })
	for _, tc := range []struct {
		name, user string
		want       string
	}{
		{"above float64 precision", `{"id":9007199254740993}`, "9007199254740993"},
		{"beyond int64", `{"id":1e21}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oauthutil.Client = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				body := tc.user
				if r.URL.Host == "github.com" {
					body = `{"access_token":"token"}`
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})}
			g := &Github{Addition: Addition{ClientId: "client", ClientSecret: "secret"}}
			_ = g.Init()
			_, state := g.GetAuthorizationURL("")
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest("GET", "/callback", nil)
			result, err := g.OnCallback(ctx, state, map[string]string{"code": "code"}, "")
			if tc.want == "" {
				if err == nil {
					t.Fatalf("identity %q was accepted as %q", tc.user, result.UserId)
				}
				return
			}
			if err != nil || result.UserId != tc.want {
				t.Fatalf("result=%+v error=%v, want id %q", result, err, tc.want)
			}
		})
	}
}

func TestGitHubCallbackRejectsInvalidIdentity(t *testing.T) {
	previous := oauthutil.Client
	t.Cleanup(func() { oauthutil.Client = previous })
	for _, tc := range []struct {
		name, token, user string
		status            int
		ok                bool
	}{
		{"success", `{"access_token":"token"}`, `{"id":42}`, 200, true},
		{"empty token", `{}`, `{"id":42}`, 200, false},
		{"zero identity", `{"access_token":"token"}`, `{}`, 200, false},
		{"unauthorized", `{"access_token":"token"}`, `{"id":42}`, 401, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			oauthutil.Client = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				body := tc.user
				if r.URL.Host == "github.com" {
					body = tc.token
					if r.URL.RawQuery != "" {
						t.Error("credentials in URL")
					}
					if err := r.ParseForm(); err != nil || r.Form.Get("client_secret") != "secret" {
						t.Error("missing form credentials")
					}
				}
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})}
			g := &Github{Addition: Addition{ClientId: "client", ClientSecret: "secret"}}
			_ = g.Init()
			_, state := g.GetAuthorizationURL("")
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest("GET", "/callback", nil)
			result, err := g.OnCallback(ctx, state, map[string]string{"code": "code"}, "")
			if (err == nil) != tc.ok || (tc.ok && result.UserId != "42") {
				t.Fatalf("result=%+v error=%v", result, err)
			}
			before := calls
			if _, err := g.OnCallback(ctx, state, map[string]string{"code": "code"}, ""); err == nil || calls != before {
				t.Fatal("replay was not rejected before network")
			}
		})
	}
}
