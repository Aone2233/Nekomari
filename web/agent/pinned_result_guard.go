package agent

import (
	"errors"

	v2 "github.com/Aone2233/nekomari/protocol/v2"
)

// This isolated guard is not connected to metric intake. Its inputs must be
// loaded under task/assignment, session and dispatch mutation fences in the
// final integration. A boolean is not evidence without those fences.
type pinnedResultPolicy struct {
	TaskID           uint
	ConfigGeneration string
	Instance         string
	Session          string
	DispatchID       string
	Family           string
	TaskType         string
	Assigned         bool
	SessionActive    bool
	DispatchPending  bool
	ReferenceEnabled bool
}

func validatePinnedResult(p pinnedResultPolicy, r v2.PingFamilyResultParams) error {
	if !p.Assigned || !p.SessionActive || !p.DispatchPending || p.TaskID == 0 || r.TaskID != p.TaskID ||
		!validPinnedWSID(p.ConfigGeneration) || !validPinnedWSID(p.Instance) || !validPinnedWSID(p.Session) || !validPinnedWSID(p.DispatchID) ||
		r.ConfigGeneration != p.ConfigGeneration || r.Instance != p.Instance || r.Session != p.Session || r.DispatchID != p.DispatchID ||
		(p.Family != "ipv4" && p.Family != "ipv6") {
		return errors.New("stale or unassigned pinned result")
	}
	if r.Role != "primary" && (r.Role != "reference" || !p.ReferenceEnabled) {
		return errors.New("invalid pinned result role")
	}
	validKind := false
	if r.Role == "reference" {
		switch p.TaskType {
		case "auto", "dual":
			validKind = r.PingType == "icmp"
		case "icmp", "tcp", "http":
			validKind = r.PingType == p.TaskType
		}
	} else {
		switch p.TaskType {
		case "auto":
			validKind = r.PingType == "icmp" || r.PingType == "tcp" || r.PingType == "auto"
		case "dual":
			validKind = r.PingType == "icmp" || r.PingType == "tcp" || (r.PingType == "dual" && r.Diagnostic != "")
		case "icmp", "tcp", "http":
			validKind = r.PingType == p.TaskType
		}
	}
	if !validKind {
		return errors.New("invalid pinned result protocol")
	}
	if r.Diagnostic != "" {
		switch r.Diagnostic {
		case "dns_error", "no_family_address", "family_mismatch", "family_drift", "local_socket", "invalid_target", "canceled", "not_probed", "invalid_outcome":
		default:
			return errors.New("invalid pinned diagnostic")
		}
		if r.Value != nil || r.ValueUS != nil || r.Family != "" {
			return errors.New("diagnostic must not carry a measurement")
		}
		return nil
	}
	if r.PingType == "auto" || r.Value == nil || r.ValueUS == nil || (r.Family != "ipv4" && r.Family != "ipv6") || (r.Role == "primary" && r.Family != p.Family) {
		return errors.New("invalid pinned measurement")
	}
	if (*r.Value == -1 && *r.ValueUS != -1) || (*r.Value >= 0 && (*r.ValueUS < 0 || int64(*r.Value) != *r.ValueUS/1000)) || *r.Value < -1 {
		return errors.New("inconsistent pinned values")
	}
	return nil
}
