//go:build linux

package server

import (
	"encoding/binary"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"syscall"
	"testing"
	"time"
)

type capturedPacket struct {
	at   time.Time
	data []byte
}

// Packet capture brackets SYN to final ACK; this differs slightly from the
// client's connect syscall boundaries. Requires CAP_NET_RAW and loopback.
func TestHTTPTCPFieldCalibration(t *testing.T) {
	if os.Getenv("NEKOMARI_HTTP_TCP_FIELD") != "1" {
		t.Skip("requires raw packet capture privilege")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer srv.Close()
	port := srv.Listener.Addr().(*net.TCPAddr).Port
	fd, err := syscall.Socket(syscall.AF_PACKET, syscall.SOCK_RAW, int(htons(3)))
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(fd)
	iface, err := net.InterfaceByName("lo")
	if err != nil {
		t.Fatal(err)
	}
	if err = syscall.Bind(fd, &syscall.SockaddrLinklayer{Protocol: htons(3), Ifindex: iface.Index}); err != nil {
		t.Fatal(err)
	}
	if err = syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &syscall.Timeval{Sec: 1}); err != nil {
		t.Fatal(err)
	}
	packets := make(chan capturedPacket, 1000)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 65536)
		for {
			n, _, e := syscall.Recvfrom(fd, buf, 0)
			if e != nil {
				select {
				case <-stop:
					return
				default:
					continue
				}
			}
			at := time.Now()
			if n >= 54 {
				copyBuf := append([]byte(nil), buf[:n]...)
				select {
				case packets <- capturedPacket{at, copyBuf}:
				default:
				}
			}
		}
	}()
	defer func() { close(stop); <-done }()
	for run := 1; run <= 3; run++ {
		out := httpProbe(srv.URL, 2*time.Second, "", true)
		if out.err != nil || out.obs == nil || out.obs.TCPUS == nil {
			t.Fatalf("run=%d outcome=%+v", run, out)
		}
		var syn, ack time.Time
		var src uint16
		deadline := time.After(2 * time.Second)
	loop:
		for {
			select {
			case p := <-packets:
				d := p.data
				if len(d) < 54 || d[12] != 8 || d[13] != 0 || d[23] != 6 {
					continue
				}
				ihl := int(d[14]&15) * 4
				off := 14 + ihl
				if off+20 > len(d) {
					continue
				}
				sport := binary.BigEndian.Uint16(d[off:])
				dport := binary.BigEndian.Uint16(d[off+2:])
				flags := d[off+13]
				if int(dport) != port {
					continue
				}
				if flags&0x12 == 0x02 && syn.IsZero() {
					syn = p.at
					src = sport
				}
				if !syn.IsZero() && sport == src && flags&0x12 == 0x10 && p.at.After(syn) {
					ack = p.at
					break loop
				}
			case <-deadline:
				t.Fatalf("run=%d did not capture complete handshake", run)
			}
		}
		actual := float64(*out.obs.TCPUS) / 1000
		reference := float64(ack.Sub(syn).Microseconds()) / 1000
		diff := math.Abs(actual - reference)
		fmt.Printf("TCP_FIELD run=%d actual_ms=%.3f reference_ms=%.3f diff_ms=%.3f tolerance_ms=5.000\n", run, actual, reference, diff)
		if diff > 5 {
			t.Errorf("run=%d difference %.3f ms > 5 ms", run, diff)
		}
	}
}
func htons(v uint16) uint16 { return v<<8 | v>>8 }
