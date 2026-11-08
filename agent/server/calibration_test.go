package server

import (
	"net"
	"sort"
	"testing"
	"time"
)

// TCP loopback calibration: the probe's handshake time should track a direct
// dial within the v1.8 ±1 ms tolerance. This is a live check of the same
// production measurement path, not a mocked value.
func TestTCPPingLoopbackCalibration(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	target := listener.Addr().String()

	probe := make([]int64, 0, 20)
	direct := make([]int64, 0, 20)
	for i := 0; i < 20; i++ {
		latency, _, err := tcpPing(target, 2*time.Second)
		if err != nil {
			t.Fatalf("probe dial: %v", err)
		}
		start := time.Now()
		conn, err := net.DialTimeout("tcp", target, 2*time.Second)
		if err != nil {
			t.Fatalf("direct dial: %v", err)
		}
		direct = append(direct, time.Since(start).Microseconds())
		_ = conn.Close()
		probe = append(probe, latency)
	}
	sort.Slice(probe, func(i, j int) bool { return probe[i] < probe[j] })
	sort.Slice(direct, func(i, j int) bool { return direct[i] < direct[j] })
	probeMedian := probe[len(probe)/2]
	directMedian := direct[len(direct)/2]
	diff := probeMedian - directMedian
	if diff < 0 {
		diff = -diff
	}
	if diff > 1000 {
		t.Fatalf("loopback medians differ by %d µs (probe=%d direct=%d), want <=1000 µs", diff, probeMedian, directMedian)
	}
}
