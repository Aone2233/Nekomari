package server

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPProbeDNSPhaseRequiresSuccessfulResolution(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	resolve := func(host, _ string) (string, error) {
		time.Sleep(30 * time.Millisecond)
		return "127.0.0.1", nil
	}
	out := httpProbeWithResolver("http://example.test:"+port, time.Second, "", true, "", resolve)
	if out.err != nil || out.obs == nil || out.obs.DNSUS == nil || *out.obs.DNSUS < 25000 {
		t.Fatalf("successful delayed resolution not measured: %+v", out)
	}
	failed := httpProbeWithResolver("http://example.test:"+port, time.Second, "", true, "", func(string, string) (string, error) {
		return "", errors.New("resolution failed")
	})
	if failed.err == nil || failed.obs == nil || failed.obs.DNSUS != nil || failed.obs.TCPUS != nil {
		t.Fatalf("failed resolution reported a phase duration: %+v", failed)
	}
}

func TestHTTPProbeLiteralAddressHasNoDNSPhase(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	out := httpProbe(server.URL, time.Second, "", true)
	if out.err != nil || out.obs == nil || out.obs.DNSUS != nil || out.obs.TCPUS == nil {
		t.Fatalf("literal address has unexpected phases: %+v", out)
	}
}

func TestHTTPProbeDNSFailureOnRedirectDoesNotReportPartialSum(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://missing.test/", http.StatusFound)
	}))
	defer server.Close()
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	out := httpProbeWithResolver("http://first.test:"+port, time.Second, "", true, "", func(host, _ string) (string, error) {
		if host == "first.test" {
			return "127.0.0.1", nil
		}
		return "", errors.New("resolution failed")
	})
	if out.err == nil || out.obs == nil || out.obs.DNSUS != nil {
		t.Fatalf("redirect failure reported a partial DNS sum: %+v", out)
	}
}

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

func TestHTTPProbeReachableWhenResponseTimesOut(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	out := httpProbe(server.URL, 100*time.Millisecond, "", true)
	if out.err == nil || out.obs == nil || out.obs.Reachable == nil || !*out.obs.Reachable {
		t.Fatalf("expected connected response timeout: %+v", out)
	}
	if out.obs.TTFBUS != nil {
		t.Fatalf("unexpected TTFB without response: %v", *out.obs.TTFBUS)
	}
}

func TestHTTPProbeTTFBExcludesRedirectHop(t *testing.T) {
	const delay = 80 * time.Millisecond
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			time.Sleep(delay)
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	out := httpProbe(server.URL, 2*time.Second, "", true)
	if out.err != nil || out.obs == nil || out.obs.TTFBUS == nil || out.obs.TotalUS == nil {
		t.Fatalf("outcome: %+v", out)
	}
	if *out.obs.TotalUS-*out.obs.TTFBUS < int64((delay-10*time.Millisecond)/time.Microsecond) {
		t.Fatalf("redirect delay leaked into final TTFB: total=%d ttfb=%d", *out.obs.TotalUS, *out.obs.TTFBUS)
	}
}
