package server

import (
	"fmt"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// This opt-in test compares the production function with separately timed
// injected delays. It does not measure the system resolver or a remote network.
func TestHTTPFieldCalibration(t *testing.T) {
	if os.Getenv("NEKOMARI_HTTP_FIELD") != "1" {
		t.Skip("field calibration disabled")
	}
	for _, delay := range []time.Duration{0, 50 * time.Millisecond, 200 * time.Millisecond} {
		for run := 1; run <= 3; run++ {
			serverReference := make(chan time.Duration, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				start := time.Now()
				time.Sleep(delay)
				serverReference <- time.Since(start)
				w.WriteHeader(http.StatusOK)
			}))
			_, port, err := net.SplitHostPort(srv.Listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			var dnsReference time.Duration
			resolve := func(_, _ string) (string, error) {
				start := time.Now()
				time.Sleep(delay)
				dnsReference = time.Since(start)
				return "127.0.0.1", nil
			}
			out := httpProbeWithResolver("http://calibration.invalid:"+port, 5*time.Second, "", true, "", resolve)
			srv.Close()
			if out.err != nil || out.obs == nil || out.obs.DNSUS == nil || out.obs.TTFBUS == nil {
				t.Fatalf("delay=%s run=%d outcome=%+v", delay, run, out)
			}
			for _, phase := range []struct {
				name      string
				actual    int64
				reference time.Duration
			}{{"dns", *out.obs.DNSUS, dnsReference}, {"ttfb", *out.obs.TTFBUS, <-serverReference}} {
				reference := float64(phase.reference.Microseconds()) / 1000
				actual := float64(phase.actual) / 1000
				difference := math.Abs(actual - reference)
				tolerance := math.Max(5, reference*0.02)
				fmt.Printf("HTTP_FIELD phase=%s delay_ms=%d run=%d actual_ms=%.3f reference_ms=%.3f diff_ms=%.3f tolerance_ms=%.3f\n", phase.name, delay.Milliseconds(), run, actual, reference, difference, tolerance)
				if difference > tolerance {
					t.Errorf("%s delay=%s run=%d difference %.3f ms exceeds %.3f ms", phase.name, delay, run, difference, tolerance)
				}
			}
		}
	}
}
