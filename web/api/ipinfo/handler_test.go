package ipinfo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestIpInfoStatusContract 校验 /status 的字段名、类型与能力位。
func TestIpInfoStatusContract(t *testing.T) {
	handler, _, _ := newTestHandler(t)
	router := newTestRouter(handler)

	recorder := doRequest(t, router, http.MethodGet, "/api/public/ip-info/v1/status", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status endpoint returned %d: %s", recorder.Code, recorder.Body.String())
	}

	var root map[string]any
	if err := decodeInto(t, recorder, &root); err != nil {
		t.Fatal(err)
	}
	if ok, _ := root["ok"].(bool); !ok {
		t.Fatalf("expected ok=true, got %v", root["ok"])
	}
	data := asObject(t, root, "data")

	requireKeys(t, data, "available", "version", "schema_version", "mainland_china_excluded", "capabilities")
	if !asBool(t, data, "available") {
		t.Error("expected available=true")
	}
	if got := asString(t, data, "version"); got != APIVersion {
		t.Errorf("version = %q, want %q", got, APIVersion)
	}
	if got := asNumber(t, data, "schema_version"); got != SchemaVersion {
		t.Errorf("schema_version = %v, want %d", got, SchemaVersion)
	}
	if asBool(t, data, "mainland_china_excluded") {
		t.Error("expected mainland_china_excluded=false")
	}

	capabilities := asObject(t, data, "capabilities")
	// 契约里 geo/network/reputation 是必需布尔，native_classification 与
	// global_latency 可选（这里同样输出）。
	requireKeys(t, capabilities, "geo", "network", "reputation", "native_classification",
		"global_latency", "media_unlock", "ai_unlock")
	for _, key := range []string{"geo", "network", "reputation", "native_classification"} {
		if !asBool(t, capabilities, key) {
			t.Errorf("expected capabilities.%s=true", key)
		}
	}
	if !asBool(t, capabilities, "global_latency") {
		t.Error("expected capabilities.global_latency=true (Globalping 默认启用)")
	}
	if asBool(t, capabilities, "media_unlock") || asBool(t, capabilities, "ai_unlock") {
		t.Error("expected media_unlock/ai_unlock=false (没有对应数据源)")
	}
}

