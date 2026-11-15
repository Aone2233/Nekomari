package server

import (
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/agent/dnsresolver"
	"golang.org/x/net/dns/dnsmessage"
)

// Ordinary probes should use the same configured resolver as pinned probes.
// The reserved name is answered only by the disposable configured DNS server.
func TestOrdinaryProbeUsesConfiguredDNS(t *testing.T) {
	listener, err := net.ListenPacket("udp4", net.JoinHostPort(net.IPv4(127, 0, 0, 1).String(), "0"))
	if err != nil {
		t.Fatal(err)
	}
	previous := dnsresolver.CustomDNSServer
	dnsresolver.SetCustomDNSServer(listener.LocalAddr().String())
	var queries atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			n, peer, err := listener.ReadFrom(buf)
			if err != nil {
				return
			}
			var request dnsmessage.Message
			if request.Unpack(buf[:n]) != nil || len(request.Questions) != 1 {
				continue
			}
			queries.Add(1)
			q := request.Questions[0]
			reply := dnsmessage.Message{Header: dnsmessage.Header{ID: request.ID, Response: true, Authoritative: true, RecursionAvailable: true}, Questions: request.Questions}
			if q.Type == dnsmessage.TypeA {
				reply.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: q.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 1}, Body: &dnsmessage.AResource{A: [4]byte{127, 0, 0, 1}}}}
			} else if q.Type == dnsmessage.TypeAAAA {
				reply.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: q.Name, Type: dnsmessage.TypeAAAA, Class: dnsmessage.ClassINET, TTL: 1}, Body: &dnsmessage.AAAAResource{AAAA: [16]byte{15: 1}}}}
			}
			if packet, err := reply.Pack(); err == nil {
				_, _ = listener.WriteTo(packet, peer)
			}
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-done
		dnsresolver.CustomDNSServer = previous
	})
	address, err := resolveIPPreference("probe-custom-dns.example.invalid.", "4")
	if err != nil || address != net.IPv4(127, 0, 0, 1).String() || queries.Load() == 0 {
		t.Fatalf("ordinary lookup = %q, %v; configured DNS queries = %d", address, err, queries.Load())
	}
	if address, err := resolveIPPreference("probe-custom-dns.example.invalid.", "6"); err != nil || address != "::1" {
		t.Fatalf("IPv6 preference = %q, %v", address, err)
	}
	before := queries.Load()
	if address, err := resolveIPPreference("127.0.0.1", "6"); err != nil || address != "127.0.0.1" || queries.Load() != before {
		t.Fatalf("literal lookup = %q, %v; DNS queries changed from %d to %d", address, err, before, queries.Load())
	}

	webListener, err := net.Listen("tcp4", net.JoinHostPort(net.IPv4(127, 0, 0, 1).String(), "0"))
	if err != nil {
		t.Fatal(err)
	}
	web := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	_ = web.Listener.Close()
	web.Listener = webListener
	web.Start()
	t.Cleanup(web.Close)
	_, port, err := net.SplitHostPort(webListener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	before = queries.Load()
	out := httpProbePreference("http://"+net.JoinHostPort("probe-custom-dns.example.invalid.", port), 3*time.Second, "", true, "4")
	if out.err != nil || out.family != "ipv4" || out.obs == nil || out.obs.DNSUS == nil || queries.Load() <= before {
		t.Fatalf("HTTP probe = %+v; configured DNS queries before/after %d/%d", out, before, queries.Load())
	}
	before = queries.Load()
	if _, family, err := tcpPingPreference(net.JoinHostPort("probe-custom-dns.example.invalid.", port), 3*time.Second, "4"); err != nil || family != "ipv4" || queries.Load() <= before {
		t.Fatalf("TCP probe family=%q error=%v; configured DNS queries before/after %d/%d", family, err, before, queries.Load())
	}
}
