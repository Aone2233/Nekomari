package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestPinnedProbeTargetParsing(t *testing.T) {
	for _, tc := range []struct{ target, host, tcp string }{
		{"host.test", "host.test", "host.test:80"},
		{"host.test:443", "host.test", "host.test:443"},
		{"2001:db8::1", "2001:db8::1", "[2001:db8::1]:80"},
		{"[2001:db8::1]", "2001:db8::1", "[2001:db8::1]:80"},
		{"[2001:db8::1]:443", "2001:db8::1", "[2001:db8::1]:443"},
	} {
		host, err := pinnedTargetHost(tc.target)
		if err != nil || host != tc.host {
			t.Errorf("host(%q) = %q, %v; want %q", tc.target, host, err, tc.host)
		}
		tcp, err := pinnedTCPTarget(tc.target)
		if err != nil || tcp != tc.tcp {
			t.Errorf("tcp(%q) = %q, %v; want %q", tc.target, tcp, err, tc.tcp)
		}
	}
	for _, target := range []string{"", " host.test", "host.test:", "host.test:0", "host.test:65536", "host.test:+80", "host.test:080", "host.test:http", "[host.test]", "[::1", "::1]", "[::1]:"} {
		if host, err := pinnedTargetHost(target); err == nil {
			t.Errorf("host(%q) unexpectedly accepted as %q", target, host)
		}
		if tcp, err := pinnedTCPTarget(target); err == nil {
			t.Errorf("tcp(%q) unexpectedly accepted as %q", target, tcp)
		}
	}
}

func TestPinnedProbeDistinguishesDNSAndActualFailures(t *testing.T) {
	dnsErr := errors.New("synthetic DNS error")
	dialErr := errors.New("synthetic connection refused")
	var icmpCalls, dialCalls int
	r := pinnedProbeRunner{
		resolver: pinnedResolver{
			lookup: func(_ context.Context, host string) ([]netip.Addr, error) {
				switch host {
				case "missing.test":
					return nil, dnsErr
				case "v4.test":
					return testAddresses("192.0.2.1"), nil
				default:
					return testAddresses("192.0.2.1", "2001:db8::1"), nil
				}
			},
			dial: func(_ context.Context, network, address string) (net.Conn, error) {
				dialCalls++
				if network != "tcp6" || address != "[2001:db8::1]:80" {
					t.Errorf("dial(%q, %q)", network, address)
				}
				return nil, dialErr
			},
		},
		icmp: func(_ context.Context, ip string, _ time.Duration, _ bool) (int64, bool, error) {
			icmpCalls++
			if ip != "2001:db8::1" {
				t.Errorf("ICMP target = %q", ip)
			}
			return -1, false, nil
		},
	}
	for _, kind := range []string{"icmp", "tcp", "http", "auto", "dual"} {
		for _, target := range []string{"missing.test", "v4.test"} {
			t.Run(kind+"/"+target, func(t *testing.T) {
				for _, result := range r.probe(context.Background(), kind, target, pinnedIPv6, time.Second, nil) {
					if result.outcome.attempted || result.outcome.family != "" || result.outcome.err == nil {
						t.Fatalf("non-probe reported as a measurement: %+v", result)
					}
					if target == "missing.test" && !errors.Is(result.outcome.err, errPinnedDNS) {
						t.Fatalf("DNS error = %v", result.outcome.err)
					}
					if target == "v4.test" && !errors.Is(result.outcome.err, errPinnedNoAddress) {
						t.Fatalf("no-v6 error = %v", result.outcome.err)
					}
				}
			})
		}
	}
	if icmpCalls != 0 || dialCalls != 0 {
		t.Fatalf("non-probes reached transport: icmp=%d dial=%d", icmpCalls, dialCalls)
	}
	for _, kind := range []string{"icmp", "tcp", "http", "auto", "dual"} {
		results := r.probe(context.Background(), kind, "dual.test:80", pinnedIPv6, time.Second, nil)
		for _, result := range results {
			if !result.outcome.attempted || result.outcome.family != pinnedIPv6 || result.outcome.err == nil {
				t.Errorf("%s: actual failure lost its address family: %+v", kind, result)
			}
		}
	}
	if icmpCalls == 0 || dialCalls == 0 {
		t.Fatalf("actual probes did not reach transports: icmp=%d dial=%d", icmpCalls, dialCalls)
	}
}

