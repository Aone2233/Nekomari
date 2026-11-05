package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Pinned probes remain disconnected from event handling and result upload.
// Their outcomes distinguish non-probes from failures after an address was
// selected; future wire code must preserve that distinction.
type pinnedProbeOutcome struct {
	latencyUS int64
	family    string
	attempted bool
	err       error
}

type pinnedProbeRunner struct {
	resolver pinnedResolver
	icmp     func(string, time.Duration, bool) (int64, bool, error)
}

var (
	errPinnedFamilyDrift  = errors.New("actual connection family differs from pinned family")
	errPinnedNoICMPSocket = errors.New("could not create an ICMP socket")
)

func validPinnedFamily(family string) bool {
	return family == pinnedIPv4 || family == pinnedIPv6
}

func pinnedPortValid(port string) bool {
	n, err := strconv.Atoi(port)
	return err == nil && n >= 1 && n <= 65535 && port == strconv.Itoa(n)
}

func pinnedTargetHost(target string) (string, error) {
	if target == "" || strings.TrimSpace(target) != target {
		return "", errPinnedInvalidTarget
	}
	if host, port, err := net.SplitHostPort(target); err == nil {
		if host == "" || !pinnedPortValid(port) {
			return "", errPinnedInvalidTarget
		}
		return host, nil
	}
	if strings.HasPrefix(target, "[") && strings.HasSuffix(target, "]") {
		addr, err := netip.ParseAddr(target[1 : len(target)-1])
		if err != nil {
			return "", errPinnedInvalidTarget
		}
		return addr.String(), nil
	}
	if addr, err := netip.ParseAddr(target); err == nil {
		return addr.String(), nil
	}
	if strings.ContainsAny(target, ":[]") {
		return "", errPinnedInvalidTarget
	}
	return target, nil
}

func pinnedTCPTarget(target string) (string, error) {
	if host, port, err := net.SplitHostPort(target); err == nil {
		if host == "" || !pinnedPortValid(port) || strings.TrimSpace(target) != target {
			return "", errPinnedInvalidTarget
		}
		return net.JoinHostPort(host, port), nil
	}
	host, err := pinnedTargetHost(target)
	if err != nil {
		return "", err
	}
	return net.JoinHostPort(host, "80"), nil
}

func (r pinnedProbeRunner) probeICMP(ctx context.Context, target, family string, timeout time.Duration) pinnedProbeOutcome {
	if !validPinnedFamily(family) {
		return pinnedProbeOutcome{err: errPinnedInvalidFamily}
	}
	dnsCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	host, err := pinnedTargetHost(target)
	if err != nil {
		return pinnedProbeOutcome{err: err}
	}
	addr, err := r.resolver.resolve(dnsCtx, host, family)
	if err != nil {
		return pinnedProbeOutcome{err: err}
	}
	if err := dnsCtx.Err(); err != nil {
		return pinnedProbeOutcome{err: err}
	}
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return pinnedProbeOutcome{err: context.DeadlineExceeded}
		}
		if remaining < timeout {
			timeout = remaining
		}
	}
	icmp := r.icmp
	if icmp == nil {
		icmp = runICMP
	}
	out := pinnedProbeOutcome{family: addressFamilyOf(addr.String()), attempted: true}
	latency, replied, err := icmp(addr.String(), timeout, true)
	if err != nil {
		if ctx.Err() != nil {
			return pinnedProbeOutcome{err: ctx.Err()}
		}
		if deadline, ok := ctx.Deadline(); ok {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				return pinnedProbeOutcome{err: context.DeadlineExceeded}
			}
			if remaining < timeout {
				timeout = remaining
			}
		}
		latency, replied, err = icmp(addr.String(), timeout, false)
		if err != nil {
			return pinnedProbeOutcome{err: errors.Join(errPinnedNoICMPSocket, err)}
		}
	}
	if err == nil && !replied {
		err = errors.New("no packets received")
	}
	out.latencyUS, out.err = latency, err
	return out
}

