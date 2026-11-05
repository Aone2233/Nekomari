package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
)

// This is an isolated building block, not a dispatched task. A separate wire
// method, capability gate and result validation are required before use.
type pinnedResolver struct {
	lookup func(context.Context, string) ([]netip.Addr, error)
	dial   func(context.Context, string, string) (net.Conn, error)
}

const (
	pinnedIPv4 = "ipv4"
	pinnedIPv6 = "ipv6"
)

var (
	errPinnedInvalidFamily  = errors.New("invalid pinned address family")
	errPinnedInvalidTarget  = errors.New("invalid pinned target")
	errPinnedFamilyMismatch = errors.New("literal does not match pinned address family")
	errPinnedDNS            = errors.New("pinned DNS lookup failed")
	errPinnedNoAddress      = errors.New("no address in pinned family")
	errPinnedNetwork        = errors.New("network does not match pinned address family")
)

func pinnedAddressMatches(addr netip.Addr, family string) bool {
	if !addr.IsValid() {
		return false
	}
	addr = addr.Unmap()
	return (family == pinnedIPv4 && addr.Is4()) || (family == pinnedIPv6 && addr.Is6())
}

// resolve chooses an address on every invocation. In particular, a cached
// protocol decision must never cache an address or allow fallback to another
// family after DNS changes.
func (r pinnedResolver) resolve(ctx context.Context, host, family string) (netip.Addr, error) {
	if family != pinnedIPv4 && family != pinnedIPv6 {
		return netip.Addr{}, errPinnedInvalidFamily
	}
	if host == "" || strings.TrimSpace(host) != host {
		return netip.Addr{}, errPinnedInvalidTarget
	}
	bracketed := strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]")
	if bracketed {
		host = host[1 : len(host)-1]
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		if !pinnedAddressMatches(addr, family) {
			return netip.Addr{}, errPinnedFamilyMismatch
		}
		return addr.Unmap(), nil
	}
	if bracketed || host == "" || strings.ContainsAny(host, "[]:") {
		return netip.Addr{}, errPinnedInvalidTarget
	}

	lookup := r.lookup
	if lookup == nil {
		lookup = func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		}
	}
	addrs, err := lookup(ctx, host)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("%w: %w", errPinnedDNS, err)
	}
	for _, addr := range addrs {
		if pinnedAddressMatches(addr, family) {
			return addr.Unmap(), nil
		}
	}
	return netip.Addr{}, errPinnedNoAddress
}

// dialTCP resolves before dialing and constrains the network as a second
// boundary. It can be used as an HTTP Transport's DialContext so every
// redirect is resolved under the same policy, without replacing the URL host.
func (r pinnedResolver) dialTCP(ctx context.Context, network, target, family string) (net.Conn, error) {
	conn, _, _, err := r.dialTCPWithAddress(ctx, network, target, family)
	return conn, err
}

// dialTCPWithAddress also reports the chosen address and whether a dial was
// actually attempted. Callers must not interpret an undispatched probe as loss.
func (r pinnedResolver) dialTCPWithAddress(ctx context.Context, network, target, family string) (net.Conn, netip.Addr, bool, error) {
	if family != pinnedIPv4 && family != pinnedIPv6 {
		return nil, netip.Addr{}, false, errPinnedInvalidFamily
	}
	wantNetwork := "tcp4"
	if family == pinnedIPv6 {
		wantNetwork = "tcp6"
	}
	if network != "tcp" && network != wantNetwork {
		return nil, netip.Addr{}, false, errPinnedNetwork
	}
	host, port, err := net.SplitHostPort(target)
	if err != nil || host == "" || port == "" {
		return nil, netip.Addr{}, false, errPinnedInvalidTarget
	}
	addr, err := r.resolve(ctx, host, family)
	if err != nil {
		return nil, netip.Addr{}, false, err
	}
	if err := ctx.Err(); err != nil {
		return nil, addr, false, err
	}
	dial := r.dial
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	conn, err := dial(ctx, wantNetwork, net.JoinHostPort(addr.String(), port))
	return conn, addr, true, err
}
