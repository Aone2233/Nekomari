package server

import (
	"math"
	"os"
	"strings"
	"testing"
)

func fakeCgroupFiles(files map[string]string) cgroupFileReader {
	return func(path string) (string, error) {
		if content, ok := files[path]; ok {
			return content, nil
		}
		return "", os.ErrNotExist
	}
}

// TestResolveMemoryLimit pins the precedence and the arithmetic: an explicit
// override, then Go's own GOMEMLIMIT, then cgroup v2, then cgroup v1; 90% of the
// detected ceiling with a 64 MiB floor. No case reads a real /sys path, so the
// test is identical on Windows.
func TestResolveMemoryLimit(t *testing.T) {
	const (
		mib = int64(1) << 20
		gib = int64(1) << 30
	)
	tests := []struct {
		name          string
		komariEnv     string
		goLimitEnv    string
		files         map[string]string
		wantDetected  int64
		wantHeap      int64
		wantSource    memoryLimitSource
		wantDisabled  bool
		wantWarning   bool
		wantReasonHas string
	}{
		{
			name:         "explicit MiB wins over cgroup",
			komariEnv:    "512MiB",
			files:        map[string]string{cgroupV2MemoryMaxPath: "1073741824"},
			wantDetected: 512 * mib,
			wantHeap:     483183820,
			wantSource:   memoryLimitSourceEnv,
		},
		{
			name:         "explicit GiB",
			komariEnv:    "1GiB",
			wantDetected: gib,
			wantHeap:     966367641,
			wantSource:   memoryLimitSourceEnv,
		},
		{
			name:         "plain byte count",
			komariEnv:    "268435456",
			wantDetected: 256 * mib,
			wantHeap:     241591910,
			wantSource:   memoryLimitSourceEnv,
		},
		{
			name:         "spaces and a lowercase binary suffix",
			komariEnv:    "  512 mib ",
			wantDetected: 512 * mib,
			wantHeap:     483183820,
			wantSource:   memoryLimitSourceEnv,
		},
		{
			name:         "MB is read as the binary unit",
			komariEnv:    "1gb",
			wantDetected: gib,
			wantHeap:     966367641,
			wantSource:   memoryLimitSourceEnv,
		},
		{
			name:         "off disables",
			komariEnv:    "off",
			wantSource:   memoryLimitSourceEnv,
			wantDisabled: true,
		},
		{
			name:         "OFF disables",
			komariEnv:    "OFF",
			wantSource:   memoryLimitSourceEnv,
			wantDisabled: true,
		},
		{
			name:         "zero disables",
			komariEnv:    "0",
			wantSource:   memoryLimitSourceEnv,
			wantDisabled: true,
		},
		{
			name:         "zero bytes disables",
			komariEnv:    "0B",
			wantSource:   memoryLimitSourceEnv,
			wantDisabled: true,
		},
		{
			name:         "the floor applies to the heap target",
			komariEnv:    "64MiB",
			wantDetected: 64 * mib,
			wantHeap:     64 * mib,
			wantSource:   memoryLimitSourceEnv,
		},
		{
			name:         "a ceiling below the floor clamps up",
			komariEnv:    "32MiB",
			wantDetected: 32 * mib,
			wantHeap:     64 * mib,
			wantSource:   memoryLimitSourceEnv,
		},
		{
			name:         "an invalid override falls through to cgroup detection",
			komariEnv:    "banana",
			files:        map[string]string{cgroupV2MemoryMaxPath: "1073741824"},
			wantDetected: gib,
			wantHeap:     966367641,
			wantSource:   memoryLimitSourceCgroupV2,
			wantWarning:  true,
		},
		{
			name:          "an overflowing override falls through and is reported",
			komariEnv:     "99999999999999999999",
			wantSource:    memoryLimitSourceNone,
			wantWarning:   true,
			wantReasonHas: "no environment override and no cgroup memory limit",
		},
		{
			name:          "cgroup v2 max means unlimited",
			files:         map[string]string{cgroupV2MemoryMaxPath: "max\n"},
			wantSource:    memoryLimitSourceCgroupV2,
			wantReasonHas: "max",
		},
		{
			name:         "cgroup v1 is the fallback",
			files:        map[string]string{cgroupV1MemoryLimitPath: "1073741824\n"},
			wantDetected: gib,
			wantHeap:     966367641,
			wantSource:   memoryLimitSourceCgroupV1,
		},
		{
			name:          "cgroup v1 sentinel means unlimited",
			files:         map[string]string{cgroupV1MemoryLimitPath: "9223372036854771712"},
			wantSource:    memoryLimitSourceCgroupV1,
			wantReasonHas: "sentinel",
		},
		{
			name:          "nothing found leaves Go's default",
			wantSource:    memoryLimitSourceNone,
			wantReasonHas: "no environment override and no cgroup memory limit",
		},
		{
			name:          "an explicit GOMEMLIMIT is respected",
			goLimitEnv:    "1GiB",
			files:         map[string]string{cgroupV2MemoryMaxPath: "536870912"},
			wantSource:    memoryLimitSourceNone,
			wantDisabled:  true,
			wantReasonHas: goMemoryLimitEnvKey,
		},
		{
			name:         "the explicit override still beats GOMEMLIMIT",
			komariEnv:    "512MiB",
			goLimitEnv:   "1GiB",
			files:        map[string]string{cgroupV2MemoryMaxPath: "1073741824"},
			wantDetected: 512 * mib,
			wantHeap:     483183820,
			wantSource:   memoryLimitSourceEnv,
		},
		{
			name:          "an implausible explicit value is treated as unlimited",
			komariEnv:     "2305843009213693952", // 2 EiB: above the plausible ceiling
			wantSource:    memoryLimitSourceEnv,
			wantReasonHas: "plausible",
		},
		{
			name:         "garbage in cgroup v2 falls through to v1",
			files:        map[string]string{cgroupV2MemoryMaxPath: "not-a-number", cgroupV1MemoryLimitPath: "536870912"},
			wantDetected: 512 * mib,
			wantHeap:     483183820,
			wantSource:   memoryLimitSourceCgroupV1,
		},
		{
			name:       "max in cgroup v2 is authoritative over v1",
			files:      map[string]string{cgroupV2MemoryMaxPath: "max", cgroupV1MemoryLimitPath: "536870912"},
			wantSource: memoryLimitSourceCgroupV2,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision := resolveMemoryLimit(test.komariEnv, test.goLimitEnv, fakeCgroupFiles(test.files))
			if decision.DetectedBytes != test.wantDetected {
				t.Fatalf("DetectedBytes = %d, want %d", decision.DetectedBytes, test.wantDetected)
			}
			if decision.HeapLimitBytes != test.wantHeap {
				t.Fatalf("HeapLimitBytes = %d, want %d", decision.HeapLimitBytes, test.wantHeap)
			}
			if decision.Source != test.wantSource {
				t.Fatalf("Source = %q, want %q", decision.Source, test.wantSource)
			}
			if decision.Disabled != test.wantDisabled {
				t.Fatalf("Disabled = %v, want %v (reason %q)", decision.Disabled, test.wantDisabled, decision.Reason)
			}
			if test.wantWarning && decision.Warning == "" {
				t.Fatal("expected a warning for the invalid override")
			}
			if !test.wantWarning && decision.Warning != "" {
				t.Fatalf("unexpected warning: %q", decision.Warning)
			}
			if test.wantReasonHas != "" && !strings.Contains(decision.Reason, test.wantReasonHas) {
				t.Fatalf("Reason = %q, want it to contain %q", decision.Reason, test.wantReasonHas)
			}
		})
	}
}

