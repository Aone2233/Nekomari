package ipinfo

import (
	"encoding/json"
	"net"
	"testing"
	"time"
)

// TestIpInfoDeriveClassification 单测原生/广播/未知三种推导结果。
func TestIpInfoDeriveClassification(t *testing.T) {
	cases := []struct {
		name       string
		geolocated string
		registered string
		wantType   string
		wantLabel  string
	}{
		{"matching-lowercase", "us", "us", ClassificationNative, LabelNative},
		{"matching-uppercase", "US", "US", ClassificationNative, LabelNative},
		{"differing", "DE", "US", ClassificationBroadcast, LabelBroadcast},
		{"registered-missing", "US", "", ClassificationUnknown, LabelUnknown},
		{"geolocated-missing", "", "US", ClassificationUnknown, LabelUnknown},
		{"both-missing", "", "", ClassificationUnknown, LabelUnknown},
		{"whitespace-tolerant", " jp ", "JP", ClassificationNative, LabelNative},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := deriveClassification(testCase.geolocated, testCase.registered)
			if got.Type != testCase.wantType || got.Label != testCase.wantLabel {
				t.Errorf("deriveClassification(%q, %q) = %s/%s, want %s/%s",
					testCase.geolocated, testCase.registered, got.Type, got.Label, testCase.wantType, testCase.wantLabel)
			}
			// 回显的国家码统一大写去空格。
			if got.GeolocatedCountryCode != upperTrim(testCase.geolocated) {
				t.Errorf("geolocated_country_code = %q", got.GeolocatedCountryCode)
			}
			if got.RegisteredCountryCode != upperTrim(testCase.registered) {
				t.Errorf("registered_country_code = %q", got.RegisteredCountryCode)
			}
		})
	}
}

func upperTrim(value string) string {
	result := ""
	for _, r := range value {
		if r == ' ' {
			continue
		}
		if r >= 'a' && r <= 'z' {
			r -= 32
		}
		result += string(r)
	}
	return result
}

// TestIpInfoReputationFormula 单测风险分公式与各字段的一致性。
func TestIpInfoReputationFormula(t *testing.T) {
	cases := []struct {
		name        string
		flags       *ipAPIFlags
		wantRisk    int
		wantPurity  int
		wantLevel   string
		wantMethod  string
		wantAvail   bool
		wantValid   int
		wantPositiv int
	}{
		{"clean", &ipAPIFlags{}, 0, 100, "", "ok", true, 6, 6},
		{"datacenter-only", &ipAPIFlags{Hosting: true}, 5, 95, "low", "ok", true, 6, 5},
		{"proxy-only", &ipAPIFlags{Proxy: true}, 20, 80, "low", "ok", true, 6, 5},
		{"proxy-and-datacenter", &ipAPIFlags{Proxy: true, Hosting: true}, 25, 75, "low", "ok", true, 6, 4},
		{"mobile-is-not-a-signal", &ipAPIFlags{Mobile: true}, 0, 100, "", "ok", true, 6, 6},
		{"unavailable", nil, 0, 100, "", "unavailable", false, 0, 0},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := buildReputation(testCase.flags, "US", []string{"ipwho.is"}, []string{})
			if got.RiskScore != testCase.wantRisk {
				t.Errorf("risk_score = %d, want %d", got.RiskScore, testCase.wantRisk)
			}
			if got.PurityScore != testCase.wantPurity {
				t.Errorf("purity_score = %d, want %d", got.PurityScore, testCase.wantPurity)
			}
			if got.PurityScore+got.RiskScore != 100 {
				t.Errorf("purity_score + risk_score = %d, want 100", got.PurityScore+got.RiskScore)
			}
			if got.RiskLevel != testCase.wantLevel {
				t.Errorf("risk_level = %q, want %q", got.RiskLevel, testCase.wantLevel)
			}
			if got.Method.Status != testCase.wantMethod {
				t.Errorf("method.status = %q, want %q", got.Method.Status, testCase.wantMethod)
			}
			if got.Available != testCase.wantAvail {
				t.Errorf("available = %v, want %v", got.Available, testCase.wantAvail)
			}
			if got.ValidSignalCount != testCase.wantValid {
				t.Errorf("valid_signal_count = %d, want %d", got.ValidSignalCount, testCase.wantValid)
			}
			if got.PositiveSignalCount != testCase.wantPositiv {
				t.Errorf("positive_signal_count = %d, want %d", got.PositiveSignalCount, testCase.wantPositiv)
			}
			if got.PollutionScore != 0 || got.PollutionLevel != "" {
				t.Errorf("pollution = %d/%q, want 0/empty (没有污染数据源)", got.PollutionScore, got.PollutionLevel)
			}
			if got.Signals.CountryCode != "US" {
				t.Errorf("signals.country_code = %q, want US", got.Signals.CountryCode)
			}
		})
	}
}

