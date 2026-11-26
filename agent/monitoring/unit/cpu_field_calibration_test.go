package monitoring

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func referenceCPU(t *testing.T) (uint64, uint64) {
	t.Helper()
	f, err := os.Open("/proc/stat")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	if !s.Scan() {
		t.Fatal("missing cpu")
	}
	fields := strings.Fields(s.Text())
	if len(fields) < 9 || fields[0] != "cpu" {
		t.Fatal("short cpu line")
	}
	var total, idle uint64
	for i, x := range fields[1:9] {
		v, err := strconv.ParseUint(x, 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		total += v
		if i == 3 || i == 4 {
			idle += v
		}
	}
	return total, idle
}
func TestCPUFieldCalibration(t *testing.T) {
	if os.Getenv("NEKOMARI_CPU_FIELD") != "1" {
		t.Skip("field calibration disabled")
	}
	cpuIntervalState.Lock()
	cpuIntervalState.ready = false
	cpuIntervalState.Unlock()
	for run := 1; run <= 3; run++ {
		rt, ri := referenceCPU(t)
		_, _, ok := cpuIntervalSample()
		if run == 1 && ok {
			t.Fatal("initial sample was not unknown")
		}
		time.Sleep(60 * time.Second)
		usage, elapsed, ok := cpuIntervalSample()
		et, ei := referenceCPU(t)
		if !ok || et <= rt || ei < ri || ei-ri > et-rt {
			t.Fatalf("invalid sample run %d", run)
		}
		expected := (1 - float64(ei-ri)/float64(et-rt)) * 100
		diff := usage - expected
		if diff < 0 {
			diff = -diff
		}
		fmt.Printf("CPU_FIELD run=%d usage=%.6f reference=%.6f diff_pp=%.6f interval=%.6f\n", run, usage, expected, diff, elapsed)
		if diff > 2 {
			t.Errorf("CPU difference %.6f > 2 percentage points", diff)
		}
	}
}