func (r pinnedProbeRunner) probeTCP(ctx context.Context, target, family string, timeout time.Duration) pinnedProbeOutcome {
	if !validPinnedFamily(family) {
		return pinnedProbeOutcome{err: errPinnedInvalidFamily}
	}
	address, err := pinnedTCPTarget(target)
	if err != nil {
		return pinnedProbeOutcome{err: err}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	resolver := r.resolver
	dial := resolver.dial
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	var started time.Time
	resolver.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		started = time.Now()
		return dial(ctx, network, address)
	}
	conn, addr, attempted, err := resolver.dialTCPWithAddress(ctx, "tcp", address, family)
	out := pinnedProbeOutcome{attempted: attempted, err: err}
	if attempted {
		out.family = addressFamilyOf(addr.String())
	}
	if err != nil {
		if errors.Is(err, syscall.EAFNOSUPPORT) || errors.Is(err, syscall.EPROTONOSUPPORT) || isPermissionErr(err) || (ctx.Err() != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded)) {
			out.attempted, out.family = false, ""
		}
		return out
	}
	defer conn.Close()
	if remote, ok := conn.RemoteAddr().(*net.TCPAddr); ok && remote != nil {
		out.family = addressFamilyOf(remote.IP.String())
	}
	if out.family != family {
		out.attempted, out.err = false, errPinnedFamilyDrift
		return out
	}
	out.latencyUS = time.Since(started).Microseconds()
	return out
}

// probeHTTP takes a transport only for isolated tests, notably trusted TLS
// fixtures. Production callers must use a fresh transport with proxying off.
func (r pinnedProbeRunner) probeHTTP(ctx context.Context, target, family string, timeout time.Duration, base *http.Transport) pinnedProbeOutcome {
	if !validPinnedFamily(family) {
		return pinnedProbeOutcome{err: errPinnedInvalidFamily}
	}
	if !strings.Contains(target, "://") {
		if addr, err := netip.ParseAddr(target); err == nil && addr.Is6() && !addr.Is4In6() {
			target = "[" + target + "]"
		}
		target = "http://" + target
	}
	u, err := url.Parse(target)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || (u.Port() != "" && !pinnedPortValid(u.Port())) {
		return pinnedProbeOutcome{err: errPinnedInvalidTarget}
	}
	transport := &http.Transport{}
	if base != nil {
		transport = base.Clone()
	}
	// Proxy, custom TLS dialers and keep-alives must not bypass the resolver.
	transport.Proxy = nil
	transport.DialTLSContext = nil
	transport.DialTLS = nil
	transport.DisableKeepAlives = true
	var mu sync.Mutex
	var last pinnedProbeOutcome
	transport.DialContext = func(ctx context.Context, network, target string) (net.Conn, error) {
		conn, addr, attempted, err := r.resolver.dialTCPWithAddress(ctx, network, target, family)
		out := pinnedProbeOutcome{attempted: attempted, err: err}
		if addr.IsValid() {
			out.family = addressFamilyOf(addr.String())
		}
		if err != nil && (errors.Is(err, syscall.EAFNOSUPPORT) || errors.Is(err, syscall.EPROTONOSUPPORT) || isPermissionErr(err) || (ctx.Err() != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded))) {
			out.attempted, out.family = false, ""
		}
		if err == nil {
			if remote, ok := conn.RemoteAddr().(*net.TCPAddr); ok && remote != nil {
				out.family = addressFamilyOf(remote.IP.String())
			}
			if out.family != family {
				_ = conn.Close()
				conn, err = nil, errPinnedFamilyDrift
				out.attempted, out.err = false, err
			}
		}
		mu.Lock()
		last = out
		mu.Unlock()
		return conn, err
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: timeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return pinnedProbeOutcome{err: err}
	}
	start := time.Now()
	resp, err := client.Do(req)
	latency := time.Since(start).Microseconds()
	mu.Lock()
	out := last
	mu.Unlock()
	out.latencyUS, out.err = latency, err
	if resp != nil {
		defer resp.Body.Close()
		if err == nil && (resp.StatusCode < 200 || resp.StatusCode >= 400) {
			out.err = errors.New("http status not ok")
		}
	}
	return out
}

