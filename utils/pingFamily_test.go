package utils

import (
	"testing"

	"github.com/Aone2233/nekomari/database/models"
)

func TestPingTargetHost(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"1.1.1.1:443", "1.1.1.1"},
		{"1.1.1.1", "1.1.1.1"},
		{"[2001:db8::1]:443", "2001:db8::1"},
		{"[2001:db8::1]", "2001:db8::1"},
		// A bare IPv6 literal must not be mistaken for host:port -- the colons are
		// part of the address, and cutting at the last one would leave "2001:db8:".
		{"2001:db8::1", "2001:db8::1"},
		{"2001:250:4:100::2", "2001:250:4:100::2"},
		{"www.example.com:443", "www.example.com"},
		{"www.example.com", "www.example.com"},
		{"ha-cm-v4.ip.zstaticcdn.com:80", "ha-cm-v4.ip.zstaticcdn.com"},
		{"  1.1.1.1:443  ", "1.1.1.1"},
		{"", ""},
	}
	for _, c := range cases {
		if got := pingTargetHost(c.in); got != c.want {
			t.Errorf("pingTargetHost(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPingTargetFamily(t *testing.T) {
	cases := []struct {
		in   string
		want targetAddressFamily
	}{
		{"1.51.3.134:443", familyIPv4},
		{"1.1.1.1:443", familyIPv4},
		{"14.17.70.70", familyIPv4},
		// IPv4-mapped IPv6 is an IPv4 target in practice: a v6-only node reaching it
		// still needs a v4 route, so it must be classified as IPv4.
		{"::ffff:1.51.3.134", familyIPv4},
		{"[2001:db8::1]:443", familyIPv6},
		{"2001:250:4:100::2", familyIPv6},
		{"2604:abc0:50::11:601e", familyIPv6},
		// Hostnames resolve per-node and may be dual-stack, so they are not filtered.
		{"www.cloudflare.com:443", familyAny},
		{"ha-cm-v4.ip.zstaticcdn.com:80", familyAny},
		{"", familyAny},
	}
	for _, c := range cases {
		if got := pingTargetFamily(c.in); got != c.want {
			t.Errorf("pingTargetFamily(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestClientCanReachFamily(t *testing.T) {
	v4only := models.Client{UUID: "a", Name: "v4-only", IPv4: "1.2.3.4"}
	v6only := models.Client{UUID: "b", Name: "v6-only", IPv6: "2001:db8::1"}
	dual := models.Client{UUID: "c", Name: "dual", IPv4: "1.2.3.4", IPv6: "2001:db8::1"}
	unknown := models.Client{UUID: "d", Name: "new"}
	blank := models.Client{UUID: "e", Name: "blank", IPv4: "  ", IPv6: "\t"}

	cases := []struct {
		name   string
		client models.Client
		family targetAddressFamily
		want   bool
	}{
		{"v4 node / v4 target", v4only, familyIPv4, true},
		{"v4 node / v6 target", v4only, familyIPv6, false},
		{"v6 node / v4 target", v6only, familyIPv4, false},
		{"v6 node / v6 target", v6only, familyIPv6, true},
		{"dual / v4 target", dual, familyIPv4, true},
		{"dual / v6 target", dual, familyIPv6, true},
		// Not knowing is not the same as knowing it cannot: a node that has not
		// reported yet must not be silently excluded from monitoring.
		{"no addresses / v4 target", unknown, familyIPv4, true},
		{"no addresses / v6 target", unknown, familyIPv6, true},
		{"whitespace addresses / v4 target", blank, familyIPv4, true},
		{"hostname target / v4 node", v4only, familyAny, true},
		{"hostname target / v6 node", v6only, familyAny, true},
	}
	for _, c := range cases {
		if got := clientCanReachFamily(c.client, c.family); got != c.want {
			t.Errorf("%s: clientCanReachFamily = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestFilterClientsByTargetFamily(t *testing.T) {
	byUUID := map[string]models.Client{
		"v4":   {UUID: "v4", Name: "v4-only", IPv4: "1.2.3.4"},
		"v6":   {UUID: "v6", Name: "v6-only", IPv6: "2001:db8::1"},
		"dual": {UUID: "dual", Name: "dual", IPv4: "1.2.3.4", IPv6: "2001:db8::1"},
	}

	t.Run("ipv4 target keeps v4-capable and drops v6-only", func(t *testing.T) {
		keep, skipped := filterClientsByTargetFamily(
			[]string{"v4", "v6", "dual"}, byUUID, familyIPv4)
		if len(keep) != 2 || keep[0] != "v4" || keep[1] != "dual" {
			t.Fatalf("keep = %v, want [v4 dual]", keep)
		}
		if len(skipped) != 1 || skipped[0] != "v6" {
			t.Fatalf("skipped = %v, want [v6]", skipped)
		}
	})

	t.Run("ipv6 target drops v4-only", func(t *testing.T) {
		keep, skipped := filterClientsByTargetFamily(
			[]string{"v4", "v6", "dual"}, byUUID, familyIPv6)
		if len(keep) != 2 || keep[0] != "v6" || keep[1] != "dual" {
			t.Fatalf("keep = %v, want [v6 dual]", keep)
		}
		if len(skipped) != 1 || skipped[0] != "v4" {
			t.Fatalf("skipped = %v, want [v4]", skipped)
		}
	})

	t.Run("hostname target filters nothing", func(t *testing.T) {
		in := []string{"v4", "v6", "dual"}
		keep, skipped := filterClientsByTargetFamily(in, byUUID, familyAny)
		if len(keep) != 3 || len(skipped) != 0 {
			t.Fatalf("keep = %v, skipped = %v; want all kept, none skipped", keep, skipped)
		}
	})

	t.Run("references to removed nodes are dropped", func(t *testing.T) {
		keep, skipped := filterClientsByTargetFamily(
			[]string{"v4", "gone"}, byUUID, familyIPv4)
		if len(keep) != 1 || keep[0] != "v4" {
			t.Fatalf("keep = %v, want [v4]", keep)
		}
		if len(skipped) != 1 || skipped[0] != "gone" {
			t.Fatalf("skipped = %v, want [gone]", skipped)
		}
	})

	t.Run("order of the surviving clients is preserved", func(t *testing.T) {
		keep, _ := filterClientsByTargetFamily(
			[]string{"dual", "v4"}, byUUID, familyIPv4)
		if len(keep) != 2 || keep[0] != "dual" || keep[1] != "v4" {
			t.Fatalf("keep = %v, want [dual v4]", keep)
		}
	})

	t.Run("empty input yields empty output", func(t *testing.T) {
		keep, skipped := filterClientsByTargetFamily(nil, byUUID, familyIPv4)
		if len(keep) != 0 || len(skipped) != 0 {
			t.Fatalf("keep = %v, skipped = %v; want both empty", keep, skipped)
		}
	})
}
