package server

import (
	"fmt"
	"math"
	"os"
	"runtime/debug"
	"strconv"
	"strings"

	logger "github.com/Aone2233/nekomari/utils/log"
)

// The panel runs in containers and systemd units whose memory ceiling is
// invisible to Go's collector: by default the runtime sizes its GC target from
// the live heap, so a container "1g" or a systemd MemoryMax only reaches the
// process as an OOM kill once a query or a fleet-wide rollup scan has already
// allocated past it. Setting a soft memory limit (debug.SetMemoryLimit) makes
// the collector work harder before that point.
//
// The value is resolved once at startup, in this order:
//
//  1. KOMARI_MEMORY_LIMIT - explicit override, a byte count or a binary size
//     such as 512MiB / 1GiB. "off" or "0" disables the soft limit entirely.
//  2. GOMEMLIMIT - if the operator already set Go's own variable, leave it be:
//     overriding it would silently discard an explicit decision.
//  3. cgroup v2 /sys/fs/cgroup/memory.max.
//  4. cgroup v1 /sys/fs/cgroup/memory/memory.limit_in_bytes.
//
// In every case the installed limit is 90% of the detected ceiling (the
// remainder is for non-heap memory: the SQLite page caches, the runtime's own
// structures, stacks) and never below minMemoryLimitBytes, because a limit low
// enough to make every allocation trigger a collection is worse than no limit.
const (
	memoryLimitEnvKey       = "KOMARI_MEMORY_LIMIT"
	goMemoryLimitEnvKey     = "GOMEMLIMIT"
	cgroupV2MemoryMaxPath   = "/sys/fs/cgroup/memory.max"
	cgroupV1MemoryLimitPath = "/sys/fs/cgroup/memory/memory.limit_in_bytes"
	minMemoryLimitBytes     = 64 << 20
	maxPlausibleMemoryLimit = 1 << 60 // 1 EiB: anything larger is a sentinel, not a limit
	memoryLimitHeadroomPct  = 90
)

// memoryLimitSource names where a detected limit came from, for the one log line
// the startup path emits.
type memoryLimitSource string

const (
	memoryLimitSourceNone     memoryLimitSource = "none"
	memoryLimitSourceEnv      memoryLimitSource = "env"
	memoryLimitSourceCgroupV2 memoryLimitSource = "cgroup-v2"
	memoryLimitSourceCgroupV1 memoryLimitSource = "cgroup-v1"
)

// memoryLimitDecision is the resolved outcome, before it is installed. It is a
// plain value so the whole resolution can be tested without touching /sys or the
// Go runtime.
type memoryLimitDecision struct {
	// DetectedBytes is the ceiling found, before headroom and floor.
	DetectedBytes int64
	// HeapLimitBytes is the value for debug.SetMemoryLimit; 0 means "leave
	// Go's own default in place".
	HeapLimitBytes int64
	// Source is where DetectedBytes came from.
	Source memoryLimitSource
	// Disabled reports an explicit "do not install a limit" decision.
	Disabled bool
	// Warning carries a problem worth logging (an unparseable override);
	// resolution continues to the next source rather than failing startup.
	Warning string
	// Reason explains an unset limit, for the Debug/Info line.
	Reason string
}

// cgroupFileReader reads one cgroup memory file. Production reads /sys; tests
// inject a map so they never depend on the host (Windows has no /sys at all).
type cgroupFileReader func(path string) (string, error)

func readCgroupMemoryFile(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(content), nil
}

