package metricstore

import (
	"github.com/Aone2233/nekomari/pkg/metric"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
	"math"
	"time"
)

// Each interval is assigned to its ending sample. Never bridge restart,
// unknown epoch, collection failure, reset or a missing reporting window.
func reportIntervalPoints(report v2.Report, previous reportTrafficValues) []metric.Point {
	interval := report.SampleIntervalSeconds
	if math.IsNaN(interval) || math.IsInf(interval, 0) || interval < 1 || interval > 300 {
		interval = 5
	}
	gap := report.UpdatedAt.Sub(previous.timestamp)
	valid := report.CounterEpoch != "" && previous.epoch == report.CounterEpoch && !previous.timestamp.IsZero() && previous.hasUp && previous.hasDown && gap > 0 && gap <= time.Duration(3*interval*float64(time.Second)) && report.Uptime >= previous.uptime && report.Network.TotalUp >= previous.totalUp && report.Network.TotalDown >= previous.totalDown
	if quality, exists := report.Quality["network"]; exists && quality != "ok" {
		valid = false
	}
	if quality, exists := report.Quality["uptime"]; exists && quality != "ok" {
		valid = false
	}
	flag := 0.0
	points := make([]metric.Point, 0, 3)
	if valid {
		flag = 1
		points = append(points, metric.Point{MetricName: MetricTrafficIntervalUp, EntityID: report.UUID, Timestamp: report.UpdatedAt, Value: float64(report.Network.TotalUp - previous.totalUp)}, metric.Point{MetricName: MetricTrafficIntervalDown, EntityID: report.UUID, Timestamp: report.UpdatedAt, Value: float64(report.Network.TotalDown - previous.totalDown)})
	}
	points = append(points, metric.Point{MetricName: MetricTrafficIntervalValid, EntityID: report.UUID, Timestamp: report.UpdatedAt, Value: flag})
	return points
}
