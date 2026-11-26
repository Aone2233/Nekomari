package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	v2 "github.com/Aone2233/nekomari/agent/protocol/v2"
	"github.com/Aone2233/nekomari/agent/ws"
	"github.com/gorilla/websocket"
)

// This process is launched by the panel integration test. It uses the real
// Agent reader/handshake/probe implementation, with test-only DNS and dial
// routing; it must never be enabled through production configuration.
func TestPinnedPanelIntegrationChild(t *testing.T) {
	endpoint := os.Getenv("NEKOMARI_PINNED_TEST_WS")
	if endpoint == "" {
		t.Skip("panel integration child only")
	}
	mode, target, token := os.Getenv("NEKOMARI_PINNED_TEST_MODE"), os.Getenv("NEKOMARI_PINNED_TEST_TARGET"), os.Getenv("NEKOMARI_PINNED_TEST_TOKEN")
	hostname := os.Getenv("NEKOMARI_PINNED_TEST_HOSTNAME")
	if hostname == "" {
		hostname = "probe.test"
	}
	start, release := os.Getenv("NEKOMARI_PINNED_TEST_START"), os.Getenv("NEKOMARI_PINNED_TEST_RELEASE")
	resultPath := os.Getenv("NEKOMARI_PINNED_TEST_RESULT")
	ackPath := os.Getenv("NEKOMARI_PINNED_TEST_ACK")
	readyPath := os.Getenv("NEKOMARI_PINNED_TEST_READY")
	renewal := time.Second
	if os.Getenv("NEKOMARI_PINNED_TEST_NORMAL_HEARTBEAT") == "1" {
		renewal = pinnedHandshakeTick
	}
	disabled := os.Getenv("NEKOMARI_PINNED_TEST_DISABLE_PROBE") == "1"
	if (mode != "ipv6" && mode != "no-family") || target == "" || token == "" {
		t.Fatal("invalid isolated test configuration")
	}
	if (start == "") != (release == "") || (start != "" && (!filepath.IsAbs(start) || !filepath.IsAbs(release) || mode != "ipv6")) {
		t.Fatal("invalid probe barrier")
	}
	if resultPath != "" && (!filepath.IsAbs(resultPath) || !filepath.IsAbs(ackPath)) {
		t.Fatal("invalid result marker")
	}
	if readyPath != "" && !filepath.IsAbs(readyPath) {
		t.Fatal("invalid readiness marker")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	headers := http.Header{"Authorization": []string{"Bearer " + token}}
	raw, _, err := websocket.DefaultDialer.DialContext(ctx, endpoint, headers)
	if err != nil {
		t.Fatal(err)
	}
	conn := &pinnedIntegrationConn{SafeConn: ws.NewSafeConn(raw), ackPath: ackPath}
	defer conn.Close()
	done := make(chan struct{})
	// The production Agent writes reports regularly. Keep this isolated test
	// connection alive across the panel's 11-second read deadline as well.
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if err := conn.WriteJSON(v2.Request{JSONRPC: v2.Version, ID: "pinned-test-keepalive", Method: "fixture.barrier"}); err != nil {
					return
				}
			}
		}
	}()
	var probes sync.WaitGroup
	go handleWebSocketMessagesWithController(conn, done, renewal, newPinnedWSHandshake(), func(c *pinnedWSController) {
		if disabled {
			c.probeDispatch = nil
		} else if resultPath == "" {
			enablePinnedProbeExecution(c)
		} else {
			c.probeDispatch = func(p v2.PingFamilyProbeParams) {
				probes.Add(1)
				if err := c.launchProbe(p, func(ctx context.Context) {
					defer probes.Done()
					c.runPinnedProbe(ctx, p, &pinnedTestResultMarker{conn: conn.SafeConn, resultPath: resultPath})
				}); err != nil {
					probes.Done()
				}
			}
		}
		if readyPath != "" {
			go func() {
				ticker := time.NewTicker(10 * time.Millisecond)
				defer ticker.Stop()
				for {
					if _, _, _, _, ok := c.current(time.Now()); ok {
						_ = os.WriteFile(readyPath, []byte("ready\n"), 0600)
						return
					}
					select {
					case <-done:
						return
					case <-ticker.C:
					}
				}
			}()
		}
		c.probeRunner.resolver.lookupNetwork = func(_ context.Context, network, name string) ([]netip.Addr, error) {
			if name != hostname {
				return nil, errors.New("unexpected DNS query")
			}
			if network != "ip6" {
				return nil, errors.New("unexpected DNS family")
			}
			if mode == "no-family" {
				return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
			}
			return []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("::1")}, nil
		}
		c.probeRunner.resolver.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
			if mode != "ipv6" || network != "tcp6" || address != "[::1]:80" {
				return nil, errors.New("unexpected test dial")
			}
			if start != "" {
				if err := os.WriteFile(start, []byte("started\n"), 0600); err != nil {
					return nil, fmt.Errorf("mark dial start: %w", err)
				}
				ticker := time.NewTicker(10 * time.Millisecond)
				defer ticker.Stop()
				for {
					if _, err := os.Stat(release); err == nil {
						break
					} else if !os.IsNotExist(err) {
						return nil, err
					}
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-ticker.C:
					}
				}
			}
			return (&net.Dialer{}).DialContext(ctx, network, target)
		}
	})
	select {
	case <-done:
	case <-ctx.Done():
		conn.Abort()
		<-done
		t.Fatal("panel test did not finish")
	}
	probesDone := make(chan struct{})
	go func() { probes.Wait(); close(probesDone) }()
	select {
	case <-probesDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Agent probes did not stop after socket retirement")
	}
}

type pinnedTestResultMarker struct {
	conn       *ws.SafeConn
	resultPath string
}

func (w *pinnedTestResultMarker) WriteJSONContext(ctx context.Context, v interface{}) error {
	if err := w.conn.WriteJSONContext(ctx, v); err != nil {
		return err
	}
	if err := os.WriteFile(w.resultPath, []byte("sent\n"), 0600); err != nil {
		return err
	}
	// A normal ordered request produces a response only after the panel has
	// processed the preceding notification on this socket.
	return w.conn.WriteJSON(v2.Request{JSONRPC: v2.Version, ID: "pinned-test-barrier", Method: "fixture.barrier"})
}

type pinnedIntegrationConn struct {
	*ws.SafeConn
	ackPath string
}

func (c *pinnedIntegrationConn) ReadMessageLimited(limit int64) (int, []byte, error) {
	kind, data, err := c.SafeConn.ReadMessageLimited(limit)
	if err == nil && c.ackPath != "" {
		var response struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(data, &response) == nil && response.ID == "pinned-test-barrier" {
			if err := os.WriteFile(c.ackPath, []byte("handled\n"), 0600); err != nil {
				return kind, nil, err
			}
		}
	}
	return kind, data, err
}
