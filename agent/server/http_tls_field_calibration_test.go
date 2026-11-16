package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
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

type tlsRef struct{ handshake, delay time.Duration }

type delayedTLSListener struct {
	net.Listener
	delay     time.Duration
	reference chan tlsRef
	config    *tls.Config
}

func (l *delayedTLSListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &measuredTLSConn{Conn: c, delay: l.delay, reference: l.reference, config: l.config}, nil
}

type measuredTLSConn struct {
	net.Conn
	delay     time.Duration
	reference chan tlsRef
	config    *tls.Config
}

func (c *measuredTLSConn) Read(b []byte) (int, error) {
	if _, ok := c.Conn.(*tls.Conn); ok {
		return c.Conn.Read(b)
	}
	start := time.Now()
	var pause time.Duration
	cfg := c.config.Clone()
	cfg.GetConfigForClient = func(*tls.ClientHelloInfo) (*tls.Config, error) {
		begin := time.Now()
		time.Sleep(c.delay)
		pause = time.Since(begin)
		return nil, nil
	}
	conn := tls.Server(c.Conn, cfg)
	err := conn.Handshake()
	c.reference <- tlsRef{handshake: time.Since(start), delay: pause}
	if err != nil {
		return 0, err
	}
	c.Conn = conn
	return c.Conn.Read(b)
}

// Isolated test process provides its own trusted ephemeral CA. The server
// independently times a delay after reading ClientHello, within the handshake.
func TestHTTPTLSFieldCalibration(t *testing.T) {
	if os.Getenv("NEKOMARI_HTTP_TLS_FIELD") != "1" {
		t.Skip("field calibration disabled")
	}
	if os.Getenv("NEKOMARI_HTTP_TLS_CHILD") != "1" {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fixture CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
		der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
		if err != nil {
			t.Fatal(err)
		}
		keyDER, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		certFile := filepath.Join(dir, "ca.pem")
		keyFile := filepath.Join(dir, "key.pem")
		if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestHTTPTLSFieldCalibration$", "-test.v", "-test.timeout=30s")
		cmd.Env = append(os.Environ(), "NEKOMARI_HTTP_TLS_CHILD=1", "SSL_CERT_FILE="+certFile, "NEKOMARI_HTTP_TLS_CERT="+certFile, "NEKOMARI_HTTP_TLS_KEY="+keyFile)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("TLS fixture: %v\n%s", err, out)
		} else {
			t.Logf("%s", out)
		}
		return
	}
	pair, err := tls.LoadX509KeyPair(os.Getenv("NEKOMARI_HTTP_TLS_CERT"), os.Getenv("NEKOMARI_HTTP_TLS_KEY"))
	if err != nil {
		t.Fatal(err)
	}
	for _, delay := range []time.Duration{0, 50 * time.Millisecond, 200 * time.Millisecond} {
		for run := 1; run <= 3; run++ {
			raw, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			reference := make(chan tlsRef, 1)
			listener := &delayedTLSListener{Listener: raw, delay: delay, reference: reference, config: &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}}
			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
			_ = srv.Listener.Close()
			srv.Listener = listener
			srv.Start()
			out := httpProbe("https://"+raw.Addr().String(), 5*time.Second, "", true)
			srv.Close()
			if out.err != nil || out.obs == nil || out.obs.TLSUS == nil {
				t.Fatalf("delay=%s run=%d outcome=%+v", delay, run, out)
			}
			ref := <-reference
			actual := float64(*out.obs.TLSUS) / 1000
			expected := float64(ref.handshake.Microseconds()) / 1000
			diff := math.Abs(actual - expected)
			tol := math.Max(5, expected*.02)
			result, _ := json.Marshal(map[string]any{"delay_ms": delay.Milliseconds(), "run": run, "actual_ms": actual, "reference_ms": expected, "injected_ms": float64(ref.delay.Microseconds()) / 1000, "diff_ms": diff, "tolerance_ms": tol})
			fmt.Printf("TLS_FIELD %s\n", result)
			if diff > tol {
				t.Errorf("delay=%s run=%d error %.3f ms exceeds %.3f ms", delay, run, diff, tol)
			}
		}
	}
}
