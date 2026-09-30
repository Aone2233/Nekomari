package jsonrpc

import (
	"encoding/json"
	"testing"
	"time"

	v2 "github.com/Aone2233/nekomari/protocol/v2"
)

func TestLatestStatusPreservesUnknownAndKnownZero(t *testing.T) {
	now := time.Now().UTC()
	report := &v2.Report{UpdatedAt: now, SampledAt: now, ReceivedAt: now.Add(time.Second), CounterEpoch: "epoch-a", SampleIntervalSeconds: 5,
		Quality:     map[string]string{"cpu": "ok", "ram": "unknown", "net_rate": "unknown", "connections": "ok"},
		Connections: v2.ConnectionsReport{TCP: 21, UDP: 4}}
	got := latestStatusFromReport("node", report, true, nil)
	if got.Cpu == nil || *got.Cpu != 0 || got.Ram != nil || got.RamTotal != nil || got.NetIn != nil || got.Gpu != nil || got.Temp != nil {
		t.Fatalf("unknown values became measured zeros: %+v", got)
	}
	if got.ConnectionsTCP == nil || *got.ConnectionsTCP != 21 || got.Connections == nil || *got.Connections != 25 || !got.SampledAt.Equal(now) || got.CounterEpoch != "epoch-a" {
		t.Fatalf("sample metadata or connection dimensions lost: %+v", got)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["cpu"] != float64(0) || decoded["ram"] != nil || decoded["net_in"] != nil {
		t.Fatalf("wire values: %s", encoded)
	}
}

func TestLatestStatusAcceptsLegacyQualityAndSuppressesFailedGPU(t *testing.T) {
	report := &v2.Report{GPU: &v2.GPUDetailReport{Count: 1, AverageUsage: 12}}
	if got := latestStatusFromReport("node", report, false, nil); got.Cpu == nil || got.Gpu == nil || *got.Gpu != 12 {
		t.Fatalf("legacy sample: %+v", got)
	}
	report.Quality = map[string]string{"gpu": "unknown"}
	if got := latestStatusFromReport("node", report, false, nil); got.Gpu != nil || got.GpuDetailedInfo != nil {
		t.Fatalf("failed GPU exposed: %+v", got)
	}
}

func TestLatestStatusKernelTotalsDoNotDependOnBillingCycle(t *testing.T) {
	report := &v2.Report{Quality: map[string]string{"network": "ok", "traffic_cycle": "unknown"},
		Network: v2.NetworkReport{TotalUp: 100, TotalDown: 200}}
	got := latestStatusFromReport("node", report, true, nil)
	if got.NetTotalUp == nil || *got.NetTotalUp != 100 || got.NetTotalDown == nil || *got.NetTotalDown != 200 {
		t.Fatalf("valid kernel counters hidden by disabled billing cycle: %+v", got)
	}
	report.Quality = map[string]string{"network": "unknown", "traffic_cycle": "ok"}
	got = latestStatusFromReport("node", report, true, nil)
	if got.NetTotalUp != nil || got.NetTotalDown != nil {
		t.Fatalf("failed kernel counters exposed by billing quality: %+v", got)
	}
}
