package monitoring

import (
	"os"
	"path/filepath"
	"testing"
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
	write("cpu 100 100 0 100 0 0 0 0 20 20")
	if _, _, ok := cpuIntervalSample(); ok {
		t.Fatal("first sample must be unknown")
	}
	write("cpu 150 150 0 200 0 0 0 0 70 70")
	if usage, _, ok := cpuIntervalSample(); !ok || usage != 50 {
		t.Fatalf("guest-inclusive usage = %v, valid %v; want 50", usage, ok)
	}
	write("cpu 10 10 0 10 0 0 0 0 0 0")
	if _, _, ok := cpuIntervalSample(); ok {
		t.Fatal("counter reset must be unknown")
	}
	write("cpu 20 10 0 20 0 0 0 0 0 0")
	if usage, _, ok := cpuIntervalSample(); !ok || usage != 50 {
		t.Fatalf("recovery usage = %v, valid %v; want 50", usage, ok)
	}
}
