package agent

import (
	"testing"

	v2 "github.com/Aone2233/nekomari/protocol/v2"
)

func pinnedResultFixture() (pinnedResultPolicy, v2.PingFamilyResultParams) {
	p := pinnedResultPolicy{
		TaskID: 7, ConfigGeneration: pinnedTestInstanceA, Instance: pinnedTestInstanceB,
		Session: "11111111111111111111111111111111", DispatchID: "22222222222222222222222222222222",
		Family: "ipv6", TaskType: "tcp", Assigned: true, SessionActive: true, DispatchPending: true,
	}
	value, micros := 2, int64(2345)
	r := v2.PingFamilyResultParams{TaskID: p.TaskID, ConfigGeneration: p.ConfigGeneration,
		Instance: p.Instance, Session: p.Session, DispatchID: p.DispatchID, Role: "primary",
		PingType: "tcp", Family: p.Family, Value: &value, ValueUS: &micros}
	return p, r
}

func TestPinnedResultRejectsStaleOrUnassigned(t *testing.T) {
	p, r := pinnedResultFixture()
	if err := validatePinnedResult(p, r); err != nil {
		t.Fatalf("valid result rejected: %v", err)
	}
	for name, change := range map[string]func(*pinnedResultPolicy, *v2.PingFamilyResultParams){
		"deleted task":     func(p *pinnedResultPolicy, _ *v2.PingFamilyResultParams) { p.TaskID = 0 },
		"different task":   func(_ *pinnedResultPolicy, r *v2.PingFamilyResultParams) { r.TaskID++ },
		"unassigned":       func(p *pinnedResultPolicy, _ *v2.PingFamilyResultParams) { p.Assigned = false },
		"disconnected":     func(p *pinnedResultPolicy, _ *v2.PingFamilyResultParams) { p.SessionActive = false },
		"expired dispatch": func(p *pinnedResultPolicy, _ *v2.PingFamilyResultParams) { p.DispatchPending = false },
		"old config":       func(_ *pinnedResultPolicy, r *v2.PingFamilyResultParams) { r.ConfigGeneration = pinnedTestInstanceB },
		"old instance":     func(_ *pinnedResultPolicy, r *v2.PingFamilyResultParams) { r.Instance = pinnedTestInstanceA },
		"old session":      func(_ *pinnedResultPolicy, r *v2.PingFamilyResultParams) { r.Session = pinnedTestInstanceA },
		"other dispatch":   func(_ *pinnedResultPolicy, r *v2.PingFamilyResultParams) { r.DispatchID = pinnedTestInstanceA },
		"malformed ids":    func(p *pinnedResultPolicy, _ *v2.PingFamilyResultParams) { p.DispatchID = "invalid" },
		"invalid family":   func(p *pinnedResultPolicy, _ *v2.PingFamilyResultParams) { p.Family = "" },
	} {
		t.Run(name, func(t *testing.T) {
			p, r := pinnedResultFixture()
			change(&p, &r)
			if err := validatePinnedResult(p, r); err == nil {
				t.Fatal("invalid result accepted")
			}
		})
	}
}

func TestPinnedResultProtocolRoleAndFamily(t *testing.T) {
	for _, tc := range []struct {
		name, taskType, kind, role, family string
		reference, diagnostic, valid       bool
	}{
		{"primary tcp", "tcp", "tcp", "primary", "ipv6", false, false, true},
		{"primary wrong family", "tcp", "tcp", "primary", "ipv4", false, false, false},
		{"primary missing family", "tcp", "tcp", "primary", "", false, false, false},
		{"wrong protocol", "tcp", "icmp", "primary", "ipv6", false, false, false},
		{"invalid role", "tcp", "tcp", "", "ipv6", false, false, false},
		{"no reference", "tcp", "tcp", "reference", "ipv4", false, false, false},
		{"independent reference", "tcp", "tcp", "reference", "ipv4", true, false, true},
		{"auto tcp", "auto", "tcp", "primary", "ipv6", false, false, true},
		{"auto icmp", "auto", "icmp", "primary", "ipv6", false, false, true},
		{"auto diagnostic", "auto", "auto", "primary", "", false, true, true},
		{"auto not a measurement", "auto", "auto", "primary", "ipv6", false, false, false},
		{"dual tcp", "dual", "tcp", "primary", "ipv6", false, false, true},
		{"dual icmp", "dual", "icmp", "primary", "ipv6", false, false, true},
		{"dual diagnostic", "dual", "dual", "primary", "", false, true, true},
		{"dual not a measurement", "dual", "dual", "primary", "ipv6", false, false, false},
		{"dual reference icmp", "dual", "icmp", "reference", "ipv4", true, false, true},
		{"dual reference tcp", "dual", "tcp", "reference", "ipv4", true, false, false},
		{"invalid task type", "invalid", "invalid", "reference", "ipv4", true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, r := pinnedResultFixture()
			p.TaskType, p.ReferenceEnabled = tc.taskType, tc.reference
			r.PingType, r.Role, r.Family = tc.kind, tc.role, tc.family
			if tc.diagnostic {
				r.Diagnostic, r.Value, r.ValueUS = "dns_error", nil, nil
			}
			if err := validatePinnedResult(p, r); (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
		})
	}
}

func TestPinnedResultValuesAndDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name       string
		value      *int
		micros     *int64
		family     string
		diagnostic string
		valid      bool
	}{
		{"ok", intPtr(2), int64Ptr(2345), "ipv6", "", true},
		{"zero", intPtr(0), int64Ptr(999), "ipv6", "", true},
		{"loss", intPtr(-1), int64Ptr(-1), "ipv6", "", true},
		{"loss mismatch", intPtr(-1), int64Ptr(0), "ipv6", "", false},
		{"negative value", intPtr(-2), int64Ptr(-2), "ipv6", "", false},
		{"mismatched success", intPtr(1), int64Ptr(2345), "ipv6", "", false},
		{"missing millis", nil, int64Ptr(2345), "ipv6", "", false},
		{"missing micros", intPtr(2), nil, "ipv6", "", false},
		{"diagnostic", nil, nil, "", "no_family_address", true},
		{"diagnostic and value", intPtr(-1), nil, "", "dns_error", false},
		{"diagnostic and micros", nil, int64Ptr(-1), "", "dns_error", false},
		{"diagnostic and family", nil, nil, "ipv6", "dns_error", false},
		{"unknown diagnostic", nil, nil, "", "unexpected", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, r := pinnedResultFixture()
			r.Value, r.ValueUS, r.Family, r.Diagnostic = tc.value, tc.micros, tc.family, tc.diagnostic
			if err := validatePinnedResult(p, r); (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
		})
	}
}

func intPtr(n int) *int       { return &n }
func int64Ptr(n int64) *int64 { return &n }
