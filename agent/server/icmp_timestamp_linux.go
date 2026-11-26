//go:build linux && (amd64 || arm64)

package server

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"os"
	"time"
	"unsafe"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
	"golang.org/x/sys/unix"
)

// timestampProbeError indicates that an echo was sent but its timing could not
// be established. Retrying through another socket would send a second probe.
type timestampProbeError struct{ err error }

func (e *timestampProbeError) Error() string { return "ICMP timestamp unavailable: " + e.err.Error() }
func (e *timestampProbeError) Unwrap() error { return e.err }

// Linux software timestamps use CLOCK_REALTIME. Reject observations when the
// wall/monotonic offset changes materially during a probe, or when the kernel
// timestamp interval lies outside its enclosing monotonic interval.
func checkedICMPInterval(start, end time.Time, tx, rx int64) (int64, error) {
	return validateICMPInterval(start.UnixNano(), end.UnixNano(), end.Sub(start), tx, rx)
}

func validateICMPInterval(start, end int64, monotonic time.Duration, tx, rx int64) (int64, error) {
	wall := end - start
	delta := time.Duration(wall) - monotonic
	if delta < 0 {
		delta = -delta
	}
	if monotonic < 0 || delta > time.Millisecond || tx < start-int64(time.Millisecond) || rx > end+int64(time.Millisecond) || rx < tx || rx-tx > int64(monotonic)+int64(2*time.Millisecond) {
		return 0, errors.New("clock changed or timestamp interval invalid")
	}
	return (rx - tx) / int64(time.Microsecond), nil
}

func softwareICMPTimestamp(oob []byte) (int64, bool) {
	msgs, err := unix.ParseSocketControlMessage(oob)
	if err != nil {
		return 0, false
	}
	for _, m := range msgs {
		if m.Header.Level == unix.SOL_SOCKET && m.Header.Type == unix.SCM_TIMESTAMPING && len(m.Data) >= int(unsafe.Sizeof(unix.Timespec{})) {
			// Copy to aligned storage; ancillary payload is not guaranteed aligned.
			var ts unix.Timespec
			dst := unsafe.Slice((*byte)(unsafe.Pointer(&ts)), int(unsafe.Sizeof(ts)))
			copy(dst, m.Data)
			if ts.Sec < 0 || ts.Nsec < 0 || ts.Nsec >= 1e9 || (ts.Sec == 0 && ts.Nsec == 0) {
				return 0, false
			}
			return ts.Nano(), true
		}
	}
	return 0, false
}

func icmpTxTimestampKind(oob []byte) (uint32, bool) {
	msgs, err := unix.ParseSocketControlMessage(oob)
	if err != nil {
		return 0, false
	}
	for _, m := range msgs {
		if (m.Header.Level == unix.SOL_IP && m.Header.Type == unix.IP_RECVERR || m.Header.Level == unix.SOL_IPV6 && m.Header.Type == unix.IPV6_RECVERR) && len(m.Data) >= int(unsafe.Sizeof(unix.SockExtendedErr{})) {
			var info unix.SockExtendedErr
			copy(unsafe.Slice((*byte)(unsafe.Pointer(&info)), int(unsafe.Sizeof(info))), m.Data)
			return info.Info, info.Origin == unix.SO_EE_ORIGIN_TIMESTAMPING && info.Errno == uint32(unix.ENOMSG)
		}
	}
	return 0, false
}

