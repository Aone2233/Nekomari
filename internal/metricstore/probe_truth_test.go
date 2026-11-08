package metricstore

import (
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/models"
)

func TestPingRecordPointsAddProbeTruthMetrics(t *testing.T) {
	at := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	status, redirects := 503, 2
	reachable, available := true, false
	dns, tcp, ttfb, total := int64(1000), int64(2000), int64(3000), int64(5000)
	rec := models.PingRecord{
		Client: "node-a", TaskId: 7, PingType: "http", Family: "ipv4", Time: at,
		Value: 5, ValueUS: 5000, Attempts: 3, Success: 2, LossPct: 33.333333333333336,
		MinUS: 4000, AvgUS: 5000, MaxUS: 6000, JitterUS: 63,
		HTTP: &models.PingHTTPObservation{
			DNSUS: &dns, TCPUS: &tcp, TTFBUS: &ttfb, TotalUS: &total,
			StatusCode: &status, Redirects: &redirects,
			Reachable: &reachable, Available: &available,
		},
	}
	points := pingRecordPoints([]models.PingRecord{rec})
	byName := map[string]float64{}
	for _, p := range points {
		byName[p.MetricName] = p.Value
	}
	want := map[string]float64{
		MetricPingAttempts:  3,
		MetricPingSuccess:   2,
		MetricPingLossPct:   33.333333333333336,
		MetricPingMin:       4,
		MetricPingAvg:       5,
		MetricPingMax:       6,
		MetricPingJitter:    0.063,
		MetricHTTPDNS:       1,
		MetricHTTPTCP:       2,
		MetricHTTPTTFB:      3,
		MetricHTTPTotal:     5,
		MetricHTTPStatus:    503,
		MetricHTTPRedirects: 2,
		MetricHTTPReachable: 1,
		MetricHTTPAvailable: 0,
	}
	for name, value := range want {
		got, ok := byName[name]
		if !ok {
			t.Errorf("missing metric %s in %v", name, byName)
			continue
		}
		if got != value {
			t.Errorf("%s = %v, want %v", name, got, value)
		}
	}
}