// parseMemoryLimitValue parses a byte count or a binary size suffix. Integers
// only: a fractional value is a configuration error, not something to round.
// The suffixes are binary (1MiB = 1<<20) because the documented examples are
// 512MiB/1GiB; "MB" is accepted as the same thing for convenience.
func parseMemoryLimitValue(raw string) (int64, bool) {
	value := strings.ToLower(strings.TrimSpace(raw))
	value = strings.ReplaceAll(value, " ", "")
	if value == "" {
		return 0, false
	}
	digits := 0
	for digits < len(value) && value[digits] >= '0' && value[digits] <= '9' {
		digits++
	}
	if digits == 0 {
		return 0, false
	}
	n, err := strconv.ParseInt(value[:digits], 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	multiplier, ok := memoryUnitMultiplier(value[digits:])
	if !ok {
		return 0, false
	}
	if n > math.MaxInt64/multiplier {
		return 0, false
	}
	return n * multiplier, true
}

func memoryUnitMultiplier(unit string) (int64, bool) {
	switch unit {
	case "", "b", "byte", "bytes":
		return 1, true
	case "k", "kb", "kib":
		return 1 << 10, true
	case "m", "mb", "mib":
		return 1 << 20, true
	case "g", "gb", "gib":
		return 1 << 30, true
	case "t", "tb", "tib":
		return 1 << 40, true
	default:
		return 0, false
	}
}

// decisionFromDetected applies the 90% headroom and the floor. Exported values
// are not clamped: only the heap target is, so the log line can still say what
// the environment actually asked for.
func decisionFromDetected(detected int64, source memoryLimitSource, origin string) memoryLimitDecision {
	heapLimit := detected * memoryLimitHeadroomPct / 100
	if heapLimit < minMemoryLimitBytes {
		heapLimit = minMemoryLimitBytes
	}
	return memoryLimitDecision{
		DetectedBytes:  detected,
		HeapLimitBytes: heapLimit,
		Source:         source,
		Reason:         origin,
	}
}

// resolveMemoryLimit is the whole policy as one pure function. Precedence:
// KOMARI_MEMORY_LIMIT, then GOMEMLIMIT (leave Go alone), then cgroup v2, then
// cgroup v1.
func resolveMemoryLimit(komariEnv, goLimitEnv string, read cgroupFileReader) memoryLimitDecision {
	var warning string

	if raw := strings.TrimSpace(komariEnv); raw != "" {
		switch strings.ToLower(raw) {
		case "off", "0":
			return memoryLimitDecision{
				Source:   memoryLimitSourceEnv,
				Disabled: true,
				Reason:   fmt.Sprintf("%s=%s disables the soft memory limit", memoryLimitEnvKey, raw),
			}
		}
		if detected, ok := parseMemoryLimitValue(raw); ok {
			if detected == 0 {
				return memoryLimitDecision{
					Source:   memoryLimitSourceEnv,
					Disabled: true,
					Reason:   fmt.Sprintf("%s=%s disables the soft memory limit", memoryLimitEnvKey, raw),
				}
			}
			if detected >= maxPlausibleMemoryLimit {
				return memoryLimitDecision{
					Source: memoryLimitSourceEnv,
					Reason: fmt.Sprintf("%s=%s is above the plausible ceiling and is treated as unlimited", memoryLimitEnvKey, raw),
				}
			}
			return decisionFromDetected(detected, memoryLimitSourceEnv, fmt.Sprintf("%s=%s", memoryLimitEnvKey, raw))
		}
		// An unparseable override must not stop startup, and must not silently
		// discard the container's real ceiling either: remember it and detect.
		warning = fmt.Sprintf("%s=%q is not a byte count or size (for example 512MiB or 1GiB); using the detected limit", memoryLimitEnvKey, raw)
	}

	if strings.TrimSpace(goLimitEnv) != "" {
		decision := memoryLimitDecision{
			Source:   memoryLimitSourceNone,
			Disabled: true,
			Reason:   fmt.Sprintf("%s is set (%q); leaving Go's own soft memory limit in effect", goMemoryLimitEnvKey, strings.TrimSpace(goLimitEnv)),
		}
		decision.Warning = warning
		return decision
	}

	if raw, err := read(cgroupV2MemoryMaxPath); err == nil {
		raw = strings.TrimSpace(raw)
		if strings.EqualFold(raw, "max") {
			return memoryLimitDecision{
				Source:  memoryLimitSourceCgroupV2,
				Warning: warning,
				Reason:  fmt.Sprintf("%s is \"max\" (no limit in this cgroup)", cgroupV2MemoryMaxPath),
			}
		}
		if detected, ok := parseMemoryLimitValue(raw); ok {
			if detected <= 0 || detected >= maxPlausibleMemoryLimit {
				return memoryLimitDecision{
					Source:  memoryLimitSourceCgroupV2,
					Warning: warning,
					Reason:  fmt.Sprintf("%s=%q is a sentinel, not a limit", cgroupV2MemoryMaxPath, raw),
				}
			}
			decision := decisionFromDetected(detected, memoryLimitSourceCgroupV2, cgroupV2MemoryMaxPath)
			decision.Warning = warning
			return decision
		}
	}

	if raw, err := read(cgroupV1MemoryLimitPath); err == nil {
		raw = strings.TrimSpace(raw)
		if !strings.EqualFold(raw, "max") {
			if detected, ok := parseMemoryLimitValue(raw); ok {
				if detected <= 0 || detected >= maxPlausibleMemoryLimit {
					return memoryLimitDecision{
						Source:  memoryLimitSourceCgroupV1,
						Warning: warning,
						Reason:  fmt.Sprintf("%s=%q is a sentinel, not a limit", cgroupV1MemoryLimitPath, raw),
					}
				}
				decision := decisionFromDetected(detected, memoryLimitSourceCgroupV1, cgroupV1MemoryLimitPath)
				decision.Warning = warning
				return decision
			}
		}
	}

	return memoryLimitDecision{
		Source:  memoryLimitSourceNone,
		Warning: warning,
		Reason:  "no environment override and no cgroup memory limit was found; leaving Go's default soft memory limit",
	}
}

// setMemoryLimit is debug.SetMemoryLimit behind a seam: a test asserts that a
// disabled decision does not touch the runtime, and how much is installed.
var setMemoryLimit = debug.SetMemoryLimit

// applyMemoryLimit resolves the limit, installs it once, and logs where it came
// from. Called from the startup path; never from a request.
func applyMemoryLimit() memoryLimitDecision {
	return configureMemoryLimit(os.Getenv(memoryLimitEnvKey), os.Getenv(goMemoryLimitEnvKey), readCgroupMemoryFile)
}

func configureMemoryLimit(komariEnv, goLimitEnv string, read cgroupFileReader) memoryLimitDecision {
	decision := resolveMemoryLimit(komariEnv, goLimitEnv, read)
	if decision.Warning != "" {
		logger.Warn("server", decision.Warning)
	}
	if decision.HeapLimitBytes > 0 {
		previous := setMemoryLimit(decision.HeapLimitBytes)
		logger.Infof("server",
			"Soft memory limit set to %s (90%% of the %s detected from %s; the runtime's previous limit was %s)",
			formatMemoryBytes(decision.HeapLimitBytes),
			formatMemoryBytes(decision.DetectedBytes),
			decision.Source,
			formatMemoryLimitPrevious(previous),
		)
		return decision
	}
	if decision.Disabled {
		logger.Info("server", "Soft memory limit not set: "+decision.Reason)
		return decision
	}
	logger.Debug("server", "Soft memory limit not set: "+decision.Reason)
	return decision
}

// formatMemoryBytes renders a byte count in the binary units the limit is
// configured in, so a log line and the environment variable read the same way.
func formatMemoryBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func formatMemoryLimitPrevious(previous int64) string {
	if previous >= math.MaxInt64 || previous <= 0 {
		return "unlimited"
	}
	return formatMemoryBytes(previous)
}