// pinnedProbeCache stores protocol choices, never resolved addresses. The
// family participates in the key, so IPv4 and IPv6 decisions cannot leak.
type pinnedProbeCache struct {
	mu        sync.Mutex
	decisions map[pinnedProbeCacheKey]autoDecision
}

type pinnedProbeCacheKey struct {
	target, family string
}

func (c *pinnedProbeCache) get(target, family string, now time.Time) (autoDecision, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	d, ok := c.decisions[pinnedProbeCacheKey{target, family}]
	return d, ok && now.Sub(d.at) >= 0 && now.Sub(d.at) < autoCacheTTL
}

func (c *pinnedProbeCache) put(target, family string, d autoDecision) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.decisions == nil {
		c.decisions = make(map[pinnedProbeCacheKey]autoDecision)
	}
	for key, existing := range c.decisions {
		if age := d.at.Sub(existing.at); age >= autoCacheTTL || age < 0 {
			delete(c.decisions, key)
		}
	}
	key := pinnedProbeCacheKey{target, family}
	if _, exists := c.decisions[key]; !exists && len(c.decisions) >= 4096 {
		var oldest pinnedProbeCacheKey
		var oldestTime time.Time
		for key, existing := range c.decisions {
			if oldestTime.IsZero() || existing.at.Before(oldestTime) {
				oldest, oldestTime = key, existing.at
			}
		}
		delete(c.decisions, oldest)
	}
	c.decisions[key] = d
}

func (r pinnedProbeRunner) autoTarget(ctx context.Context, target, family string, cache *pinnedProbeCache) (string, string, error) {
	if !validPinnedFamily(family) {
		return "", "", errPinnedInvalidFamily
	}
	if _, port, err := net.SplitHostPort(target); err == nil && pinnedPortValid(port) {
		return "tcp", target, nil
	}
	host, err := pinnedTargetHost(target)
	if err != nil {
		return "", "", err
	}
	if d, ok := cache.get(target, family, time.Now()); ok {
		return d.kind, d.target, nil
	}
	result := r.probeICMP(ctx, target, family, autoProbeTimeout)
	if !result.attempted && !errors.Is(result.err, errPinnedNoICMPSocket) {
		return "", "", result.err
	}
	initialICMPErr := result.err
	kind, chosen := "icmp", target
	if result.err != nil {
		for _, port := range []string{"443", "80"} {
			candidate := net.JoinHostPort(host, port)
			result = r.probeTCP(ctx, candidate, family, autoProbeTimeout)
			if !result.attempted {
				return "", "", result.err
			}
			if result.err == nil {
				kind, chosen = "tcp", candidate
				break
			}
		}
	}
	if kind == "icmp" && errors.Is(initialICMPErr, errPinnedNoICMPSocket) {
		return "", "", initialICMPErr
	}
	cache.put(target, family, autoDecision{kind: kind, target: chosen, at: time.Now()})
	return kind, chosen, nil
}

func (r pinnedProbeRunner) dualTarget(ctx context.Context, target, family string, cache *pinnedProbeCache) (string, error) {
	if !validPinnedFamily(family) {
		return "", errPinnedInvalidFamily
	}
	if _, port, err := net.SplitHostPort(target); err == nil && pinnedPortValid(port) {
		return target, nil
	}
	host, err := pinnedTargetHost(target)
	if err != nil {
		return "", err
	}
	if d, ok := cache.get(target, family, time.Now()); ok && d.kind == "tcp" {
		return d.target, nil
	}
	for _, port := range []string{"443", "80"} {
		candidate := net.JoinHostPort(host, port)
		result := r.probeTCP(ctx, candidate, family, autoProbeTimeout)
		if !result.attempted {
			return "", result.err
		}
		if result.err == nil {
			return candidate, nil
		}
	}
	return net.JoinHostPort(host, "443"), nil
}

