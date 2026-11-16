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

	"github.com/Aone2233/nekomari/agent/dnsresolver"
	"golang.org/x/net/dns/dnsmessage"
)

// Calibrates the production custom-resolver path against a local UDP server.
func TestHTTPConfiguredDNSFieldCalibration(t *testing.T) {
	if os.Getenv("NEKOMARI_HTTP_CONFIGURED_DNS_FIELD") != "1" {
		t.Skip("field calibration disabled")
	}
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer web.Close()
	_, port, err := net.SplitHostPort(web.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	previous := dnsresolver.CustomDNSServer
	defer func() { dnsresolver.CustomDNSServer = previous }()
	for _, delay := range []time.Duration{0, 50 * time.Millisecond, 200 * time.Millisecond} {
		for run := 1; run <= 3; run++ {
			dns, err := net.ListenPacket("udp4", net.JoinHostPort(net.IPv4(127, 0, 0, 1).String(), "0"))
			if err != nil {
				t.Fatal(err)
			}
			dnsresolver.SetCustomDNSServer(dns.LocalAddr().String())
			refs := make(chan time.Duration, 8)
			var workers sync.WaitGroup
			done := make(chan struct{})
			go func() {
				defer close(done)
				buf := make([]byte, 4096)
				for {
					n, addr, err := dns.ReadFrom(buf)
					if err != nil {
						break
					}
					var req dnsmessage.Message
					if req.Unpack(buf[:n]) != nil || len(req.Questions) != 1 {
						continue
					}
					workers.Add(1)
					go func(req dnsmessage.Message, addr net.Addr) {
						defer workers.Done()
						q := req.Questions[0]
						reply := dnsmessage.Message{Header: dnsmessage.Header{ID: req.ID, Response: true, Authoritative: true, RecursionAvailable: true}, Questions: req.Questions}
						if q.Type == dnsmessage.TypeA {
							reply.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: q.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 1}, Body: &dnsmessage.AResource{A: [4]byte{127, 0, 0, 1}}}}
						}
						packet, err := reply.Pack()
						if err != nil {
							return
						}
						start := time.Now()
						time.Sleep(delay)
						elapsed := time.Since(start)
						if _, err := dns.WriteTo(packet, addr); err == nil {
							refs <- elapsed
						}
					}(req, addr)
				}
				workers.Wait()
			}()
			out := httpProbePreference("http://configured-dns-field.example.invalid.:"+port, 5*time.Second, "", true, "4")
			_ = dns.Close()
			<-done
			close(refs)
			var maximum time.Duration
			count := 0
			for ref := range refs {
				count++
				if ref > maximum {
					maximum = ref
				}
			}
			if out.err != nil || out.obs == nil || out.obs.DNSUS == nil || count < 2 {
				t.Fatalf("delay=%s run=%d queries=%d outcome=%+v", delay, run, count, out)
			}
			actual := float64(*out.obs.DNSUS) / 1000
			expected := float64(maximum.Microseconds()) / 1000
			diff := math.Abs(actual - expected)
			tolerance := math.Max(5, expected*.02)
			fmt.Printf("CONFIGURED_DNS delay_ms=%d run=%d queries=%d actual_ms=%.3f reference_ms=%.3f diff_ms=%.3f tolerance_ms=%.3f\n", delay.Milliseconds(), run, count, actual, expected, diff, tolerance)
			if diff > tolerance {
				t.Errorf("delay=%s run=%d error %.3f ms exceeds %.3f ms", delay, run, diff, tolerance)
			}
		}
	}
}