// TestIpInfoLookupHappyPath 覆盖 lookup 的完整成功路径与全部契约字段。
func TestIpInfoLookupHappyPath(t *testing.T) {
	handler, upstreams, _ := newTestHandler(t)
	router := newTestRouter(handler)

	recorder := doRequest(t, router, http.MethodGet, "/api/public/ip-info/v1/lookup?uuid=client-a&ip=1.2.3.4", nil)
	_, data, meta := decodeSuccess(t, recorder)

	if got := asString(t, data, "uuid"); got != "client-a" {
		t.Errorf("uuid = %q, want %q", got, "client-a")
	}
	if got := asNumber(t, data, "schema_version"); got != SchemaVersion {
		t.Errorf("schema_version = %v, want %d", got, SchemaVersion)
	}
	if asBool(t, data, "excluded") {
		t.Error("expected excluded=false")
	}
	if reason := asNullableString(t, data, "excluded_reason"); reason != nil {
		t.Errorf("excluded_reason = %q, want null", *reason)
	}
	assertAddress(t, data, "1.2.3.4", 4)

	location := asObject(t, data, "location")
	requireKeys(t, location, "continent", "continent_code", "country", "country_code",
		"registered_country", "registered_country_code", "region", "region_code", "city",
		"postal_code", "timezone", "latitude", "longitude", "accuracy_radius")
	if got := asString(t, location, "country_code"); got != "US" {
		t.Errorf("location.country_code = %q, want US", got)
	}
	if got := asString(t, location, "country"); got != "United States" {
		t.Errorf("location.country = %q, want United States", got)
	}
	if got := asString(t, location, "city"); got != "Los Angeles" {
		t.Errorf("location.city = %q, want Los Angeles", got)
	}
	if got := asString(t, location, "timezone"); got != "America/Los_Angeles" {
		t.Errorf("location.timezone = %q", got)
	}
	if got := asString(t, location, "registered_country_code"); got != "US" {
		t.Errorf("location.registered_country_code = %q, want US", got)
	}
	asNumber(t, location, "latitude")
	asNumber(t, location, "longitude")
	asNumber(t, location, "accuracy_radius")

	network := asObject(t, data, "network")
	requireKeys(t, network, "asn", "asn_number", "organization", "operator", "network_type",
		"company_type", "route", "rir", "domain", "datacenter")
	if got := asString(t, network, "asn"); got != "AS15169" {
		t.Errorf("network.asn = %q, want AS15169", got)
	}
	if got := asNumber(t, network, "asn_number"); got != 15169 {
		t.Errorf("network.asn_number = %v, want 15169", got)
	}
	if got := asString(t, network, "route"); got != "1.2.3.0/24" {
		t.Errorf("network.route = %q, want 1.2.3.0/24", got)
	}
	if got := asString(t, network, "organization"); got != "Google LLC" {
		t.Errorf("network.organization = %q", got)
	}
	if got := asString(t, network, "domain"); got != "google.com" {
		t.Errorf("network.domain = %q", got)
	}
	// RIR 来自 RIPEstat whois 的 source 字段。
	if got := asString(t, network, "rir"); got != "ARIN" {
		t.Errorf("network.rir = %q, want ARIN", got)
	}
	if got := asString(t, network, "datacenter"); got != "" {
		t.Errorf("network.datacenter = %q, want empty when hosting=false", got)
	}

	assertClassification(t, asObject(t, data, "classification"), ClassificationNative, LabelNative)

	reputation := asObject(t, data, "reputation")
	requireKeys(t, reputation, "available", "purity_score", "risk_score", "pollution_score",
		"risk_level", "pollution_level", "positive_signal_count", "valid_signal_count",
		"signals", "database_scores", "database_signals", "available_sources", "failed_sources", "method")
	if !asBool(t, reputation, "available") {
		t.Error("expected reputation.available=true")
	}
	if got := asNumber(t, reputation, "purity_score"); got != 100 {
		t.Errorf("purity_score = %v, want 100", got)
	}
	if got := asNumber(t, reputation, "risk_score"); got != 0 {
		t.Errorf("risk_score = %v, want 0", got)
	}
	if got := asNumber(t, reputation, "valid_signal_count"); got != 6 {
		t.Errorf("valid_signal_count = %v, want 6", got)
	}
	if got := asNumber(t, reputation, "positive_signal_count"); got != 6 {
		t.Errorf("positive_signal_count = %v, want 6", got)
	}
	signals := asObject(t, reputation, "signals")
	requireKeys(t, signals, "country_code", "proxy", "tor", "vpn", "datacenter", "abuser", "crawler")
	for _, key := range []string{"proxy", "tor", "vpn", "datacenter", "abuser", "crawler"} {
		if asBool(t, signals, key) {
			t.Errorf("signals.%s = true, want false", key)
		}
	}
	// database_scores / database_signals 必须是对象而不是 null。
	asObject(t, reputation, "database_scores")
	asObject(t, reputation, "database_signals")

	sources := asArray(t, reputation, "available_sources")
	if len(sources) != 3 {
		t.Errorf("available_sources = %v, want 3 sources", sources)
	}
	if failed := asArray(t, reputation, "failed_sources"); len(failed) != 0 {
		t.Errorf("failed_sources = %v, want empty", failed)
	}
	method := asObject(t, reputation, "method")
	requireKeys(t, method, "id", "status")
	if got := asString(t, method, "status"); got != "ok" {
		t.Errorf("method.status = %q, want ok", got)
	}

	capabilities := asObject(t, data, "capabilities")
	requireKeys(t, capabilities, "media_unlock", "ai_unlock")
	if asBool(t, capabilities, "media_unlock") || asBool(t, capabilities, "ai_unlock") {
		t.Error("expected media_unlock/ai_unlock=false")
	}

	provider := asObject(t, data, "provider")
	requireKeys(t, provider, "id", "name", "homepage", "base_source", "quality_sources", "security_data_available")
	if got := asString(t, provider, "base_source"); got != sourceIPWhoIs {
		t.Errorf("provider.base_source = %q, want %q", got, sourceIPWhoIs)
	}
	if !asBool(t, provider, "security_data_available") {
		t.Error("expected provider.security_data_available=true")
	}
	asArray(t, provider, "quality_sources")

	assertMeta(t, meta)
	if got := asString(t, meta, "cache"); got != CacheMiss {
		t.Errorf("meta.cache = %q, want miss", got)
	}
	if asBool(t, meta, "stale") {
		t.Error("expected meta.stale=false")
	}
	if warning := asNullableString(t, meta, "warning"); warning != nil {
		t.Errorf("meta.warning = %q, want null", *warning)
	}

	// 三个上游各被调用一次，whois 再补一次。
	if got := upstreams.hitsFor("geo"); got != 1 {
		t.Errorf("ipwho.is hits = %d, want 1", got)
	}
	if got := upstreams.hitsFor("ipapi"); got != 1 {
		t.Errorf("ip-api.com hits = %d, want 1", got)
	}
	if got := upstreams.hitsFor("ripe"); got != 2 {
		t.Errorf("ripestat hits = %d, want 2 (network-info + whois)", got)
	}
}

