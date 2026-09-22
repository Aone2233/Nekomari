package public

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/accounts"
	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/web/oauth"
	"github.com/gin-gonic/gin"
)

// The tests in this file drive the two OAuth HTTP endpoints against fake
// upstreams. Production OAuth is disabled, so a deployment cannot prove these
// paths; an httptest provider can, offline and deterministically.

// oauthTestInstance enables OAuth for one test, points the configured provider at
// a fake upstream, and restores the global config, provider and admission bucket
// afterwards. All three are process-wide, and the tests in this package share one
// process.
func oauthTestInstance(t *testing.T, providerName, providerConfig string) {
	t.Helper()
	previousEnabled, _ := config.GetAs[bool](config.OAuthEnabledKey, false)
	previousLimiter := defaultOAuthStartLimiter
	t.Cleanup(func() {
		_ = config.Set(config.OAuthEnabledKey, previousEnabled)
		_ = oauth.Shutdown()
		defaultOAuthStartLimiter = previousLimiter
	})
	defaultOAuthStartLimiter = newLoginLimiter()
	if err := config.Set(config.OAuthEnabledKey, true); err != nil {
		t.Fatal(err)
	}
	if err := oauth.LoadProvider(providerName, providerConfig); err != nil {
		t.Fatal(err)
	}
}

// bindOAuthTestAccount makes an SSO id resolvable so a callback that passes every
// check ends in a session instead of the "bind your account first" branch.
func bindOAuthTestAccount(t *testing.T, username, ssoID string) {
	t.Helper()
	user, err := accounts.CreateAccount(username, "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if err := accounts.BindingExternalAccount(user.UUID, ssoID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = accounts.DeleteAccountByUsername(username)
		_ = accounts.DeleteAllSessions()
	})
}

func oauthRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/oauth", OAuth)
	r.GET("/api/oauth_callback", OAuthCallback)
	return r
}