// preciseICMP returns handled=false only before sending, permitting the legacy
// implementation on kernels that do not support timestamping. Once sent, a
// reply with a missing timestamp is unknown and must not be retried. No reply
// by the deadline remains loss; a qdisc drop may also suppress TX timestamps.
func preciseICMP(ctx context.Context, ip string, timeout time.Duration, privileged bool) (latency int64, replied bool, err error, handled bool) {
	if err := ctx.Err(); err != nil {
		return -1, false, err, true
	}
	addr := net.ParseIP(ip)
	if addr == nil {
		return 0, false, nil, false
	}
	family, proto, network := unix.AF_INET, unix.IPPROTO_ICMP, 1
	var dst unix.Sockaddr
	var echoType icmp.Type = ipv4.ICMPTypeEcho
	if v4 := addr.To4(); v4 != nil {
		var a [4]byte
		copy(a[:], v4)
		dst = &unix.SockaddrInet4{Addr: a}
	} else {
		family, proto, network = unix.AF_INET6, unix.IPPROTO_ICMPV6, 58
		var a [16]byte
		copy(a[:], addr.To16())
		dst = &unix.SockaddrInet6{Addr: a}
		echoType = ipv6.ICMPTypeEchoRequest
	}
	kind := unix.SOCK_DGRAM
	if privileged {
		kind = unix.SOCK_RAW
	}
	fd, e := unix.Socket(family, kind|unix.SOCK_NONBLOCK|unix.SOCK_CLOEXEC, proto)
	if e != nil {
		return 0, false, nil, false
	}
	defer unix.Close(fd)
	opts := unix.SOF_TIMESTAMPING_TX_SOFTWARE | unix.SOF_TIMESTAMPING_RX_SOFTWARE | unix.SOF_TIMESTAMPING_SOFTWARE | unix.SOF_TIMESTAMPING_OPT_TSONLY
	if e = unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_TIMESTAMPING, opts); e != nil {
		return 0, false, nil, false
	}
	var nonce [16]byte
	if _, e = rand.Read(nonce[:]); e != nil {
		return 0, false, nil, false
	}
	msg := icmp.Message{Type: echoType, Body: &icmp.Echo{ID: os.Getpid() & 65535, Seq: 1, Data: nonce[:]}}
	packet, e := msg.Marshal(nil)
	if e != nil {
		return 0, false, nil, false
	}
	if err := ctx.Err(); err != nil {
		return -1, false, err, true
	}
	start := time.Now()
	if e = unix.Sendto(fd, packet, 0, dst); e != nil {
		return 0, false, nil, false
	}
	deadline := start.Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	var tx, rx int64
	var gotTX, gotRX bool
	buf := make([]byte, 2048)
	oob := make([]byte, 512)
	for {
		if e = ctx.Err(); e != nil {
			return -1, false, e, true
		}
		if gotTX && gotRX {
			v, e := checkedICMPInterval(start, time.Now(), tx, rx)
			if e != nil {
				return -1, false, &timestampProbeError{e}, true
			}
			return v, true, nil, true
		}
		if !time.Now().Before(deadline) {
			if gotRX && !gotTX {
				return -1, false, &timestampProbeError{errors.New("transmit timestamp missing")}, true
			}
			return -1, false, nil, true
		}
		// A socket belongs to exactly one probe, so its transmit error queue cannot
		// contain a different probe's timestamp.
		_, n, flags, _, e := unix.Recvmsg(fd, buf, oob, unix.MSG_ERRQUEUE|unix.MSG_DONTWAIT)
		if e == nil {
			if flags&unix.MSG_CTRUNC != 0 {
				return -1, false, &timestampProbeError{errors.New("truncated transmit timestamp")}, true
			}
			kind, valid := icmpTxTimestampKind(oob[:n])
			if stamp, ok := softwareICMPTimestamp(oob[:n]); ok && valid {
				switch kind {
				case unix.SCM_TSTAMP_SND:
					tx, gotTX = stamp, true
				}
			}
		}
		for {
			n, on, flags, from, e := unix.Recvmsg(fd, buf, oob, unix.MSG_DONTWAIT)
			if e == unix.EAGAIN || e == unix.EWOULDBLOCK {
				break
			}
			if e != nil {
				return -1, false, &timestampProbeError{e}, true
			}
			b := buf[:n]
			if family == unix.AF_INET && privileged {
				if len(b) < 20 || b[0]>>4 != 4 {
					continue
				}
				h := int(b[0]&15) * 4
				if h < 20 || h > len(b) {
					continue
				}
				b = b[h:]
			}
			m, e := icmp.ParseMessage(network, b)
			if e != nil {
				continue
			}
			if (family == unix.AF_INET && m.Type != ipv4.ICMPTypeEchoReply) || (family == unix.AF_INET6 && m.Type != ipv6.ICMPTypeEchoReply) {
				continue
			}
			echo, ok := m.Body.(*icmp.Echo)
			if !ok || echo.Seq != 1 || string(echo.Data) != string(nonce[:]) {
				continue
			}
			switch a := from.(type) {
			case *unix.SockaddrInet4:
				if !addr.Equal(net.IP(a.Addr[:])) {
					continue
				}
			case *unix.SockaddrInet6:
				if !addr.Equal(net.IP(a.Addr[:])) {
					continue
				}
			default:
				continue
			}
			if flags&unix.MSG_CTRUNC != 0 {
				return -1, false, &timestampProbeError{errors.New("truncated receive timestamp")}, true
			}
			rx, gotRX = softwareICMPTimestamp(oob[:on])
			if !gotRX {
				return -1, false, &timestampProbeError{errors.New("receive timestamp missing")}, true
			}
		}
		if gotTX && gotRX {
			continue
		}
		remaining := time.Until(deadline)
		if remaining > 20*time.Millisecond {
			remaining = 20 * time.Millisecond
		}
		ms := int((remaining + time.Millisecond - 1) / time.Millisecond)
		if ms < 1 {
			ms = 1
		}
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN | unix.POLLERR}}
		_, e = unix.Poll(fds, ms)
		if e != nil && e != unix.EINTR {
			return -1, false, &timestampProbeError{fmt.Errorf("poll: %w", e)}, true
		}
	}
}