// TestIpInfoLookupCacheHit 校验第二次查询命中缓存且不再打上游。
func TestIpInfoLookupCacheHit(t *testing.T) {
	handler, upstreams, _ := newTestHandler(t)
	router := newTestRouter(handler)

	first := doRequest(t, router, http.MethodGet, "/api/public/ip-info/v1/lookup?uuid=u&ip=1.2.3.4", nil)
	_, _, firstMeta := decodeSuccess(t, first)
	if got := asString(t, firstMeta, "cache"); got != CacheMiss {
		t.Fatalf("first call meta.cache = %q, want miss", got)
	}

	second := doRequest(t, router, http.MethodGet, "/api/public/ip-info/v1/lookup?uuid=u&ip=1.2.3.4", nil)
	_, _, secondMeta := decodeSuccess(t, second)
	if got := asString(t, secondMeta, "cache"); got != CacheHit {
		t.Errorf("second call meta.cache = %q, want hit", got)
	}
	if asBool(t, secondMeta, "stale") {
		t.Error("expected meta.stale=false for a fresh cache hit")
	}
	if got := upstreams.hitsFor("geo"); got != 1 {
		t.Errorf("ipwho.is hits = %d, want 1 (缓存命中不应再打上游)", got)
	}
	if got := upstreams.hitsFor("ipapi"); got != 1 {
		t.Errorf("ip-api.com hits = %d, want 1", got)
	}
}

// TestIpInfoLookupServesStaleAfterUpstreamFailure 校验「过期但保留」的兜底语义。
func TestIpInfoLookupServesStaleAfterUpstreamFailure(t *testing.T) {
	handler, upstreams, clock := newTestHandler(t)
	router := newTestRouter(handler)

	doRequest(t, router, http.MethodGet, "/api/public/ip-info/v1/lookup?uuid=u&ip=1.2.3.4", nil)

	// 越过新鲜期，但仍在 24h 兜底期内；此时上游全挂。
	clock.advance(time.Hour + time.Minute)
	upstreams.setGeo(failWith(http.StatusInternalServerError))
	upstreams.setRipe(failWith(http.StatusInternalServerError))
	upstreams.setIPAPI(failWith(http.StatusInternalServerError))

	recorder := doRequest(t, router, http.MethodGet, "/api/public/ip-info/v1/lookup?uuid=u&ip=1.2.3.4", nil)
	_, data, meta := decodeSuccess(t, recorder)

	if got := asString(t, meta, "cache"); got != CacheHit {
		t.Errorf("meta.cache = %q, want hit (数据来自缓存)", got)
	}
	if !asBool(t, meta, "stale") {
		t.Error("expected meta.stale=true when serving an expired entry")
	}
	warning := asNullableString(t, meta, "warning")
	if warning == nil || !strings.Contains(*warning, "stale") {
		t.Errorf("meta.warning = %v, want a stale-serving explanation", warning)
	}
	// 兜底返回的仍然是上一次的完整数据。
	if got := asString(t, asObject(t, data, "network"), "asn"); got != "AS15169" {
		t.Errorf("stale payload network.asn = %q, want AS15169", got)
	}

	// 越过兜底期后彻底丢弃：上游仍然全挂，这次必须报错。
	clock.advance(25 * time.Hour)
	recorder = doRequest(t, router, http.MethodGet, "/api/public/ip-info/v1/lookup?uuid=u&ip=1.2.3.4", nil)
	decodeError(t, recorder)
}