// TestIpInfoReputationMarksPartialWhenSourceFails 校验部分源失败时标记 partial。
func TestIpInfoReputationMarksPartialWhenSourceFails(t *testing.T) {
	got := buildReputation(&ipAPIFlags{}, "US", []string{"ipwho.is", "ip-api.com"}, []string{"stat.ripe.net"})
	if got.Method.Status != "partial" {
		t.Errorf("method.status = %q, want partial", got.Method.Status)
	}
	if len(got.AvailableSources) != 2 || len(got.FailedSources) != 1 {
		t.Errorf("sources = %v / failed = %v", got.AvailableSources, got.FailedSources)
	}
	if got.Method.ID != reputationMethodID {
		t.Errorf("method.id = %q, want %q", got.Method.ID, reputationMethodID)
	}
}

// TestIpInfoIsPublicIP 单测公网地址判定。
func TestIpInfoIsPublicIP(t *testing.T) {
	public := []string{
		"1.2.3.4",
		"8.8.8.8",
		"223.5.5.5",
		"2606:4700:4700::1111",
		"2001:4860:4860::8888",
		"::ffff:1.2.3.4", // IPv4-mapped IPv6 按 IPv4 处理
	}
	for _, raw := range public {
		if !isPublicIP(net.ParseIP(raw)) {
			t.Errorf("isPublicIP(%q) = false, want true", raw)
		}
	}

	nonPublic := []string{
		"", "127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.0.1", "169.254.1.1",
		"100.64.0.1", "192.0.0.1", "192.0.2.1", "198.18.0.1", "198.51.100.1",
		"203.0.113.1", "240.0.0.1", "255.255.255.255", "224.0.0.1", "0.0.0.0",
		"::1", "::", "fd00::1", "fe80::1", "ff02::1", "2001:db8::1", "100::1",
	}
	for _, raw := range nonPublic {
		if raw == "" {
			if isPublicIP(nil) {
				t.Error("isPublicIP(nil) = true, want false")
			}
			continue
		}
		if isPublicIP(net.ParseIP(raw)) {
			t.Errorf("isPublicIP(%q) = true, want false", raw)
		}
	}
}

// TestIpInfoParsePublicIP 单测输入解析与地址族判定。
func TestIpInfoParsePublicIP(t *testing.T) {
	cases := []struct {
		raw        string
		wantValue  string
		wantFamily int
		wantErr    bool
	}{
		{"1.2.3.4", "1.2.3.4", 4, false},
		{"  1.2.3.4  ", "1.2.3.4", 4, false},
		{"[2606:4700:4700::1111]", "2606:4700:4700::1111", 6, false},
		{"2606:4700:4700::1111", "2606:4700:4700::1111", 6, false},
		{"::ffff:1.2.3.4", "1.2.3.4", 4, false},
		{"", "", 0, true},
		{"not-an-ip", "", 0, true},
		{"1.2.3.4:80", "", 0, true},
		{"1.2.3.4/24", "", 0, true},
		{"127.0.0.1", "", 0, true},
	}

	for _, testCase := range cases {
		t.Run(testCase.raw, func(t *testing.T) {
			ip, family, err := parsePublicIP(testCase.raw)
			if testCase.wantErr {
				if err == nil {
					t.Fatalf("parsePublicIP(%q) succeeded, want error", testCase.raw)
				}
				if err.Error() == "" {
					t.Error("error message must not be empty")
				}
				return
			}
			if err != nil {
				t.Fatalf("parsePublicIP(%q) failed: %v", testCase.raw, err)
			}
			if ip.String() != testCase.wantValue {
				t.Errorf("value = %q, want %q", ip.String(), testCase.wantValue)
			}
			if family != testCase.wantFamily {
				t.Errorf("family = %d, want %d", family, testCase.wantFamily)
			}
		})
	}
}

