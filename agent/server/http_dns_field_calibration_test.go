package server

import (
	"context"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// An actual local UDP DNS server supplies a separately timed response delay.
// The custom resolver dials only that server; the system resolver is untouched.
func TestHTTPDNSFieldCalibration(t *testing.T) {
	if os.Getenv("NEKOMARI_HTTP_DNS_FIELD") != "1" {
		t.Skip("field calibration disabled")
	}
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer web.Close()
	_, port, err := net.SplitHostPort(web.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	for _, delay := range []time.Duration{0, 50 * time.Millisecond, 200 * time.Millisecond} {
		for run := 1; run <= 3; run++ {
			dns, err := net.ListenPacket("udp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			ref := make(chan time.Duration, 1)
			errs := make(chan error, 1)
			go func() {
				buf := make([]byte, 1500)
				_ = dns.SetReadDeadline(time.Now().Add(3 * time.Second))
				n, addr, err := dns.ReadFrom(buf)
				if err != nil {
					errs <- err
					return
				}
				var request dnsmessage.Message
				if err = request.Unpack(buf[:n]); err != nil {
					errs <- err
					return
				}
				if len(request.Questions) != 1 || request.Questions[0].Type != dnsmessage.TypeA {
					errs <- fmt.Errorf("unexpected DNS question: %+v", request.Questions)
					return
				}
				q := request.Questions[0]
				reply := dnsmessage.Message{Header: dnsmessage.Header{ID: request.Header.ID, Response: true, RecursionAvailable: true}, Questions: request.Questions, Answers: []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: q.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 1}, Body: &dnsmessage.AResource{A: [4]byte{127, 0, 0, 1}}}}}
				packed, err := reply.Pack()
				if err != nil {
					errs <- err
					return
				}
				start := time.Now()
				time.Sleep(delay)
				elapsed := time.Since(start)
				_, err = dns.WriteTo(packed, addr)
				if err != nil {
					errs <- err
					return
				}
				ref <- elapsed
			}()
			resolver := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "udp4", dns.LocalAddr().String())
			}}
			out := httpProbeWithResolver("http://dns-field.invalid:"+port, 5*time.Second, "", true, "", func(host, _ string) (string, error) {
				ips, err := resolver.LookupIP(context.Background(), "ip4", host)
				if err != nil {
					return "", err
				}
				if len(ips) == 0 {
					return "", fmt.Errorf("no address")
				}
				return ips[0].String(), nil
			})
			dns.Close()
			if out.err != nil || out.obs == nil || out.obs.DNSUS == nil {
				t.Fatalf("delay=%s run=%d outcome=%+v", delay, run, out)
			}
			var reference time.Duration
			select {
			case reference = <-ref:
			case err := <-errs:
				t.Fatal(err)
			case <-time.After(time.Second):
				t.Fatal("missing DNS reference")
			}
			actual := float64(*out.obs.DNSUS) / 1000
			expected := float64(reference.Microseconds()) / 1000
			diff := math.Abs(actual - expected)
			tol := math.Max(5, expected*.02)
			fmt.Printf("DNS_FIELD delay_ms=%d run=%d actual_ms=%.3f reference_ms=%.3f diff_ms=%.3f tolerance_ms=%.3f\n", delay.Milliseconds(), run, actual, expected, diff, tol)
			if diff > tol {
				t.Errorf("delay=%s run=%d error %.3f ms exceeds %.3f ms", delay, run, diff, tol)
			}
		}
	}
}
