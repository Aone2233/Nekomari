package routeprobe

import "testing"

func TestParseRouteEvidence(t *testing.T) {
	result := Parse("traceroute to 1.1.1.1\n 1  10.0.0.1 [AS0] 1 ms\n 2  202.97.1.1 [AS4134] 20 ms\n 3  59.43.1.1 [AS4809] 30 ms\n 4  1.1.1.1 [AS13335] 40 ms", Target{Carrier: "telecom", IP: "1.1.1.1"})
	if !result.Reached || result.Hops != 4 || result.Label != "163 / CN2" {
		t.Fatalf("bad result: %+v", result)
	}
	partial := Parse("traceroute to 1.1.1.1\n 1  *\n 2  *", Target{Carrier: "mobile", IP: "1.1.1.1"})
	if partial.Reached || partial.Label != "" {
		t.Fatal("missing route fabricated evidence")
	}
}
func TestRouteTargetsRequireExplicitPublicAddresses(t *testing.T) {
	for _, input := range []string{"telecom=127.0.0.1", "mobile=192.168.1.1", "unicom=example.com", "telecom=1.1.1.1;id", "bad=1.1.1.1", "telecom=1.1.1.1,telecom=8.8.8.8", "telecom=100.64.0.1", "mobile=192.0.2.1", "mobile=2001:db8::1", "telecom=::ffff:198.51.100.1"} {
		if _, err := ParseTargets(input); err == nil {
			t.Errorf("accepted %q", input)
		}
	}
	if targets, err := ParseTargets("telecom=1.1.1.1,mobile=2606:4700:4700::1111"); err != nil || len(targets) != 2 {
		t.Fatal(targets, err)
	}
}
