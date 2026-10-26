package clients

import (
	"math"
	"reflect"
	"strings"
	"testing"

	v2 "github.com/Aone2233/nekomari/protocol/v2"
)

func TestReportResourceClampPreservesOtherMeasurements(t *testing.T) {
	report := v2.Report{CPU: v2.CPUReport{Usage: 27.5}, Ram: v2.RamReport{Used: 120, Total: 100},
		Swap: v2.RamReport{Used: 5}, Disk: v2.DiskReport{Used: 300, Total: 200},
		Network: v2.NetworkReport{TotalDown: 123456}, Uptime: 42}
	if err := ReportVerify(report); err == nil {
		t.Fatal("strict compatibility validator accepted resource mismatch")
	}
	fields, err := NormalizeReportResources(&report)
	if err != nil || !reflect.DeepEqual(fields, []string{"ram", "swap", "disk"}) {
		t.Fatalf("clamp fields = %v, error = %v", fields, err)
	}
	if report.Ram.Used != 100 || report.Swap.Used != 0 || report.Disk.Used != 200 ||
		report.CPU.Usage != 27.5 || report.Network.TotalDown != 123456 || report.Uptime != 42 {
		t.Fatal("clamp damaged the rest of the report")
	}
	if err := ReportVerify(report); err != nil {
		t.Fatal(err)
	}
	if fields, err := NormalizeReportResources(&report); err != nil || len(fields) != 0 {
		t.Fatal("normalization is not idempotent")
	}
	if _, err := NormalizeReportResources(nil); err == nil {
		t.Fatal("nil report accepted")
	}
}

func TestReportResourceClampDoesNotHideInvalidInputs(t *testing.T) {
	for name, mutate := range map[string]func(*v2.Report){
		"negative used":    func(r *v2.Report) { r.Swap.Used = -1 },
		"negative total":   func(r *v2.Report) { r.Ram.Total = -1 },
		"overflow used":    func(r *v2.Report) { r.Disk.Used = math.MaxInt64 },
		"overflow total":   func(r *v2.Report) { r.Disk.Total = math.MaxInt64 },
		"nonfinite cpu":    func(r *v2.Report) { r.CPU.Usage = math.NaN() },
		"high cpu":         func(r *v2.Report) { r.CPU.Usage = 101 },
		"nonfinite load":   func(r *v2.Report) { r.Load.Load5 = math.Inf(1) },
		"negative load":    func(r *v2.Report) { r.Load.Load15 = -1 },
		"negative network": func(r *v2.Report) { r.Network.Down = -1 },
		"negative cycle":   func(r *v2.Report) { r.Network.CycleUp = -1 },
		"negative uptime":  func(r *v2.Report) { r.Uptime = -1 },
		"negative process": func(r *v2.Report) { r.Process = -1 },
		"negative tcp":     func(r *v2.Report) { r.Connections.TCP = -1 },
		"negative udp":     func(r *v2.Report) { r.Connections.UDP = -1 },
		"sample interval":  func(r *v2.Report) { r.SampleIntervalSeconds = 301 },
		"nonfinite sample": func(r *v2.Report) { r.SampleIntervalSeconds = math.Inf(1) },
		"nonfinite gpu": func(r *v2.Report) {
			r.GPU = &v2.GPUDetailReport{AverageUsage: math.NaN()}
		},
		"nonfinite gpu device": func(r *v2.Report) {
			r.GPU = &v2.GPUDetailReport{DetailedInfo: []v2.GPUDeviceInfo{{Utilization: math.Inf(1)}}}
		},
		"oversize payload": func(r *v2.Report) { r.Message = strings.Repeat("x", 5000) },
	} {
		t.Run(name, func(t *testing.T) {
			r := v2.Report{Ram: v2.RamReport{Used: 100, Total: 10}, Disk: v2.DiskReport{Used: 20, Total: 10}}
			mutate(&r)
			ram, swap, disk := r.Ram, r.Swap, r.Disk
			if fields, err := NormalizeReportResources(&r); err == nil || len(fields) != 0 {
				t.Fatal("invalid input was repaired into a valid report")
			}
			if r.Ram != ram || r.Swap != swap || r.Disk != disk {
				t.Fatal("rejected report was partially modified")
			}
		})
	}
}