func TestPinnedProbeICMPFallbackAndActualFamily(t *testing.T) {
	var privileges []bool
	r := pinnedProbeRunner{
		resolver: pinnedResolver{lookup: func(context.Context, string) ([]netip.Addr, error) {
			return testAddresses("192.0.2.1", "2001:db8::7"), nil
		}},
		icmp: func(_ context.Context, ip string, _ time.Duration, privileged bool) (int64, bool, error) {
			if ip != "2001:db8::7" {
				t.Errorf("ICMP target = %q", ip)
			}
			privileges = append(privileges, privileged)
			if privileged {
				return -1, false, errors.New("permission denied")
			}
			return 120, true, nil
		},
	}
	out := r.probeICMP(context.Background(), "target.test:443", pinnedIPv6, time.Second)
	if out.err != nil || !out.attempted || out.family != pinnedIPv6 || out.latencyUS != 120 {
		t.Fatalf("outcome = %+v", out)
	}
	if len(privileges) != 2 || !privileges[0] || privileges[1] {
		t.Fatalf("privilege sequence = %v", privileges)
	}
}

func TestPinnedProbeLocalSocketErrorsAreNotNetworkLoss(t *testing.T) {
	for _, dialErr := range []error{syscall.EAFNOSUPPORT, syscall.EPROTONOSUPPORT, syscall.EACCES} {
		r := pinnedProbeRunner{resolver: pinnedResolver{
			lookup: func(context.Context, string) ([]netip.Addr, error) { return testAddresses("2001:db8::1"), nil },
			dial:   func(context.Context, string, string) (net.Conn, error) { return nil, dialErr },
		}}
		for _, kind := range []string{"tcp", "http"} {
			result := r.probe(context.Background(), kind, "target.test:80", pinnedIPv6, time.Second, nil)
			if len(result) != 1 || result[0].outcome.attempted || !errors.Is(result[0].outcome.err, dialErr) {
				t.Fatalf("%s error %v result %+v", kind, dialErr, result)
			}
		}
	}
	r := pinnedProbeRunner{
		resolver: pinnedResolver{lookup: func(context.Context, string) ([]netip.Addr, error) { return testAddresses("2001:db8::1"), nil }},
		icmp: func(context.Context, string, time.Duration, bool) (int64, bool, error) {
			return -1, false, syscall.EACCES
		},
	}
	out := r.probeICMP(context.Background(), "target.test", pinnedIPv6, time.Second)
	if out.attempted || !errors.Is(out.err, errPinnedNoICMPSocket) {
		t.Fatalf("ICMP local failure = %+v", out)
	}
}

func TestPinnedAutoFallsBackWhenICMPUnavailable(t *testing.T) {
	r := pinnedProbeRunner{
		resolver: pinnedResolver{
			lookup: func(context.Context, string) ([]netip.Addr, error) { return testAddresses("2001:db8::1"), nil },
			dial: func(_ context.Context, network, address string) (net.Conn, error) {
				if network != "tcp6" || address != "[2001:db8::1]:443" {
					t.Fatalf("dial(%s,%s)", network, address)
				}
				a, b := net.Pipe()
				_ = b.Close()
				return a, nil
			},
		},
		icmp: func(context.Context, string, time.Duration, bool) (int64, bool, error) {
			return -1, false, syscall.EACCES
		},
	}
	result := r.probe(context.Background(), "auto", "target.test", pinnedIPv6, time.Second, nil)
	if len(result) != 1 || result[0].kind != "tcp" || !result[0].outcome.attempted || result[0].outcome.err != nil {
		t.Fatalf("auto result = %+v", result)
	}
	r.resolver.dial = func(context.Context, string, string) (net.Conn, error) { return nil, syscall.ECONNREFUSED }
	cache := &pinnedProbeCache{}
	result = r.probe(context.Background(), "auto", "target.test", pinnedIPv6, time.Second, cache)
	if len(result) != 1 || result[0].outcome.attempted || !errors.Is(result[0].outcome.err, errPinnedNoICMPSocket) || len(cache.decisions) != 0 {
		t.Fatalf("auto with unavailable ICMP and closed TCP = %+v, cache=%v", result, cache.decisions)
	}
}

