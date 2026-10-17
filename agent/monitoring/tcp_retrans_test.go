package monitoring

import (
	"strings"
	"testing"
)

func TestTCPRetransCountersAndIntervals(t *testing.T) {
	out, retrans, ok := parseTCPCounters(strings.NewReader("Ip: x y\nIp: 1 2\nTcp: InSegs OutSegs RetransSegs\nTcp: 7 1000 10\n"))
	if !ok || out != 1000 || retrans != 10 {
		t.Fatalf("counters %d/%d %v", out, retrans, ok)
	}
	var sampler tcpCounterSampler
	if sampler.sample(out, retrans) != nil {
		t.Fatal("first sample must be unknown")
	}
	if value := sampler.sample(1100, 12); value == nil || *value != 2 {
		t.Fatalf("want 2%% got %v", value)
	}
	if sampler.sample(1100, 12) != nil {
		t.Fatal("zero outgoing traffic must be unknown")
	}
	if sampler.sample(10, 1) != nil {
		t.Fatal("counter reset must be unknown")
	}
	if _, _, ok := parseTCPCounters(strings.NewReader("Tcp: OutSegs RetransSegs\nTcp: bad 1\n")); ok {
		t.Fatal("malformed counter accepted")
	}
}
