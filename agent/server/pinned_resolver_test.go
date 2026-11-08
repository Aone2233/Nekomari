package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/agent/dnsresolver"
	"golang.org/x/net/dns/dnsmessage"
)

func testAddresses(values ...string) []netip.Addr {
	out := make([]netip.Addr, 0, len(values))
	for _, value := range values {
		out = append(out, netip.MustParseAddr(value))
	}
	return out
}

func TestPinnedResolverUsesConfiguredNodeDNS(t *testing.T) {
	listener, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	previous := dnsresolver.CustomDNSServer
	dnsresolver.SetCustomDNSServer(listener.LocalAddr().String())
	var mu sync.Mutex
	var questions []dnsmessage.Type
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
			question := request.Questions[0]
			mu.Lock()
			questions = append(questions, question.Type)
			mu.Unlock()
			answer := dnsmessage.Message{Header: dnsmessage.Header{ID: request.ID, Response: true, Authoritative: true, RecursionAvailable: true}, Questions: request.Questions}
			if question.Type == dnsmessage.TypeAAAA {
				answer.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: question.Name, Type: dnsmessage.TypeAAAA, Class: dnsmessage.ClassINET, TTL: 1},
					Body: &dnsmessage.AAAAResource{AAAA: netip.MustParseAddr("2001:db8::123").As16()}}}
			}
			encoded, err := answer.Pack()
			if err == nil {
				_, _ = listener.WriteTo(encoded, peer)
			}
		}
	}()
	defer func() {
		_ = listener.Close()
		<-done
		dnsresolver.CustomDNSServer = previous
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	addr, err := (pinnedResolver{}).resolve(ctx, "probe.example.invalid.", pinnedIPv6)
	if err != nil || addr.String() != "2001:db8::123" {
		t.Fatalf("custom DNS resolution = %s, %v", addr, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(questions) == 0 {
		t.Fatal("configured DNS server received no query")
	}
	for _, question := range questions {
		if question != dnsmessage.TypeAAAA {
			t.Fatalf("IPv6 pin sent another DNS family: %v", questions)
		}
	}
}

func TestPinnedResolverSelectsOnlyRequestedFamily(t *testing.T) {
	cases := []struct {
		name, family string
		answers      []netip.Addr
		want         string
		wantErr      error
	}{
		{"a only", pinnedIPv4, testAddresses("192.0.2.1"), "192.0.2.1", nil},
		{"aaaa only", pinnedIPv6, testAddresses("2001:db8::1"), "2001:db8::1", nil},
		{"v6 skips first v4", pinnedIPv6, testAddresses("192.0.2.1", "2001:db8::1"), "2001:db8::1", nil},
		{"v4 skips first v6", pinnedIPv4, testAddresses("2001:db8::1", "192.0.2.1"), "192.0.2.1", nil},
		{"mapped is v4", pinnedIPv4, testAddresses("::ffff:192.0.2.1"), "192.0.2.1", nil},
		{"mapped is not v6", pinnedIPv6, testAddresses("::ffff:192.0.2.1"), "", errPinnedNoAddress},
		{"no v4", pinnedIPv4, testAddresses("2001:db8::1"), "", errPinnedNoAddress},
		{"no v6", pinnedIPv6, testAddresses("192.0.2.1"), "", errPinnedNoAddress},
		{"empty answer", pinnedIPv6, nil, "", errPinnedNoAddress},
		{"missing family", "", testAddresses("192.0.2.1"), "", errPinnedInvalidFamily},
		{"unknown family", "any", testAddresses("192.0.2.1"), "", errPinnedInvalidFamily},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := pinnedResolver{lookup: func(_ context.Context, host string) ([]netip.Addr, error) {
				if host != "example.test" {
					t.Fatalf("lookup host = %q", host)
				}
				return tc.answers, nil
			}}
			got, err := r.resolve(context.Background(), "example.test", tc.family)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr == nil && got.String() != tc.want {
				t.Fatalf("address = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestPinnedResolverQueriesOnlyRequestedDNSFamily(t *testing.T) {
	var networks []string
	r := pinnedResolver{lookupNetwork: func(_ context.Context, network, host string) ([]netip.Addr, error) {
		if host != "example.test" {
			t.Fatalf("lookup host = %q", host)
		}
		networks = append(networks, network)
		switch network {
		case "ip4":
			return testAddresses("192.0.2.1"), nil
		case "ip6":
			return testAddresses("2001:db8::1"), nil
		case "ip":
			return testAddresses("192.0.2.1", "2001:db8::1"), nil
		default:
			t.Fatalf("unexpected lookup network %q", network)
			return nil, nil
		}
	}}
	for _, tc := range []struct{ family, want string }{
		{pinnedIPv4, "192.0.2.1"}, {pinnedIPv6, "2001:db8::1"},
	} {
		got, err := r.resolve(t.Context(), "example.test", tc.family)
		if err != nil || got.String() != tc.want {
			t.Fatalf("%s lookup = %s, %v", tc.family, got, err)
		}
	}
	if _, err := r.resolveAny(t.Context(), "example.test"); err != nil {
		t.Fatal(err)
	}
	if len(networks) != 3 || networks[0] != "ip4" || networks[1] != "ip6" || networks[2] != "ip" {
		t.Fatalf("lookup networks = %v", networks)
	}
}

func TestPinnedResolverLiteralsAndErrors(t *testing.T) {
	lookupErr := errors.New("synthetic DNS error")
	lookups := 0
	r := pinnedResolver{lookup: func(ctx context.Context, _ string) ([]netip.Addr, error) {
		lookups++
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, lookupErr
	}}
	cases := []struct {
		host, family, want string
		wantErr            error
	}{
		{"192.0.2.1", pinnedIPv4, "192.0.2.1", nil},
		{"[2001:db8::1]", pinnedIPv6, "2001:db8::1", nil},
		{"::ffff:192.0.2.1", pinnedIPv4, "192.0.2.1", nil},
		{"::ffff:192.0.2.1", pinnedIPv6, "", errPinnedFamilyMismatch},
		{"192.0.2.1", pinnedIPv6, "", errPinnedFamilyMismatch},
		{"[2001:db8::1]", pinnedIPv4, "", errPinnedFamilyMismatch},
		{"", pinnedIPv4, "", errPinnedInvalidTarget},
		{" example.test", pinnedIPv4, "", errPinnedInvalidTarget},
		{"example.test:80", pinnedIPv4, "", errPinnedInvalidTarget},
		{"[invalid]", pinnedIPv4, "", errPinnedInvalidTarget},
		{"example.test", pinnedIPv4, "", errPinnedDNS},
	}
	for _, tc := range cases {
		t.Run(tc.host+"/"+tc.family, func(t *testing.T) {
			got, err := r.resolve(context.Background(), tc.host, tc.family)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr == nil && got.String() != tc.want {
				t.Fatalf("address = %s, want %s", got, tc.want)
			}
			if errors.Is(err, errPinnedDNS) && !errors.Is(err, lookupErr) {
				t.Fatalf("DNS error lost its cause: %v", err)
			}
		})
	}
	if lookups != 1 {
		t.Fatalf("lookups = %d, literal targets must not use DNS", lookups)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.resolve(ctx, "example.test", pinnedIPv4); !errors.Is(err, context.Canceled) || !errors.Is(err, errPinnedDNS) {
		t.Fatalf("canceled DNS error = %v", err)
	}
}

func TestPinnedResolverClassifiesSystemNoRecordAsNonProbe(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want error
	}{
		{"name or record not found", &net.DNSError{Err: "no such host", Name: "example.test", IsNotFound: true}, errPinnedDNS},
		{"temporary DNS failure", &net.DNSError{Err: "server failed", Name: "example.test", IsTemporary: true}, errPinnedDNS},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := pinnedResolver{lookupNetwork: func(context.Context, string, string) ([]netip.Addr, error) {
				return nil, tc.err
			}}
			out := pinnedProbeRunner{resolver: r}.probeTCP(t.Context(), "example.test:80", pinnedIPv6, time.Second)
			if out.attempted || !errors.Is(out.err, tc.want) || !errors.Is(out.err, tc.err) {
				t.Fatalf("non-probe = %+v", out)
			}
		})
	}
}

func TestPinnedDialNeverFallsBackAndResolvesAgain(t *testing.T) {
	answers := [][]netip.Addr{
		testAddresses("2001:db8::1", "192.0.2.1"),
		testAddresses("192.0.2.2"),
		testAddresses("2001:db8::2"),
	}
	lookups := 0
	type dialCall struct{ network, target string }
	var calls []dialCall
	r := pinnedResolver{
		lookup: func(context.Context, string) ([]netip.Addr, error) {
			answer := answers[lookups]
			lookups++
			return answer, nil
		},
		dial: func(_ context.Context, network, target string) (net.Conn, error) {
			calls = append(calls, dialCall{network, target})
			return nil, errors.New("synthetic refused connection")
		},
	}
	for i := 0; i < 3; i++ {
		_, err := r.dialTCP(context.Background(), "tcp", "cdn.test:443", pinnedIPv4)
		if i < 2 && (err == nil || errors.Is(err, errPinnedNoAddress)) {
			t.Fatalf("attempt %d: expected dial failure, got %v", i, err)
		}
		if i == 2 && !errors.Is(err, errPinnedNoAddress) {
			t.Fatalf("attempt %d: expected no address, got %v", i, err)
		}
	}
	if lookups != 3 || len(calls) != 2 || calls[0] != (dialCall{"tcp4", "192.0.2.1:443"}) || calls[1] != (dialCall{"tcp4", "192.0.2.2:443"}) {
		t.Fatalf("lookups=%d, dial calls=%v", lookups, calls)
	}
	for _, tc := range []struct{ network, target, family string }{
		{"tcp6", "cdn.test:443", pinnedIPv4},
		{"udp", "cdn.test:443", pinnedIPv4},
		{"tcp", "cdn.test", pinnedIPv4},
		{"tcp", "cdn.test:443", ""},
	} {
		if _, err := r.dialTCP(context.Background(), tc.network, tc.target, tc.family); err == nil {
			t.Errorf("unexpectedly accepted %+v", tc)
		}
	}
	if lookups != 3 || len(calls) != 2 {
		t.Fatalf("invalid requests caused a lookup or dial: lookups=%d calls=%v", lookups, calls)
	}
}

func TestPinnedDialUsesIPv6AndPreservesPort(t *testing.T) {
	r := pinnedResolver{
		lookup: func(context.Context, string) ([]netip.Addr, error) {
			return testAddresses("192.0.2.1", "2001:db8::10"), nil
		},
		dial: func(_ context.Context, network, target string) (net.Conn, error) {
			if network != "tcp6" || target != "[2001:db8::10]:8080" {
				t.Fatalf("dial(%q, %q), want tcp6 and [2001:db8::10]:8080", network, target)
			}
			return nil, errors.New("synthetic dial")
		},
	}
	if _, err := r.dialTCP(context.Background(), "tcp", "cdn.test:8080", pinnedIPv6); err == nil {
		t.Fatal("expected synthetic dial error")
	}
}

func TestPinnedHTTPPreservesHostTLSAndRedirectPolicy(t *testing.T) {
	var hosts []string
	var serverNames []string
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hosts = append(hosts, r.Host)
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "https://next.example.com:"+r.URL.Query().Get("port")+"/end", http.StatusFound)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	s.TLS = &tls.Config{GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		serverNames = append(serverNames, hello.ServerName)
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
	var lookedUp []string
	r := pinnedResolver{lookup: func(_ context.Context, host string) ([]netip.Addr, error) {
		lookedUp = append(lookedUp, host)
		return testAddresses("2001:db8::1", "127.0.0.1"), nil
	}}
	transport := &http.Transport{
		TLSClientConfig:   &tls.Config{RootCAs: roots},
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return r.dialTCP(ctx, network, address, pinnedIPv4)
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	resp, err := client.Get("https://example.com:" + port + "/start?port=" + port)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %s", resp.Status)
	}
	if got, want := strings.Join(lookedUp, ","), "example.com,next.example.com"; got != want {
		t.Fatalf("lookups = %q, want %q", got, want)
	}
	if got, want := strings.Join(serverNames, ","), "example.com,next.example.com"; got != want {
		t.Fatalf("TLS server names = %q, want %q", got, want)
	}
	if got, want := strings.Join(hosts, ","), fmt.Sprintf("example.com:%s,next.example.com:%s", port, port); got != want {
		t.Fatalf("HTTP hosts = %q, want %q", got, want)
	}
}

func TestPinnedHTTPDoesNotBypassCertificateValidation(t *testing.T) {
	s := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("request reached a server with a certificate for a different hostname")
	}))
	defer s.Close()
	_, port, err := net.SplitHostPort(s.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(s.Certificate())
	r := pinnedResolver{lookup: func(context.Context, string) ([]netip.Addr, error) {
		return testAddresses("127.0.0.1"), nil
	}}
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: roots},
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return r.dialTCP(ctx, network, address, pinnedIPv4)
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	resp, err := client.Get("https://uncovered.test:" + port + "/")
	if resp != nil {
		_ = resp.Body.Close()
	}
	var hostnameErr x509.HostnameError
	if !errors.As(err, &hostnameErr) {
		t.Fatalf("error = %v, want certificate hostname validation error", err)
	}
}

func TestPinnedHTTPRedirectCannotSwitchFamily(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://opposite.test:80/", http.StatusFound)
	}))
	defer s.Close()
	_, port, err := net.SplitHostPort(s.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	var lookups []string
	r := pinnedResolver{lookup: func(_ context.Context, host string) ([]netip.Addr, error) {
		lookups = append(lookups, host)
		if host == "start.test" {
			return testAddresses("127.0.0.1"), nil
		}
		return testAddresses("::1"), nil
	}}
	transport := &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return r.dialTCP(ctx, network, address, pinnedIPv4)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	resp, err := client.Get("http://start.test:" + port + "/")
	if resp != nil {
		_ = resp.Body.Close()
	}
	if !errors.Is(err, errPinnedNoAddress) {
		t.Fatalf("redirect error = %v, want no matching address", err)
	}
	if got, want := strings.Join(lookups, ","), "start.test,opposite.test"; got != want {
		t.Fatalf("lookups = %q, want %q", got, want)
	}
}
