package jsonrpc

import (
	"testing"
	"time"

	"github.com/Aone2233/nekomari/pkg/metric"
)

func TestPublicPingStatsCarriesMultiProbeSummary(t *testing.T) {
	base := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	groups := publicPingMetricAggregateGroups{
		Avg: map[string][]metric.AggregatePoint{
			"7": {{Bucket: base, Count: 3, Value: 20}},
		},
		Loss: map[string][]metric.AggregatePoint{
			"7": {{Bucket: base, Count: 3, Value: 0}},
		},
		LossAvailable: true,
		Attempts: map[string][]metric.AggregatePoint{
			"7": {{Bucket: base, Count: 4, Value: 12}},
		},
		SuccessCount: map[string][]metric.AggregatePoint{
			"7": {{Bucket: base, Count: 4, Value: 10}},
		},
		LossPct: map[string][]metric.AggregatePoint{
			"7": {{Bucket: base, Count: 4, Value: 16.666666666666668}},
		},
		Jitter: map[string][]metric.AggregatePoint{
			"7": {{Bucket: base, Count: 4, Value: 2.5}},
		},
	}
	stats := publicPingStatsFromAggregateGroups("node-a", groups, nil, nil)
	if len(stats) != 1 {
		t.Fatalf("stats = %#v, want one group", stats)
	}
	got := stats[0]
	if got.Attempts == nil || *got.Attempts != 12 {
		t.Fatalf("attempts = %v, want 12", got.Attempts)
	}
	if got.Success == nil || *got.Success != 10 {
		t.Fatalf("success = %v, want 10", got.Success)
	}
	if got.LossPct == nil || *got.LossPct < 16.66 || *got.LossPct > 16.67 {
		t.Fatalf("loss_pct = %v, want 16.666...", got.LossPct)
	}
	if got.Jitter == nil || *got.Jitter != 2.5 {
		t.Fatalf("jitter = %v, want 2.5", got.Jitter)
	}
}
