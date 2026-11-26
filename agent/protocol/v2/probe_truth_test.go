package v2

import (
	"encoding/json"
	"testing"
	"time"
)

func TestBuildPingResultPayloadWithStatsIsAdditive(t *testing.T) {
	attempts, success := 3, 2
	loss := 33.333333333333336
	minUS, avgUS, maxUS, jitterUS := int64(4000), int64(5000), int64(6000), int64(63)
	status, redirects := 503, 1
	reachable, available := true, false
	stats := PingProbeStats{
		Attempts: &attempts, Success: &success, LossPct: &loss,
		MinUS: &minUS, AvgUS: &avgUS, MaxUS: &maxUS, JitterUS: &jitterUS,
		HTTP:       &HTTPProbeReport{StatusCode: &status, Redirects: &redirects, Reachable: &reachable, Available: &available},
		ResolvedIP: "2001:db8::1", ResolvedFamily: "ipv6", ProbeMode: "multi",
	}
	payload := BuildPingResultPayloadWithStats(7, "icmp", "", "ipv6", 5, 5000, stats, time.Now())
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Params map[string]any `json:"params"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"attempts", "success", "loss_pct", "min_us", "avg_us", "max_us", "jitter_us", "http", "resolved_ip", "resolved_family", "probe_mode"} {
		if _, ok := wire.Params[key]; !ok {
			t.Errorf("missing additive key %q in %v", key, wire.Params)
		}
	}
	http, ok := wire.Params["http"].(map[string]any)
	if !ok || http["status_code"] != float64(503) {
		t.Fatalf("http payload = %#v", wire.Params["http"])
	}
}

func TestBuildPingResultPayloadWithoutStatsKeepsLegacyShape(t *testing.T) {
	payload := BuildPingResultPayloadWithRoleFamilyAndMicros(7, "icmp", "", "ipv4", 2, 2345, time.Now())
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Params map[string]any `json:"params"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"attempts", "success", "loss_pct", "http", "resolved_ip", "probe_mode"} {
		if _, ok := wire.Params[key]; ok {
			t.Errorf("legacy builder emitted additive key %q: %v", key, wire.Params)
		}
	}
}
