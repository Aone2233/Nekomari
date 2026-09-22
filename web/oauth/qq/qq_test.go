package qq

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestAuthorizationCarriesStateInCallback(t *testing.T) {
	var callback string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callback = r.URL.Query().Get("redirect_uri")
		_, _ = w.Write([]byte(`{"code":0,"url":"https://provider.example/authorize"}`))
	}))
	defer server.Close()
	q := &QQ{Addition: Addition{AggregationURL: server.URL}}
	_ = q.Init()
	auth, state := q.GetAuthorizationURL("https://panel.example/api/oauth_callback?existing=1")
	u, err := url.Parse(callback)
	if err != nil || auth == "" || state == "" || u.Query().Get("state") != state || u.Query().Get("existing") != "1" {
		t.Fatalf("invalid callback %q", callback)
	}
}
