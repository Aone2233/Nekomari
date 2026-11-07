package server

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPPingLocalSuccessPreservesActualFamily(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer s.Close()
	latency, family, err := httpPing(s.URL, time.Second)
	if err != nil || latency < 0 || family != "ipv4" {
		t.Fatalf("local HTTP probe = %d, %q, %v", latency, family, err)
	}
}

func TestHTTPPingTimeoutCanReturnBeforeResolverFinishes(t *testing.T) {
	dialed := make(chan struct{})
	resolve := func(string) (string, error) {
		// Let Client.Timeout return while net/http's dial goroutine is still in
		// resolution. It may publish its family after the caller has returned.
		time.Sleep(100 * time.Millisecond)
		return "127.0.0.1", nil
	}
	dial := func(_, _ string, _ time.Duration) (net.Conn, error) {
		close(dialed)
		return nil, errors.New("synthetic dial failure")
	}
	latency, family, err := httpPingWith("http://example.test", 20*time.Millisecond, resolve, dial)
	if err == nil || latency != -1 || family != "" {
		t.Fatalf("timed-out HTTP probe = %d, %q, %v", latency, family, err)
	}
	select {
	case <-dialed:
	case <-time.After(time.Second):
		t.Fatal("delayed transport dial did not finish")
	}
}
