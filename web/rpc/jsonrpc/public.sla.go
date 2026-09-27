package jsonrpc

import (
	"context"
	"time"

	"github.com/Aone2233/nekomari/internal/metricstore"
	"github.com/Aone2233/nekomari/internal/sla"
	"github.com/Aone2233/nekomari/pkg/rpc"
)

// public.sla.go
// SLA report over the metrics the panel already stores. Guest-readable like the rest
// of public:*, and subject to the same visibility rule: a hidden node stays hidden.

func init() {
	regPublic("getSlaReport", publicGetSlaReport, "Get availability, latency percentiles and outages for a window")
}

// slaWindowPresets are the windows a status page offers. Named rather than free-form so
// the report's bucket width is a decision the server makes, not a number a caller picks
// and the panel then has to explain.
var slaWindowPresets = map[string]time.Duration{
	"24h": 24 * time.Hour,
	"7d":  7 * 24 * time.Hour,
	"30d": 30 * 24 * time.Hour,
	"90d": 90 * 24 * time.Hour,
}

// slaBucketTarget caps how many buckets a report is computed from.
//
// The store's finest tier is one minute, so a 90-day window at full resolution is
// 129 600 buckets per series times the fleet — a report nobody asked for and a read
// budget that would refuse it anyway. Bucketing to roughly this many keeps the
// definitions honest (each incident is then accurate to the bucket, and the report
// says which bucket) while keeping the query bounded. The value is deliberately the
// same order as the dashboard's own point cap so the two agree on what "the last 30
// days" looks like.
const slaBucketTarget = 500

type publicSlaReportParams struct {
	UUID      string   `json:"uuid"`
	EntityID  string   `json:"entity_id"`
	EntityIDs []string `json:"entity_ids"`

	// Window is one of slaWindowPresets. Defaults to 24h.
	Window string `json:"window"`
	Hours  float64 `json:"hours"`

	Start *time.Time `json:"start"`
	End   *time.Time `json:"end"`
}

type publicSlaReportResponse struct {
	Start           time.Time          `json:"start"`
	End             time.Time          `json:"end"`
	Window          string             `json:"window"`
	IntervalSeconds float64            `json:"interval_seconds"`
	Clamped         string             `json:"clamped,omitempty"`
	Nodes           []sla.NodeReport   `json:"nodes"`
	Count           int                `json:"count"`
	Presets         map[string]float64 `json:"window_presets"`
}

func publicGetSlaReport(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params publicSlaReportParams
	if err := req.BindParams(&params); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid request body: "+err.Error(), nil)
	}

	store := metricstore.GetStore()
	if store == nil {
		return nil, rpc.MakeError(rpc.InternalError, "metric store not initialized", nil)
	}

	now := time.Now().UTC()
	end := metricQueryTimeOrDefault(params.End, now)

	window, label, clampReason, rpcErr := resolveSlaWindow(ctx, params, now, end)
	if rpcErr != nil {
		return nil, rpcErr
	}
	start := end.Add(-window)

	// Only the requested entities, and only the visible ones. A hidden node is absent
	// from the answer entirely rather than reported with zeroes, which would leak that
	// it exists.
	requested := normalizeStringList(params.EntityIDs, []string{firstNonEmpty(params.EntityID, params.UUID)})
	entityIDs, rpcErr := publicMetricEntityIDs(ctx, requested)
	if rpcErr != nil {
		return nil, rpcErr
	}

	interval := slaBucketInterval(window)
	if compatible := store.CompatibleSeriesInterval(start, now, interval); compatible > 0 {
		interval = compatible
	}

	report, err := sla.Build(ctx, sla.ReportRequest{
		Reader:      store,
		EntityIDs:   entityIDs,
		Start:       start,
		End:         end,
		Interval:    interval,
		WindowLabel: label,
		Clamped:     clampReason,
		Now:         now,
	})
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to build the SLA report: "+err.Error(), nil)
	}

	presets := make(map[string]float64, len(slaWindowPresets))
	for name, duration := range slaWindowPresets {
		presets[name] = duration.Hours()
	}

	return publicSlaReportResponse{
		Start:           report.Start,
		End:             report.End,
		Window:          report.Window,
		IntervalSeconds: report.IntervalSeconds,
		Clamped:         report.Clamped,
		Nodes:           report.Nodes,
		Count:           len(report.Nodes),
		Presets:         presets,
	}, nil
}

// resolveSlaWindow turns the request into a window, a label and a clamp reason.
//
// The clamp is what makes the report honest about its own scope: asking for 90 days when
// the store keeps 30 answers a 30-day question, and says so, because the coverage figure
// depends on the window being the window asked for.
func resolveSlaWindow(ctx context.Context, params publicSlaReportParams, now, end time.Time) (time.Duration, string, string, *rpc.JsonRpcError) {
	retention := metricRetentionWindow(ctx)
	clamp := func(window time.Duration) (time.Duration, string) {
		if retention <= 0 || window <= retention {
			return window, ""
		}
		return retention, "requested " + window.Round(time.Hour).String() +
			" but only " + retention.Round(time.Hour).String() + " is retained"
	}

	if params.Start != nil && params.End != nil {
		window := end.Sub(*params.Start)
		if window <= 0 {
			return 0, "", "", rpc.MakeError(rpc.InvalidParams, "end must be after start", nil)
		}
		clamped, reason := clamp(window)
		return clamped, "custom", reason, nil
	}

	if params.Window == "" && params.Hours > 0 {
		clamped, reason := clamp(metricQueryHours(params.Hours))
		return clamped, "custom", reason, nil
	}

	label := params.Window
	if label == "" {
		label = "24h"
	}
	window, ok := slaWindowPresets[label]
	if !ok {
		return 0, "", "", rpc.MakeError(rpc.InvalidParams, "unknown window "+label, nil)
	}
	clamped, reason := clamp(window)
	return clamped, label, reason, nil
}

// slaBucketInterval picks the finest bucket width that keeps a window within
// slaBucketTarget.
//
// The widths are the store's own tiers rather than an arbitrary division of the window.
// Two reasons, and the second is the one that matters:
//
//   - a report bucketed on a tier boundary asks the store for data it actually keeps,
//     instead of making it aggregate across tiers;
//   - a 24-hour window divided by the target lands on two minutes, which is 720 buckets
//     — over the target the constant exists to enforce. The first version of this
//     truncated to a round minute and produced exactly that, and the test that asserts
//     every preset stays within the target is what caught it.
//
// The smallest tier is one minute, so a short window is reported at the store's finest
// resolution rather than at a width nobody stores.
func slaBucketInterval(window time.Duration) time.Duration {
	if window <= 0 {
		return time.Minute
	}
	for _, tier := range slaBucketTiers {
		if window/tier <= slaBucketTarget {
			return tier
		}
	}
	return slaBucketTiers[len(slaBucketTiers)-1]
}

// slaBucketTiers are the bucket widths a report may ask for, ascending. They mirror the
// resolutions the metric store maintains.
var slaBucketTiers = []time.Duration{
	time.Minute,
	5 * time.Minute,
	15 * time.Minute,
	time.Hour,
	6 * time.Hour,
	24 * time.Hour,
}

// metricRetentionWindow is the longest window the metric store can answer, from the
// definitions that are actually configured rather than a constant: an instance that kept
// its history for a year would otherwise be told its own data was gone.
func metricRetentionWindow(ctx context.Context) time.Duration {
	summary, err := metricstore.GetRetentionSummary(ctx)
	if err != nil || summary.MaxDays <= 0 {
		return 0
	}
	return time.Duration(summary.MaxDays) * 24 * time.Hour
}