func TestPinnedProbeAutoCacheAndDualDoNotMixFamilies(t *testing.T) {
	var lookedUp []string
	var icmpTargets []string
	r := pinnedProbeRunner{
		resolver: pinnedResolver{
			lookup: func(_ context.Context, host string) ([]netip.Addr, error) {
				lookedUp = append(lookedUp, host)
				return testAddresses("192.0.2.10", "2001:db8::10"), nil
			},
			dial: func(_ context.Context, network, address string) (net.Conn, error) {
				if network != "tcp6" || address != "[2001:db8::10]:443" {
					t.Errorf("unexpected cross-family dial(%q, %q)", network, address)
				}
				a, b := net.Pipe()
				_ = b.Close()
				return a, nil
			},
		},
		icmp: func(_ context.Context, ip string, _ time.Duration, _ bool) (int64, bool, error) {
			icmpTargets = append(icmpTargets, ip)
			return 20, ip == "192.0.2.10", nil
		},
	}
	cache := &pinnedProbeCache{}
	for _, tc := range []struct{ family, kind string }{{pinnedIPv4, "icmp"}, {pinnedIPv6, "tcp"}, {pinnedIPv4, "icmp"}, {pinnedIPv6, "tcp"}} {
		result := r.probe(context.Background(), "auto", "target.test", tc.family, time.Second, cache)
		if len(result) != 1 || result[0].kind != tc.kind || result[0].outcome.err != nil || result[0].outcome.family != tc.family {
			t.Fatalf("auto %s = %+v, want %s", tc.family, result, tc.kind)
		}
	}
	dual := r.probe(context.Background(), "dual", "target.test", pinnedIPv6, time.Second, cache)
	if len(dual) != 2 || dual[0].kind != "icmp" || dual[0].outcome.err == nil || dual[1].kind != "tcp" || dual[1].outcome.err != nil {
		t.Fatalf("dual = %+v", dual)
	}
	for _, leg := range dual {
		if !leg.outcome.attempted || leg.outcome.family != pinnedIPv6 {
			t.Fatalf("dual leg escaped IPv6: %+v", leg)
		}
	}
	if len(icmpTargets) != 5 {
		t.Fatalf("auto cache did not avoid a second decision probe: ICMP=%v DNS=%v", icmpTargets, lookedUp)
	}
}

func TestPinnedProbeRetriesDoNotTurnDNSChangeIntoLoss(t *testing.T) {
	var calls int
	r := pinnedProbeRunner{
		resolver: pinnedResolver{lookup: func(context.Context, string) ([]netip.Addr, error) {
			calls++
			if calls == 1 {
				return testAddresses("2001:db8::1"), nil
			}
			return testAddresses("192.0.2.1"), nil
		}},
		icmp: func(context.Context, string, time.Duration, bool) (int64, bool, error) {
			return highLatencyThreshold + 1, true, nil
		},
	}
	result := r.probe(context.Background(), "icmp", "changing.test", pinnedIPv6, time.Second, nil)
	if len(result) != 1 || result[0].outcome.attempted || !errors.Is(result[0].outcome.err, errPinnedNoAddress) || calls != 2 {
		t.Fatalf("result=%+v lookups=%d; retry DNS failure must not be loss", result, calls)
	}
}

func TestPinnedProbeCancellationNeverBecomesLoss(t *testing.T) {
	for _, kind := range []string{"icmp", "tcp", "http"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			started := make(chan struct{}, 1)
			r := pinnedProbeRunner{
				resolver: pinnedResolver{
					lookup: func(context.Context, string) ([]netip.Addr, error) { return testAddresses("2001:db8::1"), nil },
					dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
						started <- struct{}{}
						<-ctx.Done()
						return nil, ctx.Err()
					},
				},
				icmp: func(ctx context.Context, _ string, _ time.Duration, _ bool) (int64, bool, error) {
					started <- struct{}{}
					<-ctx.Done()
					return -1, false, ctx.Err()
				},
			}
			done := make(chan []pinnedProbeResult, 1)
			go func() { done <- r.probe(ctx, kind, "target.test:80", pinnedIPv6, time.Minute, nil) }()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("probe did not start")
			}
			cancel()
			select {
			case results := <-done:
				if len(results) != 1 || results[0].outcome.attempted || !errors.Is(results[0].outcome.err, context.Canceled) {
					t.Fatalf("canceled result = %+v", results)
				}
			case <-time.After(time.Second):
				t.Fatal("canceled probe did not return")
			}
		})
	}
}

