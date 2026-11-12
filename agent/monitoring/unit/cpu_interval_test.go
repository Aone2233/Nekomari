package monitoring

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCPUIntervalGuestAndCounterReset(t *testing.T) {
	dir := t.TempDir()
	old := flags.HostProc
	flags.HostProc = dir
	defer func() {
		flags.HostProc = old
		cpuIntervalState.Lock()
		cpuIntervalState.ready = false
		cpuIntervalState.Unlock()
	}()
	cpuIntervalState.Lock()
	cpuIntervalState.ready = false
	cpuIntervalState.Unlock()
	write := func(line string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(line+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// Windows clocks may return the same timestamp for consecutive samples.
	// Wait for an actual interval rather than assuming filesystem writes take
	// longer than the platform's clock resolution.
	sample := func() (float64, float64, bool) {
		t.Helper()
		cpuIntervalState.Lock()
		last, ready := cpuIntervalState.lastAt, cpuIntervalState.ready
		cpuIntervalState.Unlock()
		deadline := time.Now().Add(time.Second)
		for ready && !time.Now().After(last) {
			if time.Now().After(deadline) {
				t.Fatal("clock did not advance")
			}
			time.Sleep(time.Millisecond)
		}
		return cpuIntervalSample()
	}
	write("cpu 100 100 0 100 0 0 0 0 20 20")
	if _, _, ok := sample(); ok {
		t.Fatal("first sample must be unknown")
	}
	write("cpu 150 150 0 200 0 0 0 0 70 70")
	if usage, _, ok := sample(); !ok || usage != 50 {
		t.Fatalf("guest-inclusive usage = %v, valid %v; want 50", usage, ok)
	}
	write("cpu 10 10 0 10 0 0 0 0 0 0")
	if _, _, ok := sample(); ok {
		t.Fatal("counter reset must be unknown")
	}
	write("cpu 20 10 0 20 0 0 0 0 0 0")
	if usage, _, ok := sample(); !ok || usage != 50 {
		t.Fatalf("recovery usage = %v, valid %v; want 50", usage, ok)
	}
}
