// Package routeprobe performs opt-in, low-frequency return-path observations.
package routeprobe

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Target struct {
	Carrier string
	IP      string
}
type Result struct {
	Carrier string `json:"carrier"`
	Target  string `json:"target"`
	Label   string `json:"label"`
	Reached bool   `json:"reached"`
	Hops    int    `json:"hops"`
	Error   string `json:"error,omitempty"`
}

var reservedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("100::/64"), netip.MustParsePrefix("2001:db8::/32"),
}

func publicAddress(ip net.IP) bool {
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	address = address.Unmap()
	if !address.IsGlobalUnicast() || address.IsPrivate() {
		return false
	}
	for _, prefix := range reservedPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

// Accept only numeric public addresses: no shell, hostnames, or implicit targets.
func ParseTargets(input string) ([]Target, error) {
	if strings.TrimSpace(input) == "" {
		return nil, nil
	}
	var targets []Target
	seen := map[string]bool{}
	for _, entry := range strings.Split(input, ",") {
		parts := strings.SplitN(strings.TrimSpace(entry), "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("expected carrier=IP")
		}
		carrier := parts[0]
		ip := net.ParseIP(parts[1])
		if carrier != "telecom" && carrier != "unicom" && carrier != "mobile" {
			return nil, fmt.Errorf("unknown carrier %q", carrier)
		}
		if seen[carrier] || !publicAddress(ip) {
			return nil, fmt.Errorf("target must be a unique carrier and public numeric IP")
		}
		seen[carrier] = true
		targets = append(targets, Target{Carrier: carrier, IP: ip.String()})
	}
	return targets, nil
}

var hopPattern = regexp.MustCompile(`(?m)^\s*(\d+)\s+(.+)$`)
var asnPattern = regexp.MustCompile(`(?i)\bAS(\d+)\b`)
var routeLabels = map[string]string{"4134": "163", "4809": "CN2", "4837": "169", "9929": "9929", "58807": "CMIN2", "58453": "CMI", "9808": "CMNET"}

func Parse(output string, target Target) Result {
	result := Result{Carrier: target.Carrier, Target: target.IP}
	labels := []string{}
	seen := map[string]bool{}
	for _, match := range hopPattern.FindAllStringSubmatch(output, -1) {
		hop, _ := strconv.Atoi(match[1])
		if hop > result.Hops {
			result.Hops = hop
		}
		for _, token := range strings.Fields(match[2]) {
			if ip := net.ParseIP(strings.Trim(token, "()")); ip != nil && ip.Equal(net.ParseIP(target.IP)) {
				result.Reached = true
			}
		}
		for _, asn := range asnPattern.FindAllStringSubmatch(match[2], -1) {
			if label := routeLabels[asn[1]]; label != "" && !seen[label] {
				seen[label] = true
				labels = append(labels, label)
			}
		}
	}
	result.Label = strings.Join(labels, " / ")
	return result
}

type boundedOutput struct{ bytes.Buffer }

func (b *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	if remain := 32*1024 - b.Len(); remain > 0 {
		if len(p) > remain {
			p = p[:remain]
		}
		_, _ = b.Buffer.Write(p)
	}
	return n, nil
}

func Probe(ctx context.Context, targets []Target) []Result {
	results := make([]Result, 0, len(targets))
	for _, target := range targets {
		budget, cancel := context.WithTimeout(ctx, 45*time.Second)
		family := "-4"
		if net.ParseIP(target.IP).To4() == nil {
			family = "-6"
		}
		cmd := exec.CommandContext(budget, "traceroute", family, "-I", "-n", "-A", "-m", "30", "-q", "1", "-w", "1", target.IP)
		cmd.WaitDelay = time.Second
		var output boundedOutput
		cmd.Stdout = &output
		cmd.Stderr = io.Discard
		err := cmd.Run()
		result := Parse(output.String(), target)
		if err != nil {
			result.Error = "traceroute failed or timed out; install traceroute with ASN support"
		}
		cancel()
		results = append(results, result)
	}
	return results
}
