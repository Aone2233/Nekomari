//go:build linux

package monitoring

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	pkgflags "github.com/Aone2233/nekomari/agent/cmd/flags"
)

func memoryReference(t *testing.T) (uint64, uint64) {
	t.Helper()
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	values := map[string]uint64{}
	s := bufio.NewScanner(f)
	for s.Scan() {
		parts := strings.Fields(s.Text())
		if len(parts) < 2 {
			continue
		}
		v, e := strconv.ParseUint(parts[1], 10, 64)
		if e == nil {
			values[strings.TrimSuffix(parts[0], ":")] = v * 1024
		}
	}
	if err := s.Err(); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"MemTotal", "MemFree", "Cached", "SReclaimable", "Buffers", "Shmem"} {
		if _, ok := values[k]; !ok {
			t.Fatalf("missing %s", k)
		}
	}
	total := values["MemTotal"]
	free := values["MemFree"]
	deductions := free + values["Cached"] + values["SReclaimable"] + values["Buffers"]
	if free > total || deductions > total || values["Shmem"] > deductions {
		t.Fatal("invalid reference values")
	}
	return total, total - deductions + values["Shmem"]
}

// Compare the default production RAM path with separately parsed procfs.
func TestMemoryFieldCalibration(t *testing.T) {
	if os.Getenv("NEKOMARI_MEMORY_FIELD") != "1" {
		t.Skip("field calibration disabled")
	}
	if pkgflags.GlobalConfig.MemoryIncludeCache || pkgflags.GlobalConfig.MemoryReportRawUsed {
		t.Fatal("expected default flags")
	}
	for run := 1; run <= 3; run++ {
		total, before := memoryReference(t)
		got := Ram()
		afterTotal, after := memoryReference(t)
		if !got.Valid || got.Mode != "htoplike" || got.Total != total || total != afterTotal {
			t.Fatalf("invalid Agent/reference: %+v %d %d", got, total, afterTotal)
		}
		mid := (float64(before) + float64(after)) / 2
		diff := math.Abs(float64(got.Used) - mid)
		tolerance := math.Max(float64(total)*.01, 4096)
		fmt.Printf("MEMORY_FIELD run=%d total=%d agent=%d before=%d after=%d diff_bytes=%.0f tolerance_bytes=%.0f\n", run, total, got.Used, before, after, diff, tolerance)
		if diff > tolerance {
			t.Errorf("run %d difference %.0f exceeds %.0f", run, diff, tolerance)
		}
		time.Sleep(250 * time.Millisecond)
	}
}
