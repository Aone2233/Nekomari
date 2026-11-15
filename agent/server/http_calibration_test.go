package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

func TestHTTPProbeDNSTimeoutOnRedirectDoesNotReportPartialSum(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://slow.test/", http.StatusFound)
	}))
	defer server.Close()
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	defer close(release)
	out := httpProbeWithResolver("http://first.test:"+port, 100*time.Millisecond, "", true, "", func(host, _ string) (string, error) {
		if host == "first.test" {
			return "127.0.0.1", nil
		}
		<-release
		return "", errors.New("resolution stopped")
	})
	if out.err == nil || out.obs == nil || out.obs.DNSUS != nil {
		t.Fatalf("redirect timeout reported a partial DNS sum: %+v", out)
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

func TestHTTPProbeFailedTLSHandshakeHasNoDuration(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer server.Close()
	out := httpProbe(server.URL, time.Second, "", true)
	if out.err == nil || out.obs == nil {
		t.Fatalf("expected certificate verification failure: %+v", out)
	}
	if out.obs.TLSUS != nil {
		t.Fatalf("failed TLS handshake reported a completed duration: %d µs", *out.obs.TLSUS)
	}
	if out.obs.Reachable == nil || *out.obs.Reachable {
		t.Fatalf("failed TLS handshake must not be reachable: %+v", out.obs)
	}
}

// A child process trusts the first hop only. This exercises the production
// transport without changing global roots for other tests.
func TestHTTPProbeRedirectTLSPhaseBelongsToFinalHop(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" || runtime.GOOS == "ios" {
		t.Skip("SSL_CERT_FILE does not override the platform certificate store")
	}
	if os.Getenv("NEKOMARI_TLS_REDIRECT_CHILD") == "1" {
		plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
		defer plain.Close()
		bad := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
		defer bad.Close()
		pair, err := tls.LoadX509KeyPair(os.Getenv("NEKOMARI_TLS_REDIRECT_CERT"), os.Getenv("NEKOMARI_TLS_REDIRECT_KEY"))
		if err != nil {
			t.Fatal(err)
		}
		first := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/plain" {
				http.Redirect(w, r, plain.URL, http.StatusFound)
				return
			}
			http.Redirect(w, r, bad.URL, http.StatusFound)
		}))
		first.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
		first.StartTLS()
		defer first.Close()
		for _, tc := range []struct {
			name, path string
			wantErr    bool
		}{{"plain final", "/plain", false}, {"untrusted final", "/bad", true}} {
			t.Run(tc.name, func(t *testing.T) {
				out := httpProbe(first.URL+tc.path, 2*time.Second, "", true)
				if (out.err != nil) != tc.wantErr || out.obs == nil || out.obs.TLSUS != nil {
					t.Fatalf("outcome err=%v obs=%+v", out.err, out.obs)
				}
				if out.obs.Redirects == nil || *out.obs.Redirects != 1 {
					t.Fatalf("missing redirect: %+v", out.obs)
				}
				if out.obs.Reachable == nil || *out.obs.Reachable == tc.wantErr {
					t.Fatalf("unexpected final reachability: %+v", out.obs)
				}
			})
		}
		return
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err = os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestHTTPProbeRedirectTLSPhaseBelongsToFinalHop$", "-test.v", "-test.timeout=15s")
	cmd.Env = append(os.Environ(), "NEKOMARI_TLS_REDIRECT_CHILD=1", "SSL_CERT_FILE="+certFile, "NEKOMARI_TLS_REDIRECT_CERT="+certFile, "NEKOMARI_TLS_REDIRECT_KEY="+keyFile)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("redirect child: %v\n%s", err, output)
	} else {
		t.Logf("%s", output)
	}
}
