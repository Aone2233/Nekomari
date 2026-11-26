package v2

import (
	"encoding/json"
	"testing"
)

// Four-way compatibility at the wire boundary: an old payload must decode with
// no v1.8 fields, a new payload must decode with them, and an old-shaped
// decoder must keep working when the new fields are present.
func TestPingResultPayloadCompatibility(t *testing.T) {
	legacyJSON := []byte(`{"task_id":7,"ping_type":"icmp","value":2,"value_us":2345,"finished_at":"2026-11-08T12:00:00Z","family":"ipv4"}`)
	var legacy PingResultParams
	if err := json.Unmarshal(legacyJSON, &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.Attempts != nil || legacy.HTTP != nil || legacy.LossPct != nil {
		t.Fatalf("legacy payload invented v1.8 fields: %+v", legacy)
	}

	newJSON := []byte(`{
		"task_id":7,"ping_type":"http","value":5,"value_us":5000,
		"finished_at":"2026-11-08T12:00:00Z","family":"ipv6",
		"attempts":3,"success":2,"loss_pct":33.333333333333336,
		"min_us":4000,"avg_us":5000,"max_us":6000,"jitter_us":63,
		"probe_mode":"multi","resolved_ip":"2001:db8::1","resolved_family":"ipv6",
		"http":{"dns_us":1000,"tcp_us":2000,"tls_us":2500,"ttfb_us":3000,"total_us":5000,
		"status_code":503,"redirects":1,"tls_expiry_days":30.5,"reachable":true,"available":false}
	}`)
	var modern PingResultParams
	if err := json.Unmarshal(newJSON, &modern); err != nil {
		t.Fatal(err)
	}
	if modern.Attempts == nil || *modern.Attempts != 3 || modern.Success == nil || *modern.Success != 2 {
		t.Fatalf("multi-probe fields missing: %+v", modern)
	}
	if modern.HTTP == nil || modern.HTTP.StatusCode == nil || *modern.HTTP.StatusCode != 503 {
		t.Fatalf("HTTP observation missing: %+v", modern.HTTP)
	}
	if modern.HTTP.Reachable == nil || !*modern.HTTP.Reachable || modern.HTTP.Available == nil || *modern.HTTP.Available {
		t.Fatalf("HTTP reachable/available = %+v", modern.HTTP)
	}

	// An old decoder knows only these fields and must still see them.
	var oldShape struct {
		TaskID   uint   `json:"task_id"`
		PingType string `json:"ping_type"`
		Value    int    `json:"value"`
		ValueUS  int64  `json:"value_us"`
		Family   string `json:"family"`
	}
	if err := json.Unmarshal(newJSON, &oldShape); err != nil {
		t.Fatal(err)
	}
	if oldShape.TaskID != 7 || oldShape.PingType != "http" || oldShape.ValueUS != 5000 || oldShape.Family != "ipv6" {
		t.Fatalf("old decoder lost legacy fields: %+v", oldShape)
	}
}