// TestResolveMemoryLimitReadsTheCgroupPaths pins which files are consulted and
// in which order, so a rename of the constants cannot silently stop detection.
func TestResolveMemoryLimitReadsTheCgroupPaths(t *testing.T) {
	var paths []string
	read := func(path string) (string, error) {
		paths = append(paths, path)
		return "", os.ErrNotExist
	}
	_ = resolveMemoryLimit("", "", read)
	want := []string{cgroupV2MemoryMaxPath, cgroupV1MemoryLimitPath}
	if len(paths) != len(want) {
		t.Fatalf("read %v, want %v", paths, want)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("read %v, want %v", paths, want)
		}
	}
}

// TestConfigureMemoryLimitSkipsTheRuntimeWhenDisabled asserts that "off" really
// does nothing: installing a limit would be a silent override of the operator.
func TestConfigureMemoryLimitSkipsTheRuntimeWhenDisabled(t *testing.T) {
	var installed []int64
	previous := setMemoryLimit
	setMemoryLimit = func(limit int64) int64 {
		installed = append(installed, limit)
		return math.MaxInt64
	}
	t.Cleanup(func() { setMemoryLimit = previous })

	decision := configureMemoryLimit("off", "", fakeCgroupFiles(nil))
	if decision.HeapLimitBytes != 0 {
		t.Fatalf("HeapLimitBytes = %d, want 0", decision.HeapLimitBytes)
	}
	if len(installed) != 0 {
		t.Fatalf("debug.SetMemoryLimit called with %v, want no call", installed)
	}
}