// TestIpInfoLookupNegativeCacheShortCircuits 校验失败结果会被短暂缓存。
func TestIpInfoLookupNegativeCacheShortCircuits(t *testing.T) {
	handler, upstreams, clock := newTestHandler(t)
	router := newTestRouter(handler)

	upstreams.setGeo(failWith(http.StatusInternalServerError))
	upstreams.setRipe(failWith(http.StatusInternalServerError))
	upstreams.setIPAPI(failWith(http.StatusInternalServerError))

	first := doRequest(t, router, http.MethodGet, "/api/public/ip-info/v1/lookup?uuid=u&ip=1.2.3.4", nil)
	if first.Code != http.StatusBadGateway {
		t.Fatalf("all-sources-failed lookup returned %d, want 502", first.Code)
	}
	decodeError(t, first)
	geoHits := upstreams.hitsFor("geo")

	// 负缓存期内：直接短路，不再打上游。
	second := doRequest(t, router, http.MethodGet, "/api/public/ip-info/v1/lookup?uuid=u&ip=1.2.3.4", nil)
	decodeError(t, second)
	if got := upstreams.hitsFor("geo"); got != geoHits {
		t.Errorf("ipwho.is hits = %d, want %d (负缓存期内不应再打上游)", got, geoHits)
	}

	// 负缓存过期后重新尝试上游。
	clock.advance(6 * time.Minute)
	upstreams.setGeo(writeJSON(geoBodyFor("United States", "US")))
	upstreams.setRipe(writeJSON(ripeWhoisBody("US", "ARIN")))
	upstreams.setIPAPI(writeJSON(`{"status":"success","query":"1.2.3.4","hosting":false,"mobile":false,"proxy":false}`))
	third := doRequest(t, router, http.MethodGet, "/api/public/ip-info/v1/lookup?uuid=u&ip=1.2.3.4", nil)
	decodeSuccess(t, third)
	if got := upstreams.hitsFor("geo"); got <= geoHits {
		t.Errorf("ipwho.is hits = %d, want > %d after negative TTL expiry", got, geoHits)
	}
}

// TestIpInfoLookupDegradesWhenOneSourceFails 校验单个上游挂掉时的降级。
func TestIpInfoLookupDegradesWhenOneSourceFails(t *testing.T) {
	handler, upstreams, _ := newTestHandler(t)
	router := newTestRouter(handler)

	upstreams.setGeo(failWith(http.StatusInternalServerError))

	recorder := doRequest(t, router, http.MethodGet, "/api/public/ip-info/v1/lookup?uuid=u&ip=1.2.3.4", nil)
	_, data, meta := decodeSuccess(t, recorder)

	location := asObject(t, data, "location")
	if got := asString(t, location, "country_code"); got != "" {
		t.Errorf("location.country_code = %q, want empty when ipwho.is is down", got)
	}
	// 注册国家仍来自 RIPEstat，但地理国家缺失 -> unknown。
	assertClassification(t, asObject(t, data, "classification"), ClassificationUnknown, LabelUnknown)

	reputation := asObject(t, data, "reputation")
	sources := asArray(t, reputation, "available_sources")
	failed := asArray(t, reputation, "failed_sources")
	if containsString(sources, sourceIPWhoIs) {
		t.Errorf("available_sources = %v, must not contain the failed source", sources)
	}
	if !containsString(failed, sourceIPWhoIs) {
		t.Errorf("failed_sources = %v, must contain %q", failed, sourceIPWhoIs)
	}
	if got := asString(t, asObject(t, reputation, "method"), "status"); got != "partial" {
		t.Errorf("method.status = %q, want partial when a source failed", got)
	}
	// ASN 仍可由 RIPEstat 的 network-info 补齐。
	if got := asString(t, asObject(t, data, "network"), "asn"); got != "AS15169" {
		t.Errorf("network.asn = %q, want AS15169 from RIPEstat fallback", got)
	}
	warning := asNullableString(t, meta, "warning")
	if warning == nil || !strings.Contains(*warning, sourceIPWhoIs) {
		t.Errorf("meta.warning = %v, want it to mention %q", warning, sourceIPWhoIs)
	}
}

