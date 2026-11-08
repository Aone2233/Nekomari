package utils

import (
	"fmt"
	"math"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ValidatePinnedPingInterval prevents overflowing the scheduler's duration.
func ValidatePinnedPingInterval(interval int) error {
	if interval <= 0 || int64(interval) > math.MaxInt64/int64(time.Second) {
		return fmt.Errorf("invalid pinned probe interval")
	}
	return nil
}

// ValidatePinnedPingTarget rejects an invalid explicit family or a literal in
// the wrong family without resolving DNS on the panel. Agents resolve the
// hostname themselves on every probe.
func ValidatePinnedPingTarget(target, kind, family string) error {
	if family != "ipv4" && family != "ipv6" {
		return fmt.Errorf("target family must be ipv4 or ipv6")
	}
	switch kind {
	case "icmp", "tcp", "http", "auto", "dual":
	default:
		return fmt.Errorf("invalid pinned probe type")
	}
	if target == "" || strings.TrimSpace(target) != target || len(target) > 255 || strings.ContainsAny(target, "\x00\r\n\t ") {
		return fmt.Errorf("invalid pinned target")
	}
	var host string
	if kind == "http" {
		address := target
		if !strings.Contains(address, "://") {
			if addr, err := netip.ParseAddr(address); err == nil && addr.Is6() && !addr.Is4In6() {
				address = "[" + address + "]"
			}
			address = "http://" + address
		}
		u, err := url.Parse(address)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || strings.HasSuffix(u.Host, ":") {
			return fmt.Errorf("invalid pinned HTTP target")
		}
		if strings.HasPrefix(u.Host, "[") {
			addr, err := netip.ParseAddr(u.Hostname())
			if err != nil || !addr.Is6() || addr.Zone() != "" {
				return fmt.Errorf("invalid pinned HTTP target")
			}
		}
		if port := u.Port(); port != "" && !validPinnedTargetPort(port) {
			return fmt.Errorf("invalid pinned target port")
		}
		host = u.Hostname()
	} else if h, port, err := net.SplitHostPort(target); err == nil {
		if h == "" || !validPinnedTargetPort(port) {
			return fmt.Errorf("invalid pinned target port")
		}
		if strings.HasPrefix(target, "[") {
			addr, err := netip.ParseAddr(h)
			if err != nil || !addr.Is6() || addr.Zone() != "" {
				return fmt.Errorf("invalid pinned target")
			}
		}
		host = h
	} else if addr, err := netip.ParseAddr(target); err == nil {
		host = addr.String()
	} else if strings.HasPrefix(target, "[") && strings.HasSuffix(target, "]") {
		addr, err := netip.ParseAddr(target[1 : len(target)-1])
		if err != nil {
			return fmt.Errorf("invalid pinned target")
		}
		host = addr.String()
	} else {
		if strings.ContainsAny(target, ":[]/") {
			return fmt.Errorf("invalid pinned target")
		}
		host = target
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		if addr.Zone() != "" || (addr.Unmap().Is4() && family != "ipv4") || (!addr.Unmap().Is4() && family != "ipv6") {
			return fmt.Errorf("target literal does not match the selected family")
		}
	} else if host == "" || strings.ContainsAny(host, ":[]/%?#@") {
		return fmt.Errorf("invalid pinned target hostname")
	}
	return nil
}

func validPinnedTargetPort(port string) bool {
	n, err := strconv.Atoi(port)
	return err == nil && n >= 1 && n <= 65535 && port == strconv.Itoa(n)
}
