package metricstore

import (
	v2 "github.com/Aone2233/nekomari/protocol/v2"
	"testing"
	"time"
)

func TestUnknownQualityDoesNotWriteZeroMeasurements(t *testing.T) {
	r := v2.Report{UUID: "node", UpdatedAt: time.Now().UTC(), Quality: map[string]string{"cpu": "unknown", "ram": "unknown", "swap": "unknown", "load": "unknown", "disk": "unknown", "network": "unknown", "net_rate": "unknown", "traffic_cycle": "unknown", "connections": "unknown", "process": "unknown"}}
	if points := reportMetricPoints(r); len(points) != 0 {
		t.Fatalf("unknown metrics written: %+v", points)
	}
	r.Quality["cpu"] = "ok"
	if points := reportMetricPoints(r); len(points) != 1 || points[0].MetricName != MetricCPU || points[0].Value != 0 {
		t.Fatalf("valid idle CPU lost: %+v", points)
	}
}
