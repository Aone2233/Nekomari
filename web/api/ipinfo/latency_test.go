package ipinfo

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestIpInfoLatencyHappyPath 覆盖 /latency 的成功路径与节点映射。
func TestIpInfoLatencyHappyPath(t *testing.T) {
	handler, upstreams, _ := newTestHandler(t)
	router := newTestRouter(handler)

	recorder := doRequest(t, router, http.MethodGet, "/api/public/ip-info/v1/latency?uuid=client-l&ip=1.2.3.4", nil)
	_, data, meta := decodeSuccess(t, recorder)

	if got := asString(t, data, "uuid"); got != "client-l" {
		t.Errorf("uuid = %q, want client-l", got)
	}
	if got := asNumber(t, data, "schema_version"); got != SchemaVersion {
		t.Errorf("schema_version = %v, want %d", got, SchemaVersion)
	}
	assertAddress(t, data, "1.2.3.4", 4)
	assertClassification(t, asObject(t, data, "classification"), ClassificationNative, LabelNative)

	latency := asObject(t, data, "latency")
	requireKeys(t, latency, "nodes", "available_count", "timeout_count", "provider_cached")
	if asBool(t, latency, "provider_cached") {
		t.Error("expected provider_cached=false on a cache miss")
	}

	nodes := asArray(t, latency, "nodes")
	if len(nodes) != 3 {
		t.Fatalf("nodes length = %d, want 3", len(nodes))
	}
	for index, raw := range nodes {
		node, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("node %d is not an object", index)
		}
		requireKeys(t, node, "id", "name", "city", "country_code", "latency_ms", "status")
		asString(t, node, "id")
		asString(t, node, "name")
		asString(t, node, "city")
		asString(t, node, "country_code")
		asNumber(t, node, "latency_ms")
		status := asString(t, node, "status")
		if status != LatencyStatusOK && status != LatencyStatusTimeout && status != LatencyStatusUnavailable {
			t.Errorf("node %d status = %q, must be ok|timeout|unavailable", index, status)
		}
	}

	first := nodes[0].(map[string]any)
	if got := asString(t, first, "status"); got != LatencyStatusOK {
		t.Errorf("node 0 status = %q, want ok", got)
	}
	if got := asString(t, first, "id"); got != "AS15169" {
		t.Errorf("node 0 id = %q, want AS15169", got)
	}
	if got := asNumber(t, first, "latency_ms"); got != 12 {
		t.Errorf("node 0 latency_ms = %v, want 12 (12.34 四舍五入)", got)
	}
	if got := asString(t, first, "country_code"); got != "DE" {
		t.Errorf("node 0 country_code = %q, want DE", got)
	}

	second := nodes[1].(map[string]any)
	if got := asString(t, second, "status"); got != LatencyStatusTimeout {
		t.Errorf("node 1 status = %q, want timeout (探针跑完但没有回包)", got)
	}
	if got := asNumber(t, second, "latency_ms"); got != 0 {
		t.Errorf("node 1 latency_ms = %v, want 0", got)
	}

	third := nodes[2].(map[string]any)
	if got := asString(t, third, "status"); got != LatencyStatusUnavailable {
		t.Errorf("node 2 status = %q, want unavailable (探针没跑起来)", got)
	}

	if got := asNumber(t, latency, "available_count"); got != 1 {
		t.Errorf("available_count = %v, want 1", got)
	}
	if got := asNumber(t, latency, "timeout_count"); got != 1 {
		t.Errorf("timeout_count = %v, want 1", got)
	}

	provider := asObject(t, data, "provider")
	requireKeys(t, provider, "id", "name", "homepage", "classification_available", "latency_available")
	if !asBool(t, provider, "latency_available") {
		t.Error("expected provider.latency_available=true")
	}
	if !asBool(t, provider, "classification_available") {
		t.Error("expected provider.classification_available=true")
	}

	assertMeta(t, meta)
	if got := asString(t, meta, "cache"); got != CacheMiss {
		t.Errorf("meta.cache = %q, want miss", got)
	}

	// 第二次必须命中缓存，且不再打 Globalping。
	hitsBefore := upstreams.hitsFor("globalping")
	recorder = doRequest(t, router, http.MethodGet, "/api/public/ip-info/v1/latency?uuid=client-l&ip=1.2.3.4", nil)
	_, cachedData, cachedMeta := decodeSuccess(t, recorder)
	if got := asString(t, cachedMeta, "cache"); got != CacheHit {
		t.Errorf("meta.cache = %q, want hit", got)
	}
	if !asBool(t, asObject(t, cachedData, "latency"), "provider_cached") {
		t.Error("expected provider_cached=true on a latency cache hit")
	}
	if got := upstreams.hitsFor("globalping"); got != hitsBefore {
		t.Errorf("globalping hits = %d, want %d (缓存命中不应再测量)", got, hitsBefore)
	}
}