func TestPinnedProbeReferenceUsesIndependentLegacyRunner(t *testing.T) {
	r := pinnedProbeRunner{
		resolver: pinnedResolver{
			lookup: func(context.Context, string) ([]netip.Addr, error) {
				return testAddresses("2001:db8::1"), nil
			},
			dial: func(context.Context, string, string) (net.Conn, error) {
				return nil, errors.New("synthetic dial failure")
			},
		},
		icmp: func(context.Context, string, time.Duration, bool) (int64, bool, error) { return 10, true, nil },
	}
	var refCalls int
	reference := func(kind, target string, _ time.Duration) pinnedProbeOutcome {
		refCalls++
		if kind != "icmp" || target != "gateway.test" {
			t.Errorf("reference kind=%q target=%q", kind, target)
		}
		return pinnedProbeOutcome{attempted: true, family: pinnedIPv4, latencyUS: 20}
	}
	results := r.probeWithReference(context.Background(), "dual", "primary.test:80", " gateway.test ", pinnedIPv6, time.Second, nil, reference)
	if len(results) != 3 || results[0].role != "primary" || results[1].role != "primary" || results[2].role != "reference" || results[2].outcome.family != pinnedIPv4 || refCalls != 1 {
		t.Fatalf("results=%+v reference calls=%d", results, refCalls)
	}
	bad := r.probeWithReference(context.Background(), "icmp", "primary.test", "gateway.test", "", time.Second, nil, reference)
	if len(bad) != 1 || !errors.Is(bad[0].outcome.err, errPinnedInvalidFamily) || refCalls != 1 {
		t.Fatalf("invalid family must not run any reference probe: %+v calls=%d", bad, refCalls)
	}
}

type pinnedWrongFamilyConn struct{ net.Conn }

func (c pinnedWrongFamilyConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("192.0.2.5"), Port: 80}
}

func TestPinnedProbeRejectsUnexpectedActualFamily(t *testing.T) {
	r := pinnedProbeRunner{resolver: pinnedResolver{
		lookup: func(context.Context, string) ([]netip.Addr, error) { return testAddresses("2001:db8::1"), nil },
		dial: func(context.Context, string, string) (net.Conn, error) {
			a, b := net.Pipe()
			_ = b.Close()
			return pinnedWrongFamilyConn{a}, nil
		},
	}}
	for _, kind := range []string{"tcp", "http"} {
		result := r.probe(context.Background(), kind, "target.test:80", pinnedIPv6, time.Second, nil)
		if len(result) != 1 || result[0].outcome.attempted || result[0].outcome.family != pinnedIPv4 || !errors.Is(result[0].outcome.err, errPinnedFamilyDrift) {
			t.Fatalf("%s outcome = %+v", kind, result)
		}
	}
}

func TestPinnedProbePreservesHTTPHostTLSAndRedirects(t *testing.T) {
	var hosts, names []string
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hosts = append(hosts, r.Host)
		if r.URL.Path == "/" {
			http.Redirect(w, r, "https://next.example.com:"+r.URL.Query().Get("port")+"/end", http.StatusFound)
		}
	}))
	s.TLS = &tls.Config{GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		names = append(names, hello.ServerName)
		return nil, nil
	}}
	s.StartTLS()
	defer s.Close()
	_, port, err := net.SplitHostPort(s.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(s.Certificate())
	var lookups []string
	r := pinnedProbeRunner{resolver: pinnedResolver{lookup: func(_ context.Context, host string) ([]netip.Addr, error) {
		lookups = append(lookups, host)
		return testAddresses("::1", "127.0.0.1"), nil
	}}}
	out := r.probeHTTP(context.Background(), "https://example.com:"+port+"/?port="+port, pinnedIPv4, time.Second, &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}})
	if out.err != nil || !out.attempted || out.family != pinnedIPv4 {
		t.Fatalf("outcome = %+v", out)
	}
	if got := strings.Join(lookups, ","); got != "example.com,next.example.com" {
		t.Fatalf("lookups = %q", got)
	}
	if got := strings.Join(names, ","); got != "example.com,next.example.com" {
		t.Fatalf("SNI = %q", got)
	}
	if got, want := strings.Join(hosts, ","), "example.com:"+port+",next.example.com:"+port; got != want {
		t.Fatalf("hosts = %q, want %q", got, want)
	}
}

func TestPinnedProbeHTTPRejectsWrongCertificateAndInvalidPorts(t *testing.T) {
	s := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("request reached a server with the wrong certificate")
	}))
	defer s.Close()
	_, port, err := net.SplitHostPort(s.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(s.Certificate())
	lookups := 0
	r := pinnedProbeRunner{resolver: pinnedResolver{lookup: func(context.Context, string) ([]netip.Addr, error) {
		lookups++
		return testAddresses("127.0.0.1"), nil
	}}}
	out := r.probeHTTP(context.Background(), "https://wrong.test:"+port, pinnedIPv4, time.Second, &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}})
	var hostnameErr x509.HostnameError
	if !out.attempted || !errors.As(out.err, &hostnameErr) {
		t.Fatalf("outcome = %+v, expected certificate hostname error", out)
	}
	for _, target := range []string{"http://bad.test:0", "http://bad.test:65536", "http://user:secret@bad.test"} {
		out := r.probeHTTP(context.Background(), target, pinnedIPv4, time.Second, nil)
		if out.attempted || !errors.Is(out.err, errPinnedInvalidTarget) {
			t.Errorf("%q outcome = %+v", target, out)
		}
	}
	if lookups != 1 {
		t.Fatalf("invalid targets reached DNS, lookups = %d", lookups)
	}
}

