//go:build !linux || (!amd64 && !arm64)

package server

import "context"
import "time"

type timestampProbeError struct{ err error }

func (e *timestampProbeError) Error() string { return "ICMP timestamp unavailable: " + e.err.Error() }
func (e *timestampProbeError) Unwrap() error { return e.err }
func preciseICMP(context.Context, string, time.Duration, bool) (int64, bool, error, bool) {
	return 0, false, nil, false
}
