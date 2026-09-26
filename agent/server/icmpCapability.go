package server

import (
	"sync"
	"time"
)

// What this host can actually do about ICMP, reported to the panel so that "this
// node cannot send an ICMP probe" stops looking like "the target is not
// answering". The panel side of that is roadmap E2; this is the fact itself.
//
// Three values, and the empty one is the important case:
//
//	""      the agent did not report it (an older agent, or a report that has not
//	        arrived yet). The panel must show "unknown", never "unavailable" --
//	        treating silence as a denial would mark a working node broken.
//	"raw"   a raw socket opens; the path every pre-v0.1.27 node has always used.
//	"ping"  only the kernel's unprivileged ping socket opens, which is the v0.1.27
//	        fallback and measures echo latency just as well.
//	"none"  neither opens, so an ICMP-typed task on this node cannot work and its
//	        loss figures are the tool's limitation, not the target's behaviour.
//
// The probe is an actual socket open, not an inspection of capabilities or of
// `net.ipv4.ping_group_range`: the capability can be present in CapEff and still
// be unusable (see the NoNewPrivileges/PrivateTmp trap in docs/AGENT-FOOTPRINT.md),
// and the sysctl needs the group arithmetic to be interpreted correctly. Opening
// the socket is the thing itself.
const (
	ICMPCapabilityRaw  = "raw"
	ICMPCapabilityPing = "ping"
	ICMPCapabilityNone = "none"
)

// icmpProbeTimeout bounds the socket open. It is short on purpose: this runs once
// at startup and the answer is about the socket, not about any target. A probe
// that needs a network round trip is not being asked here.
const icmpProbeTimeout = 300 * time.Millisecond

var (
	icmpCapabilityOnce   sync.Once
	icmpCapabilityCached string
)

// ICMPCapability returns the cached result of probing this host's ICMP sockets.
//
// Cached because the answer cannot change without a restart of the process: the
// capability and the sysctl are both fixed for the process's lifetime, and this
// is called on every basic-info upload.
func ICMPCapability() string {
	icmpCapabilityOnce.Do(func() {
		icmpCapabilityCached = probeICMPCapability()
	})
	return icmpCapabilityCached
}

// probeICMPCapability tries the two socket kinds in the same order the probe path
// does -- raw first, because that is the path existing nodes' history comes from.
//
// "Trying" means opening the socket and sending one echo to an address that is
// not expected to answer. That is deliberate: a socket that opens but cannot send
// is not a capability, and with a 300 ms timeout the round trip costs about that
// much once per process. `replied` is irrelevant here -- an unanswered echo is a
// success for this question.
func probeICMPCapability() string {
	if _, _, err := runICMP("127.0.0.1", icmpProbeTimeout, true); err == nil {
		return ICMPCapabilityRaw
	}
	if _, _, err := runICMP("127.0.0.1", icmpProbeTimeout, false); err == nil {
		return ICMPCapabilityPing
	}
	return ICMPCapabilityNone
}

// describeICMPCapability is the operator-facing sentence for a capability value.
// Used by the agent's startup log, so the journal says what the panel will show
// instead of leaving the two to disagree.
func describeICMPCapability(capability string) string {
	switch capability {
	case ICMPCapabilityRaw:
		return "ICMP: raw socket available"
	case ICMPCapabilityPing:
		return "ICMP: raw socket refused, unprivileged ping socket available (fallback in use)"
	case ICMPCapabilityNone:
		return "ICMP: neither a raw socket nor an unprivileged ping socket opens; " +
			"ICMP-typed tasks on this node will report loss that is not the target's"
	default:
		return "ICMP: capability not reported"
	}
}

// ICMPCapabilityLogLine is what the agent writes once at startup, so the journal
// and the panel agree about this host.
func ICMPCapabilityLogLine() string {
	return describeICMPCapability(ICMPCapability())
}
