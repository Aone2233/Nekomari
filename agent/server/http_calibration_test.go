package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// HTTP TTFB calibration: a server that waits before writing the response
// should be observed within the v1.8 phase tolerance. This catches a phase
// that is present but measured from the wrong start point.
func TestHTTPProbeTTFBCalibration(t *testing.T) {
	const delay = 40 * time.Millisecond
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(delay)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	out := httpProbe(server.URL, 2*time.Second, "", true)
	if out.err != nil {
		t.Fatalf("http probe: %v", out.err)
	}
	if out.obs == nil || out.obs.TTFBUS == nil || out.obs.TotalUS == nil {
		t.Fatalf("missing phase observations: %+v", out.obs)
	}
	ttfb := float64(*out.obs.TTFBUS) / 1000
	if ttfb < delay.Seconds()*1000-5 {
		t.Fatalf("ttfb = %.1f ms, want at least %.1f ms", ttfb, delay.Seconds()*1000-5)
	}
	if ttfb > 200 {
		t.Fatalf("ttfb = %.1f ms, implausibly far from the injected 40 ms", ttfb)
	}
	if *out.obs.TotalUS < *out.obs.TTFBUS {
		t.Fatalf("total %d µs < ttfb %d µs", *out.obs.TotalUS, *out.obs.TTFBUS)
	}
}
