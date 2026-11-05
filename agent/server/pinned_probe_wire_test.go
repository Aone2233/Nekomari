package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	v2 "github.com/Aone2233/nekomari/agent/protocol/v2"
)

func pinnedWireFixture() v2.PingFamilyProbeParams {
	return v2.PingFamilyProbeParams{TaskID: 7, Type: "tcp", Target: "target.test:80", Family: pinnedIPv6,
		ConfigGeneration: "0123456789abcdef0123456789abcdef", Instance: "11111111111111111111111111111111",
		Session: "22222222222222222222222222222222", DispatchID: "33333333333333333333333333333333", FreshnessTicket: "44444444444444444444444444444444"}
}

func TestPinnedWireRejectsMalformedEnvelopes(t *testing.T) {
	valid := pinnedWireFixture()
	if err := validatePinnedProbeEnvelope(valid, valid.Instance, valid.Session); err != nil {
		t.Fatal(err)
	}
	for name, modify := range map[string]func(*v2.PingFamilyProbeParams){
		"zero task":  func(p *v2.PingFamilyProbeParams) { p.TaskID = 0 },
		"family":     func(p *v2.PingFamilyProbeParams) { p.Family = "" },
		"generation": func(p *v2.PingFamilyProbeParams) { p.ConfigGeneration = "" },
		"instance":   func(p *v2.PingFamilyProbeParams) { p.Instance = strings.Repeat("a", 32) },
		"session":    func(p *v2.PingFamilyProbeParams) { p.Session = strings.Repeat("b", 32) },
		"dispatch":   func(p *v2.PingFamilyProbeParams) { p.DispatchID = "" },
		"ticket":     func(p *v2.PingFamilyProbeParams) { p.FreshnessTicket = "invalid" },
		"type":       func(p *v2.PingFamilyProbeParams) { p.Type = "unknown" },
		"whitespace": func(p *v2.PingFamilyProbeParams) { p.Target = " target.test" },
		"oversized":  func(p *v2.PingFamilyProbeParams) { p.Target = strings.Repeat("x", 2049) },
	} {
		t.Run(name, func(t *testing.T) {
			p := valid
			modify(&p)
			if err := validatePinnedProbeEnvelope(p, valid.Instance, valid.Session); err == nil {
				t.Fatal("invalid envelope accepted")
			}
		})
	}
}

func TestPinnedWireNonProbesNeverCarryLossValues(t *testing.T) {
	p := pinnedWireFixture()
	for _, tc := range []struct {
		err  error
		want string
	}{
		{errPinnedDNS, "dns_error"},
		{errPinnedNoAddress, "no_family_address"},
		{errPinnedNoICMPSocket, "local_socket"},
		{context.Canceled, "canceled"},
		{errors.New("local error"), "not_probed"},
	} {
		wire, err := pinnedProbeResultPayload(p, pinnedProbeResult{role: "primary", kind: "tcp", outcome: pinnedProbeOutcome{err: tc.err}})
		if err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(wire)
		if err != nil {
			t.Fatal(err)
		}
		if wire.Diagnostic != tc.want || wire.Value != nil || wire.ValueUS != nil || strings.Contains(string(b), `"value"`) {
			t.Fatalf("non-probe encoded as loss: %s", b)
		}
	}
	for _, tc := range []struct {
		name string
		out  pinnedProbeOutcome
		want int
	}{
		{"success", pinnedProbeOutcome{attempted: true, family: pinnedIPv6, latencyUS: 2345}, 2},
		{"refused", pinnedProbeOutcome{attempted: true, family: pinnedIPv6, err: errors.New("refused")}, -1},
	} {
		wire, err := pinnedProbeResultPayload(p, pinnedProbeResult{role: "primary", kind: "tcp", outcome: tc.out})
		if err != nil {
			t.Fatal(err)
		}
		if wire.Value == nil || *wire.Value != tc.want || wire.ValueUS == nil || wire.Diagnostic != "" || wire.Role != "primary" {
			t.Fatalf("%s: %+v", tc.name, wire)
		}
	}
	wrong, err := pinnedProbeResultPayload(p, pinnedProbeResult{role: "primary", kind: "tcp", outcome: pinnedProbeOutcome{attempted: true, family: pinnedIPv4}})
	if err != nil {
		t.Fatal(err)
	}
	if wrong.Diagnostic != "family_drift" || wrong.Value != nil {
		t.Fatal("wrong-family sample accepted")
	}
	p.Reference = "gateway.test"
	ref, err := pinnedProbeResultPayload(p, pinnedProbeResult{role: "reference", kind: "tcp", outcome: pinnedProbeOutcome{attempted: true, family: pinnedIPv4, latencyUS: 100}})
	if err != nil {
		t.Fatal(err)
	}
	if ref.Role != "reference" || ref.Value == nil || ref.Family != pinnedIPv4 {
		t.Fatal("reference family was incorrectly pinned")
	}
}

func TestPinnedWireRejectsUnexpectedReferenceAndProtocol(t *testing.T) {
	p := pinnedWireFixture()
	result := pinnedProbeResult{role: "reference", kind: "tcp", outcome: pinnedProbeOutcome{
		attempted: true, family: pinnedIPv6, latencyUS: 10,
	}}
	if _, err := pinnedProbeResultPayload(p, result); err == nil {
		t.Fatal("reference without a target was accepted")
	}
	p.Reference = "gateway.test"
	result.kind = "icmp"
	if _, err := pinnedProbeResultPayload(p, result); err == nil {
		t.Fatal("incorrect reference protocol was accepted")
	}
	result.role, result.kind = "primary", "auto"
	if _, err := pinnedProbeResultPayload(p, result); err == nil {
		t.Fatal("incorrect primary protocol was accepted")
	}
	p.Type = "auto"
	if _, err := pinnedProbeResultPayload(p, result); err == nil {
		t.Fatal("auto measurement without an actual protocol was accepted")
	}
	result.outcome = pinnedProbeOutcome{err: errPinnedDNS}
	if _, err := pinnedProbeResultPayload(p, result); err != nil {
		t.Fatalf("auto pre-probe diagnostic rejected: %v", err)
	}
	p.Type, result.kind = "dual", "dual"
	if _, err := pinnedProbeResultPayload(p, result); err != nil {
		t.Fatalf("dual pre-probe diagnostic rejected: %v", err)
	}
}

func TestPinnedWireRejectsInvalidOutcomeAndRole(t *testing.T) {
	p := pinnedWireFixture()
	result := pinnedProbeResult{role: "primary", kind: "tcp", outcome: pinnedProbeOutcome{
		attempted: true, family: pinnedIPv6, latencyUS: -1,
	}}
	wire, err := pinnedProbeResultPayload(p, result)
	if err != nil || wire.Diagnostic != "invalid_outcome" || wire.Value != nil || wire.ValueUS != nil || wire.Family != "" {
		t.Fatalf("negative successful latency became a measurement: %+v, %v", wire, err)
	}
	for _, role := range []string{"", "Primary", "unknown"} {
		result.role = role
		if _, err := pinnedProbeResultPayload(p, result); err == nil {
			t.Fatalf("invalid role %q accepted", role)
		}
	}
}
