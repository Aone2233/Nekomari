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
