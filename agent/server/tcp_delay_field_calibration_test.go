//go:build linux

package server

import (
	"encoding/binary"
	"fmt"
	"math"
	"net"
	"os"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// The target and interface must belong to an otherwise idle isolated test
// network. AF_PACKET observes the same connection, not a second dial. Capture
// timestamps are userspace receive times rather than kernel wire timestamps.
func TestTCPDelayFieldCalibration(t *testing.T) {
	target, ifaceName := os.Getenv("NEKOMARI_TCP_DELAY_TARGET"), os.Getenv("NEKOMARI_TCP_DELAY_IFACE")
	if target == "" || ifaceName == "" {
		t.Skip("requires isolated TCP test network")
	}
	_, portText, err := net.SplitHostPort(target)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil {
		t.Fatal(err)
	}
	iface, err := net.InterfaceByName(ifaceName)
	if err != nil {
		t.Fatal(err)
	}
	fd, err := syscall.Socket(syscall.AF_PACKET, syscall.SOCK_RAW, int(htons(3)))
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(fd)
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
			n, _, err := syscall.Recvfrom(fd, buf, 0)
			if err != nil {
				select {
				case <-stop:
					return
				default:
					continue
				}
			}
			at := time.Now()
			if n >= 54 {
				p := capturedPacket{at, append([]byte(nil), buf[:n]...)}
				select {
				case packets <- p:
				default:
				}
			}
		}
	}()
	defer func() { close(stop); <-done }()
	for run := 1; run <= 3; run++ {
		started := time.Now()
		latency, family, err := tcpPing(target, 2*time.Second)
		if err != nil {
			t.Fatalf("run=%d: %v", run, err)
		}
		if family != "ipv4" {
			t.Fatalf("run=%d family=%q", run, family)
		}
		var syn, ack time.Time
		var source uint16
		deadline := time.After(2 * time.Second)
	loop:
		for {
			select {
			case p := <-packets:
				d := p.data
				if len(d) < 54 || d[12] != 8 || d[13] != 0 || d[23] != 6 || p.at.Before(started) {
					continue
				}
				off := 14 + int(d[14]&15)*4
				if off+20 > len(d) {
					continue
				}
				sport, dport, flags := binary.BigEndian.Uint16(d[off:]), binary.BigEndian.Uint16(d[off+2:]), d[off+13]
				if uint64(dport) != port {
					continue
				}
				if flags&0x12 == 0x02 && syn.IsZero() {
					syn = p.at
					source = sport
					continue
				}
				if !syn.IsZero() && sport == source && flags&0x12 == 0x10 && p.at.After(syn) {
					ack = p.at
					break loop
				}
			case <-deadline:
				t.Fatalf("run=%d missing matching SYN/final ACK", run)
			}
		}
		actual, reference := float64(latency)/1000, float64(ack.Sub(syn).Microseconds())/1000
		diff := math.Abs(actual - reference)
		fmt.Printf("TCP_DELAY_FIELD run=%d actual_ms=%.3f reference_ms=%.3f diff_ms=%.3f tolerance_ms=1.000\n", run, actual, reference, diff)
		if diff > 1 {
			t.Errorf("run=%d difference %.3f ms exceeds 1 ms", run, diff)
		}
	}
}
