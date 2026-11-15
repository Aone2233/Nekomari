package server

import (
	"fmt"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"
)

// The final hop and the redirect hop carry different delays so a cumulative
// TTFB cannot accidentally match the final-hop reference.
func TestHTTPRedirectFieldCalibration(t *testing.T) {
	if os.Getenv("NEKOMARI_HTTP_REDIRECT_FIELD") != "1" {
		t.Skip("field calibration disabled")
	}
	for run := 1; run <= 3; run++ {
		var port string
		first := make(chan time.Duration, 1)
		final := make(chan time.Duration, 1)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			if r.URL.Path == "/" {
				time.Sleep(80 * time.Millisecond)
				first <- time.Since(start)
				http.Redirect(w, r, "http://second.fixture:"+port+"/final", http.StatusFound)
				return
			}
			time.Sleep(40 * time.Millisecond)
			final <- time.Since(start)
			w.WriteHeader(http.StatusOK)
		}))
		_, port, _ = net.SplitHostPort(srv.Listener.Addr().String())
		var mu sync.Mutex
		var dnsTotal time.Duration
		var dnsCount int
		resolver := func(_, _ string) (string, error) {
			start := time.Now()
			time.Sleep(25 * time.Millisecond)
			mu.Lock()
			dnsTotal += time.Since(start)
			dnsCount++
			mu.Unlock()
			return "127.0.0.1", nil
		}
		out := httpProbeWithResolver("http://first.fixture:"+port, 3*time.Second, "", true, "", resolver)
		srv.Close()
		mu.Lock()
		dnsRef, count := dnsTotal, dnsCount
		mu.Unlock()
		if out.err != nil || out.obs == nil || out.obs.DNSUS == nil || out.obs.TTFBUS == nil || out.obs.TotalUS == nil || out.obs.Redirects == nil || *out.obs.Redirects != 1 || count != 2 {
			t.Fatalf("run=%d outcome=%+v dns_count=%d", run, out, count)
		}
		firstRef, finalRef := <-first, <-final
		for _, p := range []struct {
			name   string
			actual int64
			ref    time.Duration
		}{{"dns_sum", *out.obs.DNSUS, dnsRef}, {"final_ttfb", *out.obs.TTFBUS, finalRef}} {
			actual := float64(p.actual) / 1000
			expected := float64(p.ref.Microseconds()) / 1000
			diff := math.Abs(actual - expected)
			tol := math.Max(5, expected*.02)
			fmt.Printf("REDIRECT_FIELD run=%d phase=%s actual_ms=%.3f reference_ms=%.3f diff_ms=%.3f tolerance_ms=%.3f\n", run, p.name, actual, expected, diff, tol)
			if diff > tol {
				t.Errorf("run=%d phase=%s diff %.3f > %.3f", run, p.name, diff, tol)
			}
		}
		if *out.obs.TotalUS-*out.obs.TTFBUS < firstRef.Microseconds() {
			t.Errorf("run=%d redirect delay absent from total", run)
		}
	}
}
