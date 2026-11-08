package utils

import (
	"math"
	"strconv"
	"testing"
	"time"
)

func TestValidatePinnedPingInterval(t *testing.T) {
	for _, interval := range []int{-1, 0} {
		if ValidatePinnedPingInterval(interval) == nil {
			t.Fatalf("accepted interval %d", interval)
		}
	}
	for _, interval := range []int{1, 60} {
		if err := ValidatePinnedPingInterval(interval); err != nil {
			t.Fatalf("refused interval %d: %v", interval, err)
		}
	}
	if strconv.IntSize == 64 {
		maxSeconds := int64(math.MaxInt64 / int64(time.Second))
		if err := ValidatePinnedPingInterval(int(maxSeconds)); err != nil {
			t.Fatalf("refused maximum interval: %v", err)
		}
		if ValidatePinnedPingInterval(int(maxSeconds+1)) == nil {
			t.Fatal("accepted interval overflowing time.Duration")
		}
	}
}

func TestValidatePinnedPingTarget(t *testing.T) {
	for _, tc := range []struct {
		target string
		kind   string
		family string
		ok     bool
	}{
		{"tj-ct-v6.ip.zstaticcdn.com:80", "auto", "ipv6", true},
		{"example.test", "dual", "ipv4", true},
		{"[2001:db8::1]:80", "tcp", "ipv6", true},
		{"2001:db8::1", "icmp", "ipv6", true},
		{"[2001:db8::1]", "auto", "ipv6", true},
		{"127.0.0.1:443", "tcp", "ipv4", true},
		{"[::ffff:127.0.0.1]:443", "tcp", "ipv4", true},
		{"https://example.test/path", "http", "ipv6", true},
		{"2001:db8::1", "http", "ipv6", true},
		{"[::ffff:127.0.0.1]:443", "tcp", "ipv6", false},
		{"127.0.0.1:443", "tcp", "ipv6", false},
		{"[2001:db8::1]:80", "tcp", "ipv4", false},
		{"[example.test]:80", "tcp", "ipv6", false},
		{"[192.0.2.1]:80", "tcp", "ipv4", false},
		{"http://[example.test]:80", "http", "ipv6", false},
		{"http://[192.0.2.1]:80", "http", "ipv4", false},
		{"https://[2001:db8::1]/", "http", "ipv4", false},
		{"example.test:0", "tcp", "ipv4", false},
		{"example.test:65536", "auto", "ipv6", false},
		{"example.test:080", "auto", "ipv6", false},
		{"example.test:", "tcp", "ipv4", false},
		{"https://user@example.test", "http", "ipv4", false},
		{"http://example.test", "tcp", "ipv4", false},
		{"bad host:80", "tcp", "ipv4", false},
		{"[fe80::1%eth0]:80", "tcp", "ipv6", false},
		{"example.test:80", "bad", "ipv6", false},
		{"example.test:80", "auto", "", false},
		{"example.test:80", "auto", "IPv6", false},
	} {
		t.Run(tc.target+"/"+tc.kind+"/"+tc.family, func(t *testing.T) {
			err := ValidatePinnedPingTarget(tc.target, tc.kind, tc.family)
			if (err == nil) != tc.ok {
				t.Fatalf("validation error=%v, want success %v", err, tc.ok)
			}
		})
	}
}