// TestIpInfoLatencyDegradesGracefully 校验 Globalping 挂掉时不硬失败。
func TestIpInfoLatencyDegradesGracefully(t *testing.T) {
	handler, upstreams, _ := newTestHandler(t)
	router := newTestRouter(handler)

	upstreams.setGlobalping(failWith(http.StatusInternalServerError))

	recorder := doRequest(t, router, http.MethodGet, "/api/public/ip-info/v1/latency?uuid=u&ip=1.2.3.4", nil)
	_, data, meta := decodeSuccess(t, recorder)

	latency := asObject(t, data, "latency")
	nodes := asArray(t, latency, "nodes")
	if len(nodes) != 0 {
		t.Errorf("nodes = %v, want empty", nodes)
	}
	if got := asNumber(t, latency, "available_count"); got != 0 {
		t.Errorf("available_count = %v, want 0", got)
	}
	if got := asNumber(t, latency, "timeout_count"); got != 0 {
		t.Errorf("timeout_count = %v, want 0", got)
	}
	if asBool(t, latency, "provider_cached") {
		t.Error("expected provider_cached=false when nothing was cached")
	}
	if asBool(t, asObject(t, data, "provider"), "latency_available") {
		t.Error("expected provider.latency_available=false after a Globalping failure")
	}
	// 分类数据仍然可用。
	assertClassification(t, asObject(t, data, "classification"), ClassificationNative, LabelNative)
	warning := asNullableString(t, meta, "warning")
	if warning == nil || !strings.Contains(*warning, "latency") {
		t.Errorf("meta.warning = %v, want a latency failure explanation", warning)
	}

	// 失败结果也会被短暂缓存：第二次命中缓存且提示仍在。
	recorder = doRequest(t, router, http.MethodGet, "/api/public/ip-info/v1/latency?uuid=u&ip=1.2.3.4", nil)
	_, _, cachedMeta := decodeSuccess(t, recorder)
	if got := asString(t, cachedMeta, "cache"); got != CacheHit {
		t.Errorf("meta.cache = %q, want hit (失败结果短缓存)", got)
	}
	if cachedWarning := asNullableString(t, cachedMeta, "warning"); cachedWarning == nil {
		t.Error("expected the cached failure warning to be preserved")
	}
}