// TestConfigureMemoryLimitInstallsNinetyPercent asserts the value handed to the
// runtime is the 90% heap target, not the detected ceiling.
func TestConfigureMemoryLimitInstallsNinetyPercent(t *testing.T) {
	var installed []int64
	previous := setMemoryLimit
	setMemoryLimit = func(limit int64) int64 {
		installed = append(installed, limit)
		return math.MaxInt64
	}
	t.Cleanup(func() { setMemoryLimit = previous })

	decision := configureMemoryLimit("512MiB", "", fakeCgroupFiles(nil))
	if len(installed) != 1 {
		t.Fatalf("debug.SetMemoryLimit called %d times, want 1", len(installed))
	}
	if installed[0] != 483183820 {
		t.Fatalf("installed limit = %d, want 483183820 (90%% of 512 MiB)", installed[0])
	}
	if decision.DetectedBytes != 512<<20 {
		t.Fatalf("DetectedBytes = %d, want %d", decision.DetectedBytes, 512<<20)
	}
}

// TestApplyMemoryLimitReadsTheEnvironment covers the wiring the startup path
// uses: memory_limit.go reads the two environment variables, not a struct.
func TestApplyMemoryLimitReadsTheEnvironment(t *testing.T) {
	t.Setenv(memoryLimitEnvKey, "128MiB")
	t.Setenv(goMemoryLimitEnvKey, "")

	var installed []int64
	previous := setMemoryLimit
	setMemoryLimit = func(limit int64) int64 {
		installed = append(installed, limit)
		return math.MaxInt64
	}
	t.Cleanup(func() { setMemoryLimit = previous })

	decision := applyMemoryLimit()
	want := int64(128<<20) * 9 / 10
	if decision.HeapLimitBytes != want {
		t.Fatalf("HeapLimitBytes = %d, want %d", decision.HeapLimitBytes, want)
	}
	if decision.Source != memoryLimitSourceEnv {
		t.Fatalf("Source = %q, want %q", decision.Source, memoryLimitSourceEnv)
	}
	if len(installed) != 1 || installed[0] != want {
		t.Fatalf("debug.SetMemoryLimit called with %v, want [%d]", installed, want)
	}
}

func TestFormatMemoryBytes(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{1024, "1.0 KiB"},
		{512 << 20, "512.0 MiB"},
		{1 << 30, "1.0 GiB"},
		{483183820, "460.8 MiB"},
	}
	for _, test := range tests {
		if got := formatMemoryBytes(test.bytes); got != test.want {
			t.Fatalf("formatMemoryBytes(%d) = %q, want %q", test.bytes, got, test.want)
		}
	}
}

func TestFormatMemoryLimitPrevious(t *testing.T) {
	if got := formatMemoryLimitPrevious(math.MaxInt64); got != "unlimited" {
		t.Fatalf("previous = %q, want unlimited", got)
	}
	if got := formatMemoryLimitPrevious(1 << 30); got != "1.0 GiB" {
		t.Fatalf("previous = %q, want 1.0 GiB", got)
	}
}
