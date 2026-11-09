package server

import (
	"net"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
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

// ICMP loopback calibration: compare the production ICMP path with the system
// ping average over the same loopback target. The v1.8 tolerance is ±50 µs.
// Skipped when raw ICMP is unavailable or the host has no ping command.
func TestICMPLoopbackCalibration(t *testing.T) {
	if _, err := exec.LookPath("ping"); err != nil {
		t.Skip("system ping not available")
	}
	output, err := exec.Command("ping", "-c", "10", "-i", "0.2", "-q", "127.0.0.1").CombinedOutput()
	if err != nil {
		t.Skipf("system ping failed: %v", err)
	}
	match := regexp.MustCompile(`rtt min/avg/max/mdev = [0-9.]+/([0-9.]+)/`).FindStringSubmatch(string(output))
	if len(match) != 2 {
		t.Skipf("cannot parse ping output: %s", output)
	}
	referenceMS, err := strconv.ParseFloat(match[1], 64)
	if err != nil {
		t.Skipf("cannot parse ping average: %v", err)
	}

	var probes []int64
	for i := 0; i < 10; i++ {
		latency, _, err := icmpPing("127.0.0.1", time.Second)
		if err != nil {
			t.Skipf("ICMP probe unavailable: %v", err)
		}
		probes = append(probes, latency)
	}
	sort.Slice(probes, func(i, j int) bool { return probes[i] < probes[j] })
	probeUS := probes[len(probes)/2]
	referenceUS := int64(referenceMS * 1000)
	diff := probeUS - referenceUS
	if diff < 0 {
		diff = -diff
	}
	if diff > 50 {
		t.Fatalf("ICMP loopback median differs from system ping by %d µs (probe=%d ref=%d), want <=50 µs", diff, probeUS, referenceUS)
	}
}