// TestIpInfoLatencyGivesUpWithinBudget 校验超时预算被真正执行（不会一直等下去）。
func TestIpInfoLatencyGivesUpWithinBudget(t *testing.T) {
	handler, upstreams, _ := newTestHandlerWithConfig(t, func(cfg *Config) {
		cfg.GlobalpingPollInterval = 5 * time.Millisecond
		cfg.GlobalpingMaxWait = 60 * time.Millisecond
	})
	router := newTestRouter(handler)

	// 永远处于 in-progress：测量永远不结束。
	upstreams.setGlobalping(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"id":"measure-slow","status":"in-progress"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"measure-slow","status":"in-progress"}`))
	})

	start := time.Now()
	recorder := doRequest(t, router, http.MethodGet, "/api/public/ip-info/v1/latency?uuid=u&ip=1.2.3.4", nil)
	elapsed := time.Since(start)

	_, data, meta := decodeSuccess(t, recorder)
	if elapsed > 3*time.Second {
		t.Errorf("latency endpoint took %s, want it to give up quickly", elapsed)
	}
	if nodes := asArray(t, asObject(t, data, "latency"), "nodes"); len(nodes) != 0 {
		t.Errorf("nodes = %v, want empty", nodes)
	}
	if asBool(t, asObject(t, data, "provider"), "latency_available") {
		t.Error("expected latency_available=false when the budget is exceeded")
	}
	if warning := asNullableString(t, meta, "warning"); warning == nil {
		t.Error("expected a budget warning")
	}
}

// TestIpInfoLatencyDisabledByConfig 校验关闭开关同时影响 /status 与 /latency。
func TestIpInfoLatencyDisabledByConfig(t *testing.T) {
	handler, upstreams, _ := newTestHandlerWithConfig(t, func(cfg *Config) {
		cfg.GlobalLatencyDisabled = true
	})
	router := newTestRouter(handler)

	recorder := doRequest(t, router, http.MethodGet, "/api/public/ip-info/v1/status", nil)
	var root map[string]any
	if err := decodeInto(t, recorder, &root); err != nil {
		t.Fatal(err)
	}
	data := asObject(t, root, "data")
	if asBool(t, asObject(t, data, "capabilities"), "global_latency") {
		t.Error("expected capabilities.global_latency=false when disabled")
	}

	recorder = doRequest(t, router, http.MethodGet, "/api/public/ip-info/v1/latency?uuid=u&ip=1.2.3.4", nil)
	_, latencyData, meta := decodeSuccess(t, recorder)
	if nodes := asArray(t, asObject(t, latencyData, "latency"), "nodes"); len(nodes) != 0 {
		t.Errorf("nodes = %v, want empty when disabled", nodes)
	}
	if warning := asNullableString(t, meta, "warning"); warning == nil || !strings.Contains(*warning, "disabled") {
		t.Errorf("meta.warning = %v, want it to mention disabled", warning)
	}
	if got := upstreams.hitsFor("globalping"); got != 0 {
		t.Errorf("globalping hits = %d, want 0 when disabled", got)
	}
}

// TestIpInfoLatencyNodeMapping 单测 status 映射与 id/name 推导的边界情况。
func TestIpInfoLatencyNodeMapping(t *testing.T) {
	probes := []globalpingProbe{
		{ASN: 15169, Network: "Google LLC", City: "Frankfurt", Country: "DE", AvgMS: 0.4, HasStat: true, Status: "finished"},
		{ASN: 24940, Network: "", City: "Nuremberg", Country: "DE", Status: "finished"},
		{ASN: 0, Network: "", City: "", Country: "RO", Status: "offline"},
		{ASN: 9009, Network: "M247", City: "Bucharest", Country: "RO", Status: "failed"},
	}

	nodes := latencyNodes(probes)
	if len(nodes) != 4 {
		t.Fatalf("nodes length = %d, want 4", len(nodes))
	}

	// 小于 1ms 的延迟取整后不能变成 0（否则「ok + 0ms」看起来像没数据）。
	if nodes[0].Status != LatencyStatusOK || nodes[0].LatencyMS != 1 {
		t.Errorf("node 0 = %+v, want ok with latency_ms=1", nodes[0])
	}
	if nodes[0].ID != "AS15169" {
		t.Errorf("node 0 id = %q, want AS15169", nodes[0].ID)
	}
	// finished 但没有延迟 -> timeout；name 退到城市。
	if nodes[1].Status != LatencyStatusTimeout {
		t.Errorf("node 1 status = %q, want timeout", nodes[1].Status)
	}
	if nodes[1].Name != "Nuremberg" {
		t.Errorf("node 1 name = %q, want Nuremberg", nodes[1].Name)
	}
	// offline / failed -> unavailable；没有 ASN/网络名时 id 退到序号。
	if nodes[2].Status != LatencyStatusUnavailable {
		t.Errorf("node 2 status = %q, want unavailable", nodes[2].Status)
	}
	if nodes[2].ID != "probe-2" {
		t.Errorf("node 2 id = %q, want probe-2", nodes[2].ID)
	}
	if nodes[3].Status != LatencyStatusUnavailable {
		t.Errorf("node 3 status = %q, want unavailable", nodes[3].Status)
	}

	if got := countLatencyStatus(nodes, LatencyStatusOK); got != 1 {
		t.Errorf("available count = %d, want 1", got)
	}
	if got := countLatencyStatus(nodes, LatencyStatusTimeout); got != 1 {
		t.Errorf("timeout count = %d, want 1", got)
	}
}

// TestIpInfoLatencyTimingsFallback 校验 stats.avg 缺失时退到 min / timings。
func TestIpInfoLatencyTimingsFallback(t *testing.T) {
	payload := `{"id":"m","status":"finished","results":[
		{"probe":{"asn":1,"network":"Google LLC","city":"Frankfurt","country":"de"},
		 "result":{"status":"finished","stats":{"avg":0,"min":7.5,"max":9,"total":3,"rcv":3}}},
		{"probe":{"asn":2,"country":"RO"},"result":{"status":"finished","stats":{"avg":0,"min":0,"total":2,"rcv":2},
		 "timings":[{"ttl":55,"rtt":10},{"ttl":56,"rtt":20}]}}
	]}`

	var measurement globalpingMeasurement
	if err := json.Unmarshal([]byte(payload), &measurement); err != nil {
		t.Fatalf("unmarshal measurement: %v", err)
	}

	probes := probesFromMeasurement(&measurement)
	if len(probes) != 2 {
		t.Fatalf("probes length = %d, want 2", len(probes))
	}
	// avg=0 -> 退到 stats.min。
	if !probes[0].HasStat || probes[0].AvgMS != 7.5 {
		t.Errorf("probe 0 = %+v, want AvgMS=7.5 from stats.min", probes[0])
	}
	// 国家码统一大写。
	if probes[0].Country != "DE" {
		t.Errorf("probe 0 country = %q, want DE", probes[0].Country)
	}
	node := latencyNodes(probes)[0]
	if node.Status != LatencyStatusOK || node.LatencyMS != 8 {
		t.Errorf("node 0 = %+v, want ok with latency_ms=8", node)
	}
	if node.CountryCode != "DE" || node.Name != "Google LLC" {
		t.Errorf("node 0 = %+v, want country_code=DE and name=Google LLC", node)
	}
	// avg/min 都是 0 -> 退到 timings 的平均值。
	if !probes[1].HasStat || probes[1].AvgMS != 15 {
		t.Errorf("probe 1 = %+v, want AvgMS=15 from timings", probes[1])
	}
}
