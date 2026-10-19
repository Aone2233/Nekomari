package server

import (
	"net/http"
	"testing"
)

// The panel's HTTP server must retire idle keep-alive connections. Go's zero IdleTimeout means
// "use ReadTimeout", and ReadTimeout is also zero here, so leaving it unset means "never" - one
// goroutine and its buffers per idle connection, for the life of the process.
//
// WriteTimeout is asserted to stay zero on purpose: setting it is the tempting "harden the server"
// change that would silently truncate file downloads and terminal streams.
func TestPanelServerRetiresIdleKeepAliveConnections(t *testing.T) {
	server := newHTTPServer("127.0.0.1:0", http.NotFoundHandler())
	if server.IdleTimeout != httpIdleTimeout {
		t.Fatalf("IdleTimeout = %s, want %s", server.IdleTimeout, httpIdleTimeout)
	}
	if httpIdleTimeout <= 0 {
		t.Fatalf("httpIdleTimeout = %s; a non-positive value disables retirement", httpIdleTimeout)
	}
	if server.ReadHeaderTimeout != httpReadHeaderTimeout {
		t.Fatalf("ReadHeaderTimeout = %s, want %s", server.ReadHeaderTimeout, httpReadHeaderTimeout)
	}
	if server.WriteTimeout != 0 {
		t.Fatalf("WriteTimeout = %s; it would cut downloads and terminal streams", server.WriteTimeout)
	}
}