// TestIpInfoCacheStaleWindow 单测缓存的「新鲜 -> 兜底 -> 丢弃」三段语义。
func TestIpInfoCacheStaleWindow(t *testing.T) {
	clock := newTestClock()
	cache := newTTLCache[string](8, clock.Now)

	cache.set("k", "v", time.Hour, 2*time.Hour)

	hit, ok := cache.get("k")
	if !ok || !hit.Fresh || hit.Value != "v" {
		t.Fatalf("fresh read = %+v, ok = %v", hit, ok)
	}
	if !hit.ExpiresAt.Equal(clock.Now().Add(time.Hour)) {
		t.Errorf("expiresAt = %s, want now+1h", hit.ExpiresAt)
	}
	if !hit.StaleUntil.Equal(clock.Now().Add(3 * time.Hour)) {
		t.Errorf("staleUntil = %s, want now+3h", hit.StaleUntil)
	}

	clock.advance(90 * time.Minute)
	hit, ok = cache.get("k")
	if !ok || hit.Fresh {
		t.Fatalf("stale read = %+v, ok = %v; want ok=true, fresh=false", hit, ok)
	}

	clock.advance(3 * time.Hour)
	if _, ok := cache.get("k"); ok {
		t.Error("expected the entry to be dropped after the stale window")
	}

	// 容量上限：写满后必须淘汰，不能无限增长。
	for i := 0; i < 20; i++ {
		cache.set(string(rune('a'+i)), "v", time.Hour, time.Hour)
	}
	if got := cache.len(); got > 8 {
		t.Errorf("cache size = %d, want <= 8", got)
	}
}

// TestIpInfoContractJSONShapes 校验 JSON 形状：可空字段为 null，集合字段为 [] / {}。
func TestIpInfoContractJSONShapes(t *testing.T) {
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	meta := metaFromCache(cacheHit[lookupSnapshot]{
		Fresh:      true,
		UpdatedAt:  now,
		ExpiresAt:  now.Add(time.Hour),
		StaleUntil: now.Add(25 * time.Hour),
	}, CacheHit, "")

	raw, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	requireKeys(t, decoded, "cache", "stale", "updated_at", "expires_at", "stale_until", "warning")
	// warning 必须是显式的 null，而不是被 omitempty 省掉。
	if value, exists := decoded["warning"]; !exists || value != nil {
		t.Errorf("meta.warning = %v (exists=%v), want explicit null", value, exists)
	}

	// 空节点集合必须是 [] 而不是 null。
	latencyRaw, err := json.Marshal(buildLatencyData("", net.ParseIP("1.2.3.4"), 4, latencySnapshot{Nodes: nil}, false))
	if err != nil {
		t.Fatal(err)
	}
	var latencyDecoded map[string]any
	if err := json.Unmarshal(latencyRaw, &latencyDecoded); err != nil {
		t.Fatal(err)
	}
	nodes := asArray(t, asObject(t, latencyDecoded, "latency"), "nodes")
	if len(nodes) != 0 {
		t.Errorf("nodes = %v, want []", nodes)
	}

	// reputation 的四个集合字段同理。
	lookupRaw, err := json.Marshal(buildLookupData("", net.ParseIP("1.2.3.4"), 4, lookupSnapshot{
		Reputation: buildReputation(nil, "", []string{}, []string{}),
	}))
	if err != nil {
		t.Fatal(err)
	}
	var lookupDecoded map[string]any
	if err := json.Unmarshal(lookupRaw, &lookupDecoded); err != nil {
		t.Fatal(err)
	}
	reputation := asObject(t, lookupDecoded, "reputation")
	asObject(t, reputation, "database_scores")
	asObject(t, reputation, "database_signals")
	asArray(t, reputation, "available_sources")
	asArray(t, reputation, "failed_sources")
	// excluded_reason 为 null。
	if reason := asNullableString(t, lookupDecoded, "excluded_reason"); reason != nil {
		t.Errorf("excluded_reason = %q, want null", *reason)
	}
	// classification 始终存在（schema 里可选，但输出更稳）。
	asObject(t, lookupDecoded, "classification")
}

// TestIpInfoDomainFromPTR 单测反向解析域名的裁剪：只取最后两段。
func TestIpInfoDomainFromPTR(t *testing.T) {
	cases := map[string]string{
		"host.example.com": "example.com",
		"dns.google.":      "dns.google",
		"a.b.example.com.": "example.com",
		"host":             "",
		"":                 "",
	}
	for input, want := range cases {
		if got := domainFromPTR(input); got != want {
			t.Errorf("domainFromPTR(%q) = %q, want %q", input, got, want)
		}
	}
}
