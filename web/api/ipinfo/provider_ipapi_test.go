package ipinfo

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestIpInfoIPAPICooldownOn429 覆盖 HTTP 429 + X-Ttl 触发的冷却短路。
func TestIpInfoIPAPICooldownOn429(t *testing.T) {
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("X-Ttl", "60")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"status":"fail","message":"throttled"}`))
	}))
	defer server.Close()

	clock := newTestClock()
	provider := newIPAPIProvider(server.URL, server.Client(), clock.Now)
	ip := net.ParseIP("1.2.3.4")

	if _, err := provider.lookup(context.Background(), ip); err == nil {
		t.Fatal("expected an error on HTTP 429")
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("upstream hits = %d, want 1", got)
	}
	until := provider.cooldownUntilTime()
	if !until.Equal(clock.Now().Add(60 * time.Second)) {
		t.Errorf("cooldownUntil = %s, want now+60s", until)
	}

	// 冷却期内：短路，不打上游，错误是可识别的限流错误。
	_, err := provider.lookup(context.Background(), ip)
	if !errors.Is(err, errIPAPIRateLimited) {
		t.Fatalf("expected errIPAPIRateLimited during cooldown, got %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("upstream hits = %d, want 1 (冷却期内必须短路)", got)
	}

	// 冷却结束后恢复查询。
	clock.advance(61 * time.Second)
	if _, err := provider.lookup(context.Background(), ip); err == nil {
		t.Fatal("expected an error on HTTP 429 after cooldown expiry")
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Errorf("upstream hits = %d, want 2 after cooldown expiry", got)
	}
}

// TestIpInfoIPAPICooldownOnZeroRemaining 覆盖正常响应里 X-Rl: 0 的提前冷却。
func TestIpInfoIPAPICooldownOnZeroRemaining(t *testing.T) {
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Rl", "0")
		w.Header().Set("X-Ttl", "30")
		_, _ = w.Write([]byte(`{"status":"success","query":"1.2.3.4","hosting":true,"mobile":false,"proxy":false}`))
	}))
	defer server.Close()

	clock := newTestClock()
	provider := newIPAPIProvider(server.URL, server.Client(), clock.Now)
	ip := net.ParseIP("1.2.3.4")

	// 第一次仍然返回数据（这次响应本身是有效的），但同时进入冷却。
	flags, err := provider.lookup(context.Background(), ip)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !flags.Hosting {
		t.Error("expected hosting=true to be parsed")
	}
	if until := provider.cooldownUntilTime(); !until.Equal(clock.Now().Add(30 * time.Second)) {
		t.Errorf("cooldownUntil = %s, want now+30s", until)
	}

	if _, err := provider.lookup(context.Background(), ip); !errors.Is(err, errIPAPIRateLimited) {
		t.Fatalf("expected errIPAPIRateLimited, got %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("upstream hits = %d, want 1", got)
	}
}

// TestIpInfoIPAPINoCooldownWhenQuotaRemains 覆盖「有余额就不该冷却」。
func TestIpInfoIPAPINoCooldownWhenQuotaRemains(t *testing.T) {
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Rl", "12")
		w.Header().Set("X-Ttl", "60")
		_, _ = w.Write([]byte(`{"status":"success","query":"1.2.3.4","hosting":false,"mobile":true,"proxy":true}`))
	}))
	defer server.Close()

	clock := newTestClock()
	provider := newIPAPIProvider(server.URL, server.Client(), clock.Now)
	ip := net.ParseIP("1.2.3.4")

	for i := 0; i < 3; i++ {
		if _, err := provider.lookup(context.Background(), ip); err != nil {
			t.Fatalf("call %d failed: %v", i, err)
		}
	}
	if got := atomic.LoadInt32(&hits); got != 3 {
		t.Errorf("upstream hits = %d, want 3 (有余额时不应短路)", got)
	}
	if until := provider.cooldownUntilTime(); !until.IsZero() {
		t.Errorf("cooldownUntil = %s, want zero", until)
	}
}

// TestIpInfoIPAPIInvalidCooldownHeader 校验荒唐的 X-Ttl 被钳到上限。
func TestIpInfoIPAPIInvalidCooldownHeader(t *testing.T) {
	provider := newIPAPIProvider("http://127.0.0.1:1", &http.Client{Timeout: time.Second}, newTestClock().Now)

	// 非法值 -> 落到默认 60s。
	provider.startCooldown(0, "test")
	if remaining, _, limited := provider.cooldown(); !limited || remaining > ipAPIDefaultCooldown {
		t.Errorf("remaining = %s, limited = %v; want <= %s", remaining, limited, ipAPIDefaultCooldown)
	}

	// 超大值 -> 钳到上限，不能把进程永久锁死。
	provider.startCooldown(1<<30, "test")
	if remaining, _, limited := provider.cooldown(); !limited || remaining > ipAPIMaxCooldown {
		t.Errorf("remaining = %s, want <= %s", remaining, ipAPIMaxCooldown)
	}
}

// TestIpInfoLookupIPAPIRateLimitDegrades 校验限流时 lookup 仍然 200 且降级。
func TestIpInfoLookupIPAPIRateLimitDegrades(t *testing.T) {
	handler, upstreams, _ := newTestHandler(t)
	router := newTestRouter(handler)

	upstreams.setIPAPI(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Ttl", "60")
		w.WriteHeader(http.StatusTooManyRequests)
	})

	recorder := doRequest(t, router, http.MethodGet, "/api/public/ip-info/v1/lookup?uuid=u&ip=1.2.3.4", nil)
	_, data, meta := decodeSuccess(t, recorder)

	reputation := asObject(t, data, "reputation")
	if asBool(t, reputation, "available") {
		t.Error("expected reputation.available=false when ip-api.com is rate limited")
	}
	if got := asNumber(t, reputation, "purity_score"); got != 100 {
		t.Errorf("purity_score = %v, want the neutral 100", got)
	}
	if got := asNumber(t, reputation, "risk_score"); got != 0 {
		t.Errorf("risk_score = %v, want 0", got)
	}
	if got := asString(t, asObject(t, reputation, "method"), "status"); got != "unavailable" {
		t.Errorf("method.status = %q, want unavailable", got)
	}
	if !containsString(asArray(t, reputation, "failed_sources"), sourceIPAPI) {
		t.Errorf("failed_sources = %v, want %q", asArray(t, reputation, "failed_sources"), sourceIPAPI)
	}
	if warning := asNullableString(t, meta, "warning"); warning == nil || !strings.Contains(*warning, sourceIPAPI) {
		t.Errorf("meta.warning = %v, want it to mention %q", warning, sourceIPAPI)
	}
	// 地理与网络数据不受影响。
	if got := asString(t, asObject(t, data, "location"), "country_code"); got != "US" {
		t.Errorf("location.country_code = %q, want US", got)
	}

	// 换一个 IP：冷却期内 ip-api.com 仍然不该被调用。
	hitsBefore := upstreams.hitsFor("ipapi")
	recorder = doRequest(t, router, http.MethodGet, "/api/public/ip-info/v1/lookup?uuid=u&ip=8.8.8.8", nil)
	decodeSuccess(t, recorder)
	if got := upstreams.hitsFor("ipapi"); got != hitsBefore {
		t.Errorf("ip-api.com hits = %d, want %d (冷却期内必须短路)", got, hitsBefore)
	}
}
