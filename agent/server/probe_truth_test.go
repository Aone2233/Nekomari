package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRunProbeCycleComputesSuccessOnlyStats(t *testing.T) {
	latencies := []int64{1000, 2000, -1}
	index := 0
	measure := func() (int64, string, error) {
		value := latencies[index]
		index++
		if value < 0 {
			return -1, "ipv4", errors.New("timeout")
		}
		return value, "ipv4", nil
	}
	cycle := runProbeCycle(measure, 3, 0)
	if cycle.attempts != 3 || cycle.success != 2 {
		t.Fatalf("attempts/success = %d/%d, want 3/2", cycle.attempts, cycle.success)
	}
	if cycle.lossPct < 33.33 || cycle.lossPct > 33.34 {
		t.Fatalf("loss_pct = %v, want 33.333...", cycle.lossPct)
	}
	if cycle.firstUS != 1000 {
		t.Fatalf("firstUS = %d, want first success 1000", cycle.firstUS)
	}
	if cycle.minUS == nil || *cycle.minUS != 1000 {
		t.Fatalf("min = %v, want 1000", cycle.minUS)
	}
	if cycle.avgUS == nil || *cycle.avgUS != 1500 {
		t.Fatalf("avg = %v, want 1500", cycle.avgUS)
	}
	if cycle.maxUS == nil || *cycle.maxUS != 2000 {
		t.Fatalf("max = %v, want 2000", cycle.maxUS)
	}
	// RFC3550: |2000-1000|/16 = 62.5, rounded to 63.
	if cycle.jitterUS == nil || *cycle.jitterUS != 63 {
		t.Fatalf("jitter = %v, want 63", cycle.jitterUS)
	}
	stats := cycle.stats()
	if stats.Attempts == nil || *stats.Attempts != 3 || stats.Success == nil || *stats.Success != 2 {
		t.Fatalf("stats identity = %+v", stats)
	}
	if stats.ProbeMode != "multi" {
		t.Fatalf("probe mode = %q, want multi", stats.ProbeMode)
	}
}

func TestRunProbeCycleAllFailHasUnknownLatency(t *testing.T) {
	cycle := runProbeCycle(func() (int64, string, error) {
		return -1, "ipv6", errors.New("timeout")
	}, 3, 0)
	if cycle.success != 0 || cycle.lossPct != 100 {
		t.Fatalf("success/loss = %d/%v, want 0/100", cycle.success, cycle.lossPct)
	}
	if cycle.firstUS != -1 {
		t.Fatalf("firstUS = %d, want -1", cycle.firstUS)
	}
	if cycle.minUS != nil || cycle.avgUS != nil || cycle.maxUS != nil || cycle.jitterUS != nil {
		t.Fatalf("all-fail latency must be unknown, got %+v", cycle)
	}
}

func TestHTTPStatusAllowed(t *testing.T) {
	cases := []struct {
		code int
		spec string
		want bool
	}{
		{200, "", true},
		{399, "", true},
		{400, "", false},
		{200, "200", true},
		{204, "200,204", true},
		{301, "300-399", true},
		{404, "200-299", false},
		{503, "500-599", true},
	}
	for _, tc := range cases {
		if got := httpStatusAllowed(tc.code, tc.spec); got != tc.want {
			t.Errorf("httpStatusAllowed(%d, %q) = %v, want %v", tc.code, tc.spec, got, tc.want)
		}
	}
}

func TestHTTPProbeRecordsPhasesAndStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Millisecond)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("missing"))
	}))
	defer server.Close()

	legacy := httpProbe(server.URL, 2*time.Second, "", false)
	if legacy.err == nil {
		t.Fatal("legacy mode must treat 404 as a failed probe")
	}
	if legacy.obs == nil || legacy.obs.StatusCode == nil || *legacy.obs.StatusCode != http.StatusNotFound {
		t.Fatalf("status observation = %+v", legacy.obs)
	}
	if legacy.obs.Reachable == nil || !*legacy.obs.Reachable {
		t.Fatalf("404 must be reachable: %+v", legacy.obs.Reachable)
	}
	if legacy.obs.Available == nil || *legacy.obs.Available {
		t.Fatalf("404 must be unavailable: %+v", legacy.obs.Available)
	}
	if legacy.obs.DNSUS == nil || legacy.obs.TCPUS == nil || legacy.obs.TotalUS == nil || legacy.obs.TTFBUS == nil {
		t.Fatalf("missing phase observations: %+v", legacy.obs)
	}

	aware := httpProbe(server.URL, 2*time.Second, "200-399", true)
	if aware.err != nil {
		t.Fatalf("status_aware 404 must not be packet loss: %v", aware.err)
	}
	if aware.obs == nil || aware.obs.Available == nil || *aware.obs.Available {
		t.Fatalf("status_aware availability = %+v", aware.obs)
	}
}

func TestSelectPreferredIP(t *testing.T) {
	addrs := []string{"2001:db8::1", "192.0.2.1"}
	for _, tc := range []struct{ preference, want string }{{"4", "192.0.2.1"}, {"6", "2001:db8::1"}, {"auto", "2001:db8::1"}} {
		if got := selectPreferredIP(addrs, tc.preference); got != tc.want {
			t.Errorf("preference %q: got %q want %q", tc.preference, got, tc.want)
		}
	}
	if got := selectPreferredIP(addrs[:1], "4"); got != addrs[0] {
		t.Fatalf("fallback = %q", got)
	}
}

func TestProbeCycleLocalFailureIsUnknown(t *testing.T) {
	count := 0
	cycle := runProbeCycle(func() (int64, string, error) {
		count++
		if count == 1 {
			return 1000, "ipv4", nil
		}
		return -1, "", &probeUnavailableError{reason: "dns_failure", err: errors.New("no address")}
	}, 2, 0)
	stats := cycle.stats()
	if stats.ProbeStatus != "unknown" || stats.ProbeReason != "dns_failure" || stats.LossPct != nil || stats.Attempts != nil {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}
