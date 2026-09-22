package generic

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

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