type pinnedProbeResult struct {
	kind    string
	role    string
	outcome pinnedProbeOutcome
}

func (r pinnedProbeRunner) probeOnce(ctx context.Context, kind, target, family string, timeout time.Duration) pinnedProbeOutcome {
	switch kind {
	case "icmp":
		return r.probeICMP(ctx, target, family, timeout)
	case "tcp":
		return r.probeTCP(ctx, target, family, timeout)
	case "http":
		return r.probeHTTP(ctx, target, family, timeout, nil)
	default:
		return pinnedProbeOutcome{err: errors.New("unsupported pinned probe type")}
	}
}

func (r pinnedProbeRunner) probeWithRetries(ctx context.Context, kind, target, family string, timeout time.Duration) pinnedProbeOutcome {
	out := r.probeOnce(ctx, kind, target, family, timeout)
	if out.err != nil || out.latencyUS <= highLatencyThreshold {
		return out
	}
	for i := 0; i < pingHighLatencyRetries; i++ {
		out = r.probeOnce(ctx, kind, target, family, timeout)
		if out.err != nil || out.latencyUS <= highLatencyThreshold {
			return out
		}
	}
	return out
}

func (r pinnedProbeRunner) probe(ctx context.Context, kind, target, family string, timeout time.Duration, cache *pinnedProbeCache) []pinnedProbeResult {
	if !validPinnedFamily(family) {
		return []pinnedProbeResult{{kind: kind, outcome: pinnedProbeOutcome{err: errPinnedInvalidFamily}}}
	}
	if cache == nil {
		cache = &pinnedProbeCache{}
	}
	switch kind {
	case "auto":
		resolved, chosen, err := r.autoTarget(ctx, target, family, cache)
		if err != nil {
			return []pinnedProbeResult{{kind: kind, outcome: pinnedProbeOutcome{err: err}}}
		}
		return []pinnedProbeResult{{kind: resolved, outcome: r.probeWithRetries(ctx, resolved, chosen, family, timeout)}}
	case "dual":
		icmp := pinnedProbeResult{kind: "icmp", outcome: r.probeOnce(ctx, "icmp", target, family, timeout)}
		chosen, err := r.dualTarget(ctx, target, family, cache)
		if err != nil {
			return []pinnedProbeResult{icmp, {kind: "tcp", outcome: pinnedProbeOutcome{err: err}}}
		}
		return []pinnedProbeResult{icmp, {kind: "tcp", outcome: r.probeOnce(ctx, "tcp", chosen, family, timeout)}}
	default:
		return []pinnedProbeResult{{kind: kind, outcome: r.probeWithRetries(ctx, kind, target, family, timeout)}}
	}
}

// The family pin applies only to the primary target. The separately injected
// reference runner preserves the legacy reference protocol and resolution.
func (r pinnedProbeRunner) probeWithReference(ctx context.Context, kind, target, reference, family string, timeout time.Duration, cache *pinnedProbeCache, referenceProbe func(string, string, time.Duration) pinnedProbeOutcome) []pinnedProbeResult {
	if !validPinnedFamily(family) {
		return []pinnedProbeResult{{kind: kind, outcome: pinnedProbeOutcome{err: errPinnedInvalidFamily}}}
	}
	results := r.probe(ctx, kind, target, family, timeout, cache)
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return results
	}
	refKind := kind
	if refKind != "icmp" && refKind != "tcp" && refKind != "http" {
		refKind = "icmp"
	}
	if referenceProbe == nil {
		return append(results, pinnedProbeResult{kind: refKind, role: "reference", outcome: pinnedProbeOutcome{err: errors.New("reference probe unavailable")}})
	}
	return append(results, pinnedProbeResult{kind: refKind, role: "reference", outcome: referenceProbe(refKind, reference, timeout)})
}