// TestIpInfoLookupRejectsInvalidAndNonPublicIP 校验输入校验：非法值与内网地址都必须 400。
func TestIpInfoLookupRejectsInvalidAndNonPublicIP(t *testing.T) {
	handler, upstreams, _ := newTestHandler(t)
	router := newTestRouter(handler)

	cases := []struct {
		name string
		ip   string
	}{
		{"empty", ""},
		{"not-an-ip", "not-an-ip"},
		{"hostname", "example.com"},
		{"with-port", "1.2.3.4:8080"},
		{"ipv4-loopback", "127.0.0.1"},
		{"ipv4-private-10", "10.0.0.1"},
		{"ipv4-private-192", "192.168.1.1"},
		{"ipv4-private-172", "172.16.0.1"},
		{"ipv4-link-local", "169.254.1.1"},
		{"ipv4-cgnat", "100.64.0.1"},
		{"ipv4-benchmark", "198.18.0.1"},
		{"ipv4-documentation", "203.0.113.10"},
		{"ipv4-unspecified", "0.0.0.0"},
		{"ipv4-multicast", "224.0.0.1"},
		{"ipv6-loopback", "::1"},
		{"ipv6-ula", "fd00::1"},
		{"ipv6-link-local", "fe80::1"},
		{"ipv6-documentation", "2001:db8::1"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			target := "/api/public/ip-info/v1/lookup?uuid=u&ip=" + testCase.ip
			recorder := doRequest(t, router, http.MethodGet, target, nil)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("ip=%q returned %d, want 400 (%s)", testCase.ip, recorder.Code, recorder.Body.String())
			}
			if message := decodeError(t, recorder); message == "" {
				t.Error("expected a clear error message")
			}
		})
	}

	if got := upstreams.hitsFor("geo"); got != 0 {
		t.Errorf("ipwho.is hits = %d, want 0 (非法输入不应打上游)", got)
	}
	if got := upstreams.hitsFor("ipapi"); got != 0 {
		t.Errorf("ip-api.com hits = %d, want 0", got)
	}
}

// TestIpInfoLookupIPv6 校验 IPv6 输入与 family=6 的输出。
func TestIpInfoLookupIPv6(t *testing.T) {
	handler, upstreams, _ := newTestHandler(t)
	router := newTestRouter(handler)

	const target = "2606:4700:4700::1111"
	var geoPath string
	upstreams.setGeo(func(w http.ResponseWriter, r *http.Request) {
		geoPath = r.URL.Path
		writeJSON(geoBodyFor("United States", "US"))(w, r)
	})

	recorder := doRequest(t, router, http.MethodGet, "/api/public/ip-info/v1/lookup?uuid=u&ip="+target, nil)
	_, data, _ := decodeSuccess(t, recorder)

	assertAddress(t, data, target, 6)
	if !strings.HasSuffix(geoPath, target) {
		t.Errorf("upstream geo path = %q, want it to end with %q", geoPath, target)
	}
}

