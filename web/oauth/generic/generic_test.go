package generic

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// genericIdentityFrom runs one callback against a user-info endpoint that returns
// body, and returns the identity the provider derived from it.
func genericIdentityFrom(t *testing.T, body string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			_, _ = w.Write([]byte(`{"access_token":"token"}`))
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	g := &Generic{Addition: Addition{TokenURL: server.URL + "/token", UserInfoURL: server.URL + "/user", UserIDField: "id"}}
	_ = g.Init()
	_, state := g.GetAuthorizationURL("http://panel/callback")
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("GET", "/callback", nil)
	result, err := g.OnCallback(ctx, state, map[string]string{"code": "code"}, "http://panel/callback")
	if err != nil {
		t.Fatalf("callback for %s failed: %v", body, err)
	}
	return result.UserId
}

// TestGenericNumericIdentityFormattingIsPinned records the representation existing
// SSO bindings depend on. A JSON number is decoded into float64 and formatted, so
// the stored id is Go's shortest float form — "1.234567e+06" for 1234567, not
// "1234567" — and any change to that would silently detach every numeric binding.
// docs/OAUTH-INTEGRATION.md holds the migration that has to happen first.
func TestGenericNumericIdentityFormattingIsPinned(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"small integer", `{"id":42}`, "42"},
		{"legacy binding", `{"id":1234567}`, "1.234567e+06"},
		{"legacy binding, larger", `{"id":1234567890123}`, "1.234567890123e+12"},
		{"float64 limit", `{"id":9007199254740992}`, "9.007199254740992e+15"},
		{"float64 limit plus one", `{"id":9007199254740993}`, "9.007199254740992e+15"},
		{"string id is verbatim", `{"id":"9007199254740993"}`, "9007199254740993"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := genericIdentityFrom(t, tc.body); got != tc.want {
				t.Fatalf("identity %q, want %q", got, tc.want)
			}
		})
	}
}

// TestGenericNumericIdentityCollidesAboveFloat64Precision is the concrete failure
// the migration document describes: two distinct provider accounts one apart
// collapse onto one SSO id, so a login for one can resolve to the other's binding.
// A provider that sends the id as a string does not collide, which is why the
// document recommends string ids in new configuration.
func TestGenericNumericIdentityCollidesAboveFloat64Precision(t *testing.T) {
	lower := genericIdentityFrom(t, `{"id":9007199254740992}`)
	upper := genericIdentityFrom(t, `{"id":9007199254740993}`)
	if lower != upper {
		t.Fatalf("numeric ids no longer collide (%q vs %q); update docs/OAUTH-INTEGRATION.md", lower, upper)
	}
	if got := genericIdentityFrom(t, `{"id":"9007199254740993"}`); got != "9007199254740993" {
		t.Fatalf("string identity %q, want the exact digits", got)
	}
}

func TestGenericCallbackValidatesIdentityAndConsumesState(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"string", `{"id":"user-1"}`, "user-1"},
		{"legacy numeric binding", `{"id":1234567}`, "1.234567e+06"},
		{"null", `{"id":null}`, ""},
		{"object", `{"id":{}}`, ""},
		{"empty", `{"id":""}`, ""},
		{"missing", `{}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path == "/token" {
					_, _ = w.Write([]byte(`{"access_token":"token"}`))
					return
				}
				if r.Header.Get("Authorization") != "Bearer token" {
					t.Error("missing bearer token")
				}
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			g := &Generic{Addition: Addition{TokenURL: server.URL + "/token", UserInfoURL: server.URL + "/user", UserIDField: "id"}}
			_ = g.Init()
			_, state := g.GetAuthorizationURL("http://panel/callback")
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest("GET", "/callback", nil)
			result, err := g.OnCallback(ctx, state, map[string]string{"code": "code"}, "http://panel/callback")
			if tc.want == "" {
				if err == nil {
					t.Fatal("invalid identity accepted")
				}
			} else if err != nil || result.UserId != tc.want {
				t.Fatalf("result=%+v error=%v", result, err)
			}
			before := calls
			if _, err := g.OnCallback(ctx, state, map[string]string{"code": "code"}, "http://panel/callback"); err == nil {
				t.Fatal("replay accepted")
			}
			if calls != before {
				t.Fatal("replay reached provider")
			}
		})
	}
}