func TestPinnedProbeHTTPRedirectAndLocalTCP(t *testing.T) {
	requests := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path == "/" {
			http.Redirect(w, r, "http://v6-only.test:80/", http.StatusFound)
			return
		}
		_, _ = io.WriteString(w, "unexpected")
	}))
	defer s.Close()
	_, port, err := net.SplitHostPort(s.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	r := pinnedProbeRunner{resolver: pinnedResolver{lookup: func(_ context.Context, host string) ([]netip.Addr, error) {
		if host == "start.test" {
			return testAddresses("::1", "127.0.0.1"), nil
		}
		return testAddresses("::1"), nil
	}}}
	tcp := r.probeTCP(context.Background(), "start.test:"+port, pinnedIPv4, time.Second)
	if tcp.err != nil || !tcp.attempted || tcp.family != pinnedIPv4 {
		t.Fatalf("TCP = %+v", tcp)
	}
	result := r.probeHTTP(context.Background(), "http://start.test:"+port+"/", pinnedIPv4, time.Second, nil)
	if result.attempted || result.family != "" || !errors.Is(result.err, errPinnedNoAddress) || requests != 1 {
		t.Fatalf("redirect result=%+v requests=%d; no-v4 redirect is not packet loss", result, requests)
	}
}

func TestPinnedProbeRedirectPolicyErrorIsNotLoss(t *testing.T) {
	requests := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Redirect(w, r, "/loop", http.StatusFound)
	}))
	defer s.Close()
	_, port, err := net.SplitHostPort(s.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	r := pinnedProbeRunner{resolver: pinnedResolver{lookup: func(context.Context, string) ([]netip.Addr, error) {
		return testAddresses("127.0.0.1"), nil
	}}}
	out := r.probeHTTP(context.Background(), "http://loop.test:"+port+"/", pinnedIPv4, time.Second, nil)
	if out.attempted || out.family != "" || out.err == nil || !strings.Contains(out.err.Error(), "stopped after 10 redirects") || requests != 10 {
		t.Fatalf("redirect limit outcome=%+v requests=%d", out, requests)
	}
}

func TestPinnedProbeRejectsInvalidRedirectBeforeDial(t *testing.T) {
	requests := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Location", "http://redirect.test:0/")
		w.WriteHeader(http.StatusFound)
	}))
	defer s.Close()
	_, port, err := net.SplitHostPort(s.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	lookups := 0
	r := pinnedProbeRunner{resolver: pinnedResolver{lookup: func(_ context.Context, host string) ([]netip.Addr, error) {
		lookups++
		if host != "start.test" {
			t.Errorf("invalid redirect reached DNS: %q", host)
		}
		return testAddresses("127.0.0.1"), nil
	}}}
	out := r.probeHTTP(context.Background(), "http://start.test:"+port, pinnedIPv4, time.Second, nil)
	if out.attempted || out.family != "" || !errors.Is(out.err, errPinnedInvalidTarget) || requests != 1 || lookups != 1 {
		t.Fatalf("outcome=%+v requests=%d lookups=%d", out, requests, lookups)
	}
}

func TestPinnedProbeCacheIsBoundedAndExpires(t *testing.T) {
	cache := &pinnedProbeCache{}
	now := time.Now()
	cache.put("old", pinnedIPv4, autoDecision{kind: "icmp", at: now.Add(-autoCacheTTL)})
	if _, ok := cache.get("old", pinnedIPv4, now); ok {
		t.Fatal("expired cache entry used")
	}
	for i := 0; i < 4100; i++ {
		cache.put(strings.Repeat("x", i/1000)+time.Unix(int64(i), 0).String(), pinnedIPv6, autoDecision{kind: "icmp", at: now})
	}
	if len(cache.decisions) > 4096 {
		t.Fatalf("cache entries=%d, want <= 4096", len(cache.decisions))
	}
}
