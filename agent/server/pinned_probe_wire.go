package server

import (
	"context"
	"errors"
	"strings"

	v2 "github.com/Aone2233/nekomari/agent/protocol/v2"
)

// These helpers are not attached to processV2Event or result upload. The
// current session and one-use, agent-issued freshness ticket must be checked
// atomically when a probe starts; syntactic validation does not authorize it.
func validatePinnedProbeEnvelope(p v2.PingFamilyProbeParams, instance, session string) error {
	if p.TaskID == 0 || !pinnedValidID(p.ConfigGeneration) || !pinnedValidID(p.Instance) || !pinnedValidID(p.Session) ||
		!pinnedValidID(p.DispatchID) || !pinnedValidID(p.FreshnessTicket) ||
		p.Instance != instance || p.Session != session || !validPinnedFamily(p.Family) ||
		p.Target == "" || strings.TrimSpace(p.Target) != p.Target || len(p.Target) > 2048 || len(p.Reference) > 2048 {
		return errors.New("invalid pinned probe envelope")
	}
	switch p.Type {
	case "icmp", "tcp", "http", "auto", "dual":
		return nil
	default:
		return errors.New("invalid pinned probe type")
	}
}

func pinnedProbeDiagnostic(out pinnedProbeOutcome) string {
	switch {
	case out.err == nil:
		return "invalid_outcome"
	case errors.Is(out.err, context.Canceled), errors.Is(out.err, context.DeadlineExceeded):
		return "canceled"
	case errors.Is(out.err, errPinnedDNS):
		return "dns_error"
	case errors.Is(out.err, errPinnedNoAddress):
		return "no_family_address"
	case errors.Is(out.err, errPinnedFamilyMismatch):
		return "family_mismatch"
	case errors.Is(out.err, errPinnedFamilyDrift):
		return "family_drift"
	case errors.Is(out.err, errPinnedNoICMPSocket):
		return "local_socket"
	case errors.Is(out.err, errPinnedInvalidTarget), errors.Is(out.err, errPinnedInvalidFamily):
		return "invalid_target"
	default:
		return "not_probed"
	}
}

// Invalid roles cannot be reinterpreted as primary; an invalid successful
// latency becomes a diagnostic rather than a fabricated loss sample.
func pinnedProbeResultPayload(p v2.PingFamilyProbeParams, result pinnedProbeResult) (v2.PingFamilyResultParams, error) {
	role := result.role
	if role != "primary" && role != "reference" {
		return v2.PingFamilyResultParams{}, errors.New("invalid pinned result role")
	}
	if role == "reference" && strings.TrimSpace(p.Reference) == "" {
		return v2.PingFamilyResultParams{}, errors.New("unexpected pinned reference result")
	}
	wire := v2.PingFamilyResultParams{TaskID: p.TaskID, ConfigGeneration: p.ConfigGeneration, Instance: p.Instance,
		Session: p.Session, DispatchID: p.DispatchID, Role: role, PingType: result.kind}
	out := result.outcome
	if !out.attempted {
		wire.Diagnostic = pinnedProbeDiagnostic(out)
		if !validPinnedResultKind(p.Type, role, result.kind, true) {
			return v2.PingFamilyResultParams{}, errors.New("invalid pinned result protocol")
		}
		return wire, nil
	}
	if !validPinnedResultKind(p.Type, role, result.kind, false) {
		return v2.PingFamilyResultParams{}, errors.New("invalid pinned result protocol")
	}
	if (out.family != pinnedIPv4 && out.family != pinnedIPv6) || (role == "primary" && out.family != p.Family) {
		wire.Diagnostic = "family_drift"
		return wire, nil
	}
	if out.err == nil && (out.latencyUS < 0 || int64(int(out.latencyUS/1000)) != out.latencyUS/1000) {
		wire.Diagnostic = "invalid_outcome"
		return wire, nil
	}
	value, micros := pingResultValues(out.latencyUS, out.err == nil)
	wire.Family, wire.Value, wire.ValueUS = out.family, &value, &micros
	return wire, nil
}

func validPinnedResultKind(taskType, role, kind string, diagnostic bool) bool {
	if role == "reference" {
		switch taskType {
		case "auto", "dual":
			return kind == "icmp"
		case "icmp", "tcp", "http":
			return kind == taskType
		default:
			return false
		}
	}
	switch taskType {
	case "auto":
		return kind == "icmp" || kind == "tcp" || (diagnostic && kind == "auto")
	case "dual":
		return kind == "icmp" || kind == "tcp" || (diagnostic && kind == "dual")
	case "icmp", "tcp", "http":
		return kind == taskType
	default:
		return false
	}
}