// TestIpInfoLookupClassificationMatrix 覆盖 classification 推导的三种结果。
func TestIpInfoLookupClassificationMatrix(t *testing.T) {
	cases := []struct {
		name           string
		geoCountryCode string
		registeredCode string
		wantType       string
		wantLabel      string
	}{
		{"matching", "US", "US", ClassificationNative, LabelNative},
		{"differing", "DE", "US", ClassificationBroadcast, LabelBroadcast},
		{"registered-missing", "US", "", ClassificationUnknown, LabelUnknown},
		{"geolocated-missing", "", "US", ClassificationUnknown, LabelUnknown},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			handler, upstreams, _ := newTestHandler(t)
			router := newTestRouter(handler)

			if testCase.geoCountryCode != "US" {
				upstreams.setGeo(writeJSON(geoBodyFor("Germany", testCase.geoCountryCode)))
			}
			if testCase.registeredCode == "" {
				// whois 不返回国家码（RIPE 区域的 ASN 就是这样）。
				upstreams.setRipe(func(w http.ResponseWriter, r *http.Request) {
					if strings.Contains(r.URL.Path, "whois") {
						writeJSON(ripeWhoisBody("", "RIPE"))(w, r)
						return
					}
					writeJSON(`{"data":{"prefix":"1.2.3.0/24","asns":["15169"]}}`)(w, r)
				})
			} else if testCase.registeredCode != "US" {
				upstreams.setRipe(func(w http.ResponseWriter, r *http.Request) {
					if strings.Contains(r.URL.Path, "whois") {
						writeJSON(ripeWhoisBody(testCase.registeredCode, "ARIN"))(w, r)
						return
					}
					writeJSON(`{"data":{"prefix":"1.2.3.0/24","asns":["15169"]}}`)(w, r)
				})
			}

			recorder := doRequest(t, router, http.MethodGet, "/api/public/ip-info/v1/lookup?uuid=u&ip=1.2.3.4", nil)
			_, data, _ := decodeSuccess(t, recorder)

			classification := asObject(t, data, "classification")
			assertClassification(t, classification, testCase.wantType, testCase.wantLabel)
			if got := asString(t, classification, "geolocated_country_code"); got != testCase.geoCountryCode {
				t.Errorf("geolocated_country_code = %q, want %q", got, testCase.geoCountryCode)
			}
			if got := asString(t, classification, "registered_country_code"); got != testCase.registeredCode {
				t.Errorf("registered_country_code = %q, want %q", got, testCase.registeredCode)
			}
		})
	}
}

