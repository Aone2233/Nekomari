package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"math"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Compare the last of two independently timed TLS handshakes and response
// delays. The first hop is slower so its inclusion cannot pass unnoticed.
func TestHTTPHTTPSRedirectFieldCalibration(t *testing.T) {
	if os.Getenv("NEKOMARI_HTTPS_REDIRECT_FIELD") != "1" {
		t.Skip("field calibration disabled")
	}
	if os.Getenv("NEKOMARI_HTTPS_REDIRECT_CHILD") != "1" {
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
		certFile, keyFile := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "key.pem")
		if err = os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestHTTPHTTPSRedirectFieldCalibration$", "-test.v", "-test.timeout=30s")
		cmd.Env = append(os.Environ(), "NEKOMARI_HTTPS_REDIRECT_CHILD=1", "SSL_CERT_FILE="+certFile, "NEKOMARI_REDIRECT_CERT="+certFile, "NEKOMARI_REDIRECT_KEY="+keyFile)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("HTTPS redirect fixture: %v\n%s", err, out)
		}
		t.Logf("%s", out)
		return
	}
	pair, err := tls.LoadX509KeyPair(os.Getenv("NEKOMARI_REDIRECT_CERT"), os.Getenv("NEKOMARI_REDIRECT_KEY"))
	if err != nil {
		t.Fatal(err)
	}
	for run := 1; run <= 3; run++ {
		startServer := func(handshakeDelay, responseDelay time.Duration, destination string) (*httptest.Server, <-chan tlsRef, <-chan time.Duration) {
			raw, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			handshake := make(chan tlsRef, 1)
			response := make(chan time.Duration, 1)
			listener := &delayedTLSListener{Listener: raw, delay: handshakeDelay, reference: handshake, config: &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}}
			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				start := time.Now()
				time.Sleep(responseDelay)
				response <- time.Since(start)
				if destination != "" {
					http.Redirect(w, r, destination, http.StatusFound)
					return
				}
				w.WriteHeader(http.StatusOK)
			}))
			_ = srv.Listener.Close()
			srv.Listener = listener
			srv.Start()
			return srv, handshake, response
		}
		final, finalTLS, finalResponse := startServer(20*time.Millisecond, 40*time.Millisecond, "")
		first, firstTLS, firstResponse := startServer(70*time.Millisecond, 80*time.Millisecond, "https://"+final.Listener.Addr().String()+"/final")
		out := httpProbe("https://"+first.Listener.Addr().String(), 5*time.Second, "", true)
		first.Close()
		final.Close()
		if out.err != nil || out.obs == nil || out.obs.TLSUS == nil || out.obs.TTFBUS == nil || out.obs.TotalUS == nil || out.obs.Redirects == nil || *out.obs.Redirects != 1 || out.obs.StatusCode == nil || *out.obs.StatusCode != 200 || out.obs.TLSExpiryDays == nil {
			t.Fatalf("run=%d outcome=%+v", run, out)
		}
		firstHandshake, lastHandshake := <-firstTLS, <-finalTLS
		firstWait, lastWait := <-firstResponse, <-finalResponse
		for _, p := range []struct {
			name      string
			actual    int64
			reference time.Duration
		}{{"final_tls", *out.obs.TLSUS, lastHandshake.handshake}, {"final_ttfb", *out.obs.TTFBUS, lastWait}} {
			actual, expected := float64(p.actual)/1000, float64(p.reference.Microseconds())/1000
			diff, tolerance := math.Abs(actual-expected), math.Max(5, expected*.02)
			fmt.Printf("HTTPS_REDIRECT_FIELD run=%d phase=%s actual_ms=%.3f reference_ms=%.3f diff_ms=%.3f tolerance_ms=%.3f\n", run, p.name, actual, expected, diff, tolerance)
			if diff > tolerance {
				t.Errorf("run=%d phase=%s difference %.3f > %.3f", run, p.name, diff, tolerance)
			}
		}
		if *out.obs.TotalUS-*out.obs.TTFBUS < (firstHandshake.handshake + firstWait).Microseconds() {
			t.Errorf("run=%d first-hop delay absent from total", run)
		}
	}
}