// oauthRequest sends one request with an explicit client address. RemoteAddr and
// X-Forwarded-For carry the same value so the result does not depend on whether
// the router trusts proxy headers.
func oauthRequest(router *gin.Engine, path, query, ip string, cookies map[string]string) *httptest.ResponseRecorder {
	target := "http://panel.test" + path
	if query != "" {
		target += "?" + query
	}
	req := httptest.NewRequest("GET", target, nil)
	req.Host = "panel.test"
	req.RemoteAddr = ip + ":34567"
	req.Header.Set("X-Forwarded-For", ip)
	for name, value := range cookies {
		req.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// startOAuth performs a login start and returns the state the panel issued.
func startOAuth(t *testing.T, router *gin.Engine, ip string) (state string, location string) {
	t.Helper()
	w := oauthRequest(router, "/api/oauth", "", ip, nil)
	if w.Code != http.StatusFound {
		t.Fatalf("start status %d, body %s", w.Code, w.Body.String())
	}
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == "oauth_state" {
			state = cookie.Value
		}
	}
	if state == "" {
		t.Fatal("start issued no oauth_state cookie")
	}
	return state, w.Header().Get("Location")
}

// fakeGenericUpstream is the smallest offline provider: a token endpoint and a
// user-info endpoint. It counts identity lookups so a test can prove a rejected
// callback never reached the provider.
type fakeGenericUpstream struct {
	server  *httptest.Server
	userID  string
	lookups int
}

func newFakeGenericUpstream(t *testing.T, userID string) *fakeGenericUpstream {
	t.Helper()
	f := &fakeGenericUpstream{userID: userID}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			_, _ = w.Write([]byte(`{"access_token":"token"}`))
		case "/user":
			f.lookups++
			_, _ = w.Write([]byte(`{"id":"` + f.userID + `"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeGenericUpstream) config(t *testing.T) string {
	t.Helper()
	raw, err := json.Marshal(map[string]string{
		"client_id":     "client",
		"client_secret": "secret",
		"auth_url":      f.server.URL + "/authorize",
		"token_url":     f.server.URL + "/token",
		"user_info_url": f.server.URL + "/user",
		"user_id_field": "id",
		"scope":         "openid",
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestOAuthCallbackRejectsDisabledAndMismatchedState is the pre-existing
// regression test: a disabled instance must reject before contacting a provider,
// and the state in the query must equal the state in the cookie exactly.
func TestOAuthCallbackRejectsDisabledAndMismatchedState(t *testing.T) {
	previous, _ := config.GetAs[bool](config.OAuthEnabledKey, false)
	t.Cleanup(func() { _ = config.Set(config.OAuthEnabledKey, previous); _ = oauth.Shutdown() })
	if err := oauth.LoadProvider("qq", "{}"); err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	r.GET("/callback", OAuthCallback)
	for _, tc := range []struct {
		name    string
		enabled bool
		query   string
		want    int
	}{
		{"disabled", false, "cookie-state", http.StatusForbidden},
		{"mismatch", true, "attacker-state", http.StatusBadRequest},
		{"missing", true, "", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := config.Set(config.OAuthEnabledKey, tc.enabled); err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest("GET", "/callback?state="+tc.query, nil)
			req.AddCookie(&http.Cookie{Name: "oauth_state", Value: "cookie-state"})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("status %d, body %s", w.Code, w.Body.String())
			}
		})
	}
}

// TestOAuthCallbackRequiresExactStateForEveryProvider re-checks the exact-state
// rule per provider, because a provider that accepted a superset (prefix, suffix,
// case-folded) would reintroduce the CSRF hole the v0.1.17 fix closed.
func TestOAuthCallbackRequiresExactStateForEveryProvider(t *testing.T) {
	genericUpstream := newFakeGenericUpstream(t, "exact-state-user")
	aggregator := newFakeQQAggregator(t, "exact-state-uid")
	for _, tc := range []struct{ name, config string }{
		{"generic", genericUpstream.config(t)},
		{"qq", aggregator.config(t)},
		{"github", "{}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oauthTestInstance(t, tc.name, tc.config)
			router := oauthRouter()
			state, _ := startOAuth(t, router, "203.0.113.5")

			for _, mangled := range []string{state + "x", state[:len(state)-1], "x" + state, "", "ATTACKER"} {
				w := oauthRequest(router, "/api/oauth_callback", "state="+url.QueryEscape(mangled)+"&code=code",
					"203.0.113.5", map[string]string{"oauth_state": state})
				if w.Code != http.StatusBadRequest {
					t.Fatalf("state %q accepted with status %d, body %s", mangled, w.Code, w.Body.String())
				}
			}
			if genericUpstream.lookups != 0 || aggregator.callbackCalls != 0 {
				t.Fatal("a mismatched state reached a provider")
			}
		})
	}
}

// TestOAuthStartAdmissionRejectsOneIPButNotAnother covers the admission policy at
// the endpoint: one caller that spends its own budget is refused with the login
// limiter's convention (429 + Retry-After), while a different source is still
// admitted — the global cap is shared, the budget is not.
func TestOAuthStartAdmissionRejectsOneIPButNotAnother(t *testing.T) {
	upstream := newFakeGenericUpstream(t, "admission-user")
	oauthTestInstance(t, "generic", upstream.config(t))
	router := oauthRouter()

	flooder, bystander := "203.0.113.7", "198.51.100.4"
	for i := 0; i < int(oauthStartIPBurst); i++ {
		startOAuth(t, router, flooder)
	}

	w := oauthRequest(router, "/api/oauth", "", flooder, nil)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status %d, want 429; body %s", w.Code, w.Body.String())
	}
	retryAfter, err := strconv.Atoi(w.Header().Get("Retry-After"))
	if err != nil || retryAfter < 1 || retryAfter > int(oauthStartIPRefillSeconds) {
		t.Fatalf("Retry-After = %q (%v), want 1..%v seconds", w.Header().Get("Retry-After"), err, oauthStartIPRefillSeconds)
	}

	// The bystander must still get a usable start: that is the whole point of a
	// per-IP budget in front of a global cap.
	startOAuth(t, router, bystander)

	// The endpoint's budget is the OAuth bucket, not the login bucket: a flood of
	// provider logins must not create password-login state.
	if _, ok := defaultLoginLimiter.ips[flooder]; ok {
		t.Fatal("OAuth starts created a login-limiter bucket")
	}
}

// TestOAuthStartAdmissionExpiresIdleEntries: the admission map is swept on the
// login limiter's schedule, so a long-running panel does not keep an entry for
// every source it has ever seen.
func TestOAuthStartAdmissionExpiresIdleEntries(t *testing.T) {
	limiter := newLoginLimiter()
	now := time.Now()
	limiter.admitIP("203.0.113.7", now, oauthStartIPBurst, oauthStartIPRefillSeconds)

	later := now.Add(loginLimiterEntryTTL + loginLimiterCleanupInterval)
	limiter.admitIP("192.0.2.1", later, oauthStartIPBurst, oauthStartIPRefillSeconds)

	if _, ok := limiter.ips["203.0.113.7"]; ok {
		t.Fatal("an idle admission bucket should have been swept")
	}
}

// TestOAuthStartAdmissionIsABucketNotALock pins expiry: the refused caller
// recovers on its own once a token refills, with no operator action.
func TestOAuthStartAdmissionIsABucketNotALock(t *testing.T) {
	limiter := newLoginLimiter()
	now := time.Now()
	ip := "203.0.113.7"

	for i := 0; i < int(oauthStartIPBurst); i++ {
		if allowed, _ := limiter.admitIP(ip, now, oauthStartIPBurst, oauthStartIPRefillSeconds); !allowed {
			t.Fatalf("start %d should be admitted", i+1)
		}
	}
	allowed, retry := limiter.admitIP(ip, now, oauthStartIPBurst, oauthStartIPRefillSeconds)
	if allowed {
		t.Fatal("a start past the burst should be refused")
	}
	if retry <= 0 || retry > time.Duration(oauthStartIPRefillSeconds)*time.Second {
		t.Fatalf("retry-after = %v, want within one refill interval", retry)
	}
	if allowed, _ := limiter.admitIP(ip, now.Add(time.Duration(oauthStartIPRefillSeconds)*time.Second), oauthStartIPBurst, oauthStartIPRefillSeconds); !allowed {
		t.Fatal("the bucket should have refilled one token")
	}
}

// TestOAuthStartAdmissionFairBetweenTwoIPs pins the unit-level property the
// endpoint test relies on: one source's spent budget does not move another's.
func TestOAuthStartAdmissionFairBetweenTwoIPs(t *testing.T) {
	limiter := newLoginLimiter()
	now := time.Now()
	drained, bystander := "203.0.113.7", "198.51.100.4"

	for i := 0; i < int(oauthStartIPBurst); i++ {
		limiter.admitIP(drained, now, oauthStartIPBurst, oauthStartIPRefillSeconds)
	}
	if allowed, _ := limiter.admitIP(drained, now, oauthStartIPBurst, oauthStartIPRefillSeconds); allowed {
		t.Fatal("the drained IP was admitted past its burst")
	}
	for i := 0; i < int(oauthStartIPBurst); i++ {
		if allowed, retry := limiter.admitIP(bystander, now, oauthStartIPBurst, oauthStartIPRefillSeconds); !allowed {
			t.Fatalf("bystander start %d refused (retry-after %v) because of another IP's budget", i+1, retry)
		}
	}
}

// TestOAuthStartAdmissionStaysBounded drives a flood of distinct sources. The
// map must stay at its bound and, crucially, a new source must still be admitted:
// a full map that failed closed would be the H1 lockout again, one dimension over.
func TestOAuthStartAdmissionStaysBounded(t *testing.T) {
	limiter := newLoginLimiter()
	now := time.Now()

	for i := 0; i < loginLimiterMaxEntries+100; i++ {
		limiter.admitIP(testIP(i), now, oauthStartIPBurst, oauthStartIPRefillSeconds)
	}
	if len(limiter.ips) > loginLimiterMaxEntries {
		t.Fatalf("admission map grew to %d entries, want <= %d", len(limiter.ips), loginLimiterMaxEntries)
	}
	if allowed, retry := limiter.admitIP("192.0.2.200", now, oauthStartIPBurst, oauthStartIPRefillSeconds); !allowed {
		t.Fatalf("a fresh source was locked out by a full admission map (retry-after %v)", retry)
	}
}

// TestOAuthStartAdmissionDoesNotGrowOnRefusal: a refused caller already owns a
// bucket, so repeated refusals must not add entries — the bounded map is filled
// by admissions, never by denials. (This is the property Allow had to be fixed
// for in the login limiter.)
func TestOAuthStartAdmissionDoesNotGrowOnRefusal(t *testing.T) {
	limiter := newLoginLimiter()
	now := time.Now()
	ip := "203.0.113.7"

	for i := 0; i < int(oauthStartIPBurst); i++ {
		limiter.admitIP(ip, now, oauthStartIPBurst, oauthStartIPRefillSeconds)
	}
	size := len(limiter.ips)
	for i := 0; i < 1000; i++ {
		if allowed, _ := limiter.admitIP(ip, now, oauthStartIPBurst, oauthStartIPRefillSeconds); allowed {
			t.Fatal("refused start was admitted")
		}
	}
	if len(limiter.ips) != size {
		t.Fatalf("refusals grew the map from %d to %d entries", size, len(limiter.ips))
	}
}

// TestOAuthStartStillHonoursGlobalStateCap proves the per-IP bucket did not
// replace the shared store's ceiling: a source that never repeats is admitted
// every time until the store itself is full, and that refusal is the store's 503,
// not the limiter's 429.
func TestOAuthStartStillHonoursGlobalStateCap(t *testing.T) {
	upstream := newFakeGenericUpstream(t, "capacity-user")
	oauthTestInstance(t, "generic", upstream.config(t))
	router := oauthRouter()

	full := false
	for i := 0; i < 5000 && !full; i++ {
		w := oauthRequest(router, "/api/oauth", "", testIP(i), nil)
		switch w.Code {
		case http.StatusFound:
		case http.StatusServiceUnavailable:
			full = true
		case http.StatusTooManyRequests:
			t.Fatalf("source %d was throttled before the store was full", i)
		default:
			t.Fatalf("source %d got status %d, body %s", i, w.Code, w.Body.String())
		}
	}
	if !full {
		t.Fatal("the global pending-state cap never engaged")
	}
}

// TestOAuthQQCallbackQueryPreservation drives the QQ aggregator flow end to end.
//
// The aggregator hands the browser back to the callback URL the panel supplied,
// and the state the panel compares against its cookie travels in that query. An
// aggregator that preserves the query logs the user in; one that drops or mangles
// it must fail closed before the aggregator's callback endpoint is reached.
func TestOAuthQQCallbackQueryPreservation(t *testing.T) {
	aggregator := newFakeQQAggregator(t, "qq-uid-1")
	bindOAuthTestAccount(t, "oauth-qq-user", "qq_qq-uid-1")
	oauthTestInstance(t, "qq", aggregator.config(t))
	router := oauthRouter()

	state, _ := startOAuth(t, router, "203.0.113.7")
	if aggregator.loginCalls != 1 {
		t.Fatalf("aggregator login calls = %d, want 1", aggregator.loginCalls)
	}
	returned, err := url.Parse(aggregator.redirectURI)
	if err != nil {
		t.Fatalf("aggregator callback %q: %v", aggregator.redirectURI, err)
	}
	if returned.Query().Get("state") != state {
		t.Fatalf("aggregator callback state %q, panel issued %q", returned.Query().Get("state"), state)
	}

	// Preserved query: the aggregator appends its own code and type and the login
	// completes with a session.
	preserved := returned.RawQuery + "&code=qq-code&type=qq"
	w := oauthRequest(router, "/api/oauth_callback", preserved, "203.0.113.7", map[string]string{"oauth_state": state})
	if w.Code != http.StatusFound {
		t.Fatalf("preserved-query callback status %d, body %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Location") != "/admin/dashboard" {
		t.Fatalf("location %q", w.Header().Get("Location"))
	}
	if !hasCookie(w, "session_token") {
		t.Fatal("preserved-query callback created no session")
	}
	if aggregator.callbackCalls != 1 {
		t.Fatalf("aggregator callback calls = %d, want 1", aggregator.callbackCalls)
	}

	// Dropped query: the browser arrives without the state the panel supplied.
	droppedState, _ := startOAuth(t, router, "203.0.113.7")
	w = oauthRequest(router, "/api/oauth_callback", "code=qq-code&type=qq", "203.0.113.7",
		map[string]string{"oauth_state": droppedState})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("dropped-query callback status %d, want 400; body %s", w.Code, w.Body.String())
	}
	if aggregator.callbackCalls != 1 {
		t.Fatal("dropped-query callback reached the aggregator")
	}

	// Mangled query: the state is present but not the one the panel issued.
	mangledState, _ := startOAuth(t, router, "203.0.113.7")
	w = oauthRequest(router, "/api/oauth_callback", "state="+url.QueryEscape(mangledState+"-mangled")+"&code=qq-code&type=qq",
		"203.0.113.7", map[string]string{"oauth_state": mangledState})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("mangled-query callback status %d, want 400; body %s", w.Code, w.Body.String())
	}
	if aggregator.callbackCalls != 1 {
		t.Fatal("mangled-query callback reached the aggregator")
	}
}

// TestOAuthProviderReloadInvalidatesPendingLogin covers an administrator editing
// the provider (or switching to another one) while a browser is away at the
// provider. The pending state belongs to the replaced instance, so the callback
// must fail rather than be resolved against a provider that never issued it.
func TestOAuthProviderReloadInvalidatesPendingLogin(t *testing.T) {
	t.Run("same provider, new configuration", func(t *testing.T) {
		first := newFakeGenericUpstream(t, "before-reload")
		oauthTestInstance(t, "generic", first.config(t))
		router := oauthRouter()
		state, _ := startOAuth(t, router, "203.0.113.7")

		second := newFakeGenericUpstream(t, "after-reload")
		if err := oauth.LoadProvider("generic", second.config(t)); err != nil {
			t.Fatal(err)
		}

		w := oauthRequest(router, "/api/oauth_callback", "state="+url.QueryEscape(state)+"&code=code",
			"203.0.113.7", map[string]string{"oauth_state": state})
		if w.Code < 400 {
			t.Fatalf("a state from the replaced provider was accepted with status %d, body %s", w.Code, w.Body.String())
		}
		if first.lookups != 0 || second.lookups != 0 {
			t.Fatal("the pending login reached a provider")
		}
	})

	t.Run("different provider", func(t *testing.T) {
		upstream := newFakeGenericUpstream(t, "generic-user")
		oauthTestInstance(t, "generic", upstream.config(t))
		router := oauthRouter()
		state, _ := startOAuth(t, router, "203.0.113.7")

		if err := oauth.LoadProvider("github", "{}"); err != nil {
			t.Fatal(err)
		}

		w := oauthRequest(router, "/api/oauth_callback", "state="+url.QueryEscape(state)+"&code=code",
			"203.0.113.7", map[string]string{"oauth_state": state})
		if w.Code < 400 {
			t.Fatalf("a state from another provider was accepted with status %d, body %s", w.Code, w.Body.String())
		}
		if upstream.lookups != 0 {
			t.Fatal("the pending login reached the replaced provider")
		}
	})
}

// TestOAuthCallbackReplayIsRejected is the back-button case: the browser
// resubmits a callback that already completed. The state was consumed by the
// first attempt, so the replay must be refused before it reaches the provider
// again — a second session for one authorization is the failure this prevents.
func TestOAuthCallbackReplayIsRejected(t *testing.T) {
	upstream := newFakeGenericUpstream(t, "replay-user")
	bindOAuthTestAccount(t, "oauth-replay-user", "generic_replay-user")
	oauthTestInstance(t, "generic", upstream.config(t))
	router := oauthRouter()

	state, _ := startOAuth(t, router, "203.0.113.7")
	cookies := map[string]string{"oauth_state": state}
	query := "state=" + url.QueryEscape(state) + "&code=code"

	first := oauthRequest(router, "/api/oauth_callback", query, "203.0.113.7", cookies)
	if first.Code != http.StatusFound {
		t.Fatalf("first callback status %d, body %s", first.Code, first.Body.String())
	}
	if !hasCookie(first, "session_token") {
		t.Fatal("first callback created no session")
	}

	// The back button re-sends the same request, cookie included.
	replay := oauthRequest(router, "/api/oauth_callback", query, "203.0.113.7", cookies)
	if replay.Code < 400 {
		t.Fatalf("replayed callback accepted with status %d, body %s", replay.Code, replay.Body.String())
	}
	if upstream.lookups != 1 {
		t.Fatalf("identity lookups = %d, want 1: the replay reached the provider", upstream.lookups)
	}
}

// fakeQQAggregator stands in for the QQ aggregation service: it records the
// callback URL the panel registered and answers the two calls the provider makes.
type fakeQQAggregator struct {
	server        *httptest.Server
	redirectURI   string
	socialUID     string
	loginCalls    int
	callbackCalls int
}

func newFakeQQAggregator(t *testing.T, socialUID string) *fakeQQAggregator {
	t.Helper()
	f := &fakeQQAggregator{socialUID: socialUID}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("act") {
		case "login":
			f.loginCalls++
			f.redirectURI = r.URL.Query().Get("redirect_uri")
			_, _ = w.Write([]byte(`{"code":0,"url":"https://qq.example/authorize"}`))
		case "callback":
			f.callbackCalls++
			_, _ = w.Write([]byte(`{"code":0,"social_uid":"` + f.socialUID + `"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeQQAggregator) config(t *testing.T) string {
	t.Helper()
	raw, err := json.Marshal(map[string]string{
		"aggregation_url": f.server.URL,
		"app_id":          "app",
		"app_key":         "key",
		"login_type":      "qq",
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func hasCookie(w *httptest.ResponseRecorder, name string) bool {
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == name && cookie.Value != "" {
			return true
		}
	}
	return false
}

// testIP spreads indexes over distinct addresses so a test can drive many sources
// without repeating one.
func testIP(index int) string {
	return fmt.Sprintf("10.%d.%d.%d", (index>>16)&0xff, (index>>8)&0xff, index&0xff)
}