// TestIpInfoRefreshHandler 覆盖刷新接口的请求体解析、强制刷新与失败语义。
func TestIpInfoRefreshHandler(t *testing.T) {
	handler, upstreams, _ := newTestHandler(t)
	router := newTestRouter(handler)

	// 先建立缓存。
	doRequest(t, router, http.MethodGet, "/api/public/ip-info/v1/lookup?uuid=u&ip=1.2.3.4", nil)
	hitsBefore := upstreams.hitsFor("geo")

	// force=true 必须绕过缓存。
	recorder := doRequest(t, router, http.MethodPost, "/api/admin/ip-info/v1/refresh",
		jsonBody(map[string]any{"uuid": "client-b", "ip": "1.2.3.4", "force": true, "include_latency": false}))
	_, data, meta := decodeSuccess(t, recorder)

	if got := asString(t, data, "uuid"); got != "client-b" {
		t.Errorf("uuid = %q, want client-b", got)
	}
	if got := asString(t, meta, "cache"); got != CacheMiss {
		t.Errorf("meta.cache = %q, want miss with force=true", got)
	}
	if got := upstreams.hitsFor("geo"); got <= hitsBefore {
		t.Errorf("ipwho.is hits = %d, want > %d (force 应绕过缓存)", got, hitsBefore)
	}

	// 缺省 force 视为 true（刷新接口的语义就是刷新）。
	hitsBefore = upstreams.hitsFor("geo")
	recorder = doRequest(t, router, http.MethodPost, "/api/admin/ip-info/v1/refresh",
		jsonBody(map[string]any{"uuid": "client-b", "ip": "1.2.3.4"}))
	decodeSuccess(t, recorder)
	if got := upstreams.hitsFor("geo"); got <= hitsBefore {
		t.Errorf("ipwho.is hits = %d, want > %d (缺省 force=true)", got, hitsBefore)
	}

	// force=false 时允许命中缓存。
	hitsBefore = upstreams.hitsFor("geo")
	recorder = doRequest(t, router, http.MethodPost, "/api/admin/ip-info/v1/refresh",
		jsonBody(map[string]any{"uuid": "client-b", "ip": "1.2.3.4", "force": false}))
	_, _, cachedMeta := decodeSuccess(t, recorder)
	if got := asString(t, cachedMeta, "cache"); got != CacheHit {
		t.Errorf("meta.cache = %q, want hit with force=false", got)
	}
	if got := upstreams.hitsFor("geo"); got != hitsBefore {
		t.Errorf("ipwho.is hits = %d, want %d (force=false 应命中缓存)", got, hitsBefore)
	}

	// include_latency=true 时顺带刷新延迟缓存。
	recorder = doRequest(t, router, http.MethodPost, "/api/admin/ip-info/v1/refresh",
		jsonBody(map[string]any{"uuid": "client-b", "ip": "1.2.3.4", "force": true, "include_latency": true}))
	decodeSuccess(t, recorder)
	if got := upstreams.hitsFor("globalping"); got == 0 {
		t.Error("expected Globalping to be queried when include_latency=true")
	}

	// 非法请求体与非法 IP 都必须是 400 + error.message。
	recorder = doRequest(t, router, http.MethodPost, "/api/admin/ip-info/v1/refresh", strings.NewReader("{not json"))
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("malformed body returned %d, want 400", recorder.Code)
	}
	decodeError(t, recorder)

	recorder = doRequest(t, router, http.MethodPost, "/api/admin/ip-info/v1/refresh",
		jsonBody(map[string]any{"uuid": "client-b", "ip": "192.168.0.1", "force": true}))
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("private ip returned %d, want 400", recorder.Code)
	}
	decodeError(t, recorder)

	// 上游全挂时刷新必须是非 2xx + error.message。
	upstreams.setGeo(failWith(http.StatusInternalServerError))
	upstreams.setRipe(failWith(http.StatusInternalServerError))
	upstreams.setIPAPI(failWith(http.StatusInternalServerError))
	recorder = doRequest(t, router, http.MethodPost, "/api/admin/ip-info/v1/refresh",
		jsonBody(map[string]any{"uuid": "client-b", "ip": "9.9.9.9", "force": true}))
	if recorder.Code < 400 {
		t.Errorf("all-sources-failed refresh returned %d, want non-2xx", recorder.Code)
	}
	decodeError(t, recorder)
}

// TestIpInfoEnvelopeShape 校验响应外壳本身：成功带 data/meta，失败只有 error。
func TestIpInfoEnvelopeShape(t *testing.T) {
	handler, _, _ := newTestHandler(t)
	router := newTestRouter(handler)

	success := doRequest(t, router, http.MethodGet, "/api/public/ip-info/v1/lookup?uuid=&ip=8.8.8.8", nil)
	var root map[string]any
	if err := decodeInto(t, success, &root); err != nil {
		t.Fatal(err)
	}
	if _, exists := root["error"]; exists {
		t.Error("success envelope must not contain error")
	}
	if got := asString(t, root["data"].(map[string]any), "uuid"); got != "" {
		t.Errorf("uuid = %q, want empty string when the query parameter is absent", got)
	}

	failure := doRequest(t, router, http.MethodGet, "/api/public/ip-info/v1/lookup?ip=bad", nil)
	var failureRoot map[string]any
	if err := decodeInto(t, failure, &failureRoot); err != nil {
		t.Fatal(err)
	}
	if _, exists := failureRoot["data"]; exists {
		t.Error("error envelope must not contain data")
	}
	if _, exists := failureRoot["ok"]; exists {
		t.Error("error envelope must not contain ok")
	}
}

func decodeInto(t *testing.T, recorder *httptest.ResponseRecorder, out any) error {
	t.Helper()
	return json.Unmarshal(recorder.Body.Bytes(), out)
}
