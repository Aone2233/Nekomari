package client

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/protocol/v2"
)

func TestPingResultParamsDecodeProbeTruthFields(t *testing.T) {
	raw := []byte(`{
		"task_id":7,"ping_type":"icmp","value":2,"value_us":2345,
		"finished_at":"2026-11-08T12:00:00Z","role":"","family":"ipv6",
		"attempts":3,"success":2,"loss_pct":33.333333333333336,
		"min_us":2000,"avg_us":2345,"max_us":2690,"jitter_us":63,
		"probe_mode":"multi","resolved_ip":"2001:db8::1","resolved_family":"ipv6"
	}`)
	var params v2.PingResultParams
	if err := json.Unmarshal(raw, &params); err != nil {
		t.Fatal(err)
	}
	if params.Attempts == nil || *params.Attempts != 3 || params.Success == nil || *params.Success != 2 {
		t.Fatalf("attempts/success = %+v/%+v", params.Attempts, params.Success)
	}
	if params.LossPct == nil || *params.LossPct < 33.33 || *params.LossPct > 33.34 {
		t.Fatalf("loss_pct = %+v", params.LossPct)
	}
	if params.MinUS == nil || *params.MinUS != 2000 || params.AvgUS == nil || *params.AvgUS != 2345 {
		t.Fatalf("min/avg = %+v/%+v", params.MinUS, params.AvgUS)
	}
	if params.ResolvedIP != "2001:db8::1" || params.ResolvedFamily != "ipv6" || params.ProbeMode != "multi" {
		t.Fatalf("resolved/probe mode = %+v", params)
	}
}

func TestPingStatsFromParamsKeepsLegacyNil(t *testing.T) {
	legacy := v2.PingResultParams{TaskID: 7, PingType: "icmp", Value: 2}
	if stats := pingStatsFromParams(legacy); stats != nil {
		t.Fatalf("legacy params produced stats: %+v", stats)
	}

	attempts := 1
	stats := pingStatsFromParams(v2.PingResultParams{TaskID: 7, PingType: "icmp", Attempts: &attempts})
	if stats == nil || stats.Attempts == nil || *stats.Attempts != 1 {
		t.Fatalf("single-probe stats = %+v", stats)
	}
}

func TestNewPingRecordWithStatsMapsHTTPObservation(t *testing.T) {
	attempts, success := 3, 2
	loss := 33.333333333333336
	minUS, avgUS, maxUS, jitterUS := int64(2000), int64(2345), int64(2690), int64(63)
	dnsUS, tcpUS, totalUS := int64(100), int64(200), int64(500)
	status, redirects := 404, 1
	reachable, available := true, false
	at := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

	stats := &v2.PingProbeStats{
		Attempts: &attempts, Success: &success, LossPct: &loss,
		MinUS: &minUS, AvgUS: &avgUS, MaxUS: &maxUS, JitterUS: &jitterUS,
		HTTP: &v2.HTTPProbeReport{
			DNSUS: &dnsUS, TCPUS: &tcpUS, TotalUS: &totalUS,
			StatusCode: &status, Redirects: &redirects,
			Reachable: &reachable, Available: &available,
		},
		ResolvedIP: "192.0.2.1", ResolvedFamily: "ipv4", ProbeMode: "multi",
	}
	rec := newPingRecordWithStats("node-a", 7, "http", "", "ipv4", 1, 500, stats, at)
	if rec.Attempts != 3 || rec.Success != 2 || rec.LossPct < 33.33 || rec.LossPct > 33.34 {
		t.Fatalf("multi-probe summary = %+v", rec)
	}
	if rec.MinUS != 2000 || rec.AvgUS != 2345 || rec.MaxUS != 2690 || rec.JitterUS != 63 {
		t.Fatalf("latency summary = %+v", rec)
	}
	if rec.HTTP == nil || rec.HTTP.StatusCode == nil || *rec.HTTP.StatusCode != 404 ||
		rec.HTTP.Reachable == nil || !*rec.HTTP.Reachable ||
		rec.HTTP.Available == nil || *rec.HTTP.Available {
		t.Fatalf("http observation = %+v", rec.HTTP)
	}
}
