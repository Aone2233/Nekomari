package jsonrpc

import (
	"context"
	"sort"
	"strconv"
	"time"

	"github.com/Aone2233/nekomari/internal/metricstore"
	"github.com/Aone2233/nekomari/pkg/metric"

	"github.com/Aone2233/nekomari/database/clients"
	"github.com/Aone2233/nekomari/pkg/rpc"
	agentRuntime "github.com/Aone2233/nekomari/web/agent"
)

type probeQualityNode struct {
	UUID                  string            `json:"uuid"`
	Name                  string            `json:"name"`
	LastReportAgeSeconds  *float64          `json:"last_report_age_seconds,omitempty"`
	SampledAt             *time.Time        `json:"sampled_at,omitempty"`
	ReceivedAt            *time.Time        `json:"received_at,omitempty"`
	SampleIntervalSeconds float64           `json:"sample_interval_seconds,omitempty"`
	Quality               map[string]string `json:"quality,omitempty"`
	QualityFields         int               `json:"quality_fields"`
	UnknownFields         int               `json:"unknown_fields"`
	CPUIntervalSeconds    float64           `json:"cpu_interval_seconds,omitempty"`
	CgroupPresent         bool              `json:"cgroup_present"`
	DiskMounts            int               `json:"disk_mounts"`
	TCPCountersPresent    bool              `json:"tcp_counters_present"`
	BootIDHashPresent     bool              `json:"boot_id_hash_present"`
	ResolvedIPPresent     bool              `json:"resolved_ip_present"`
}

func init() {
	reg("getProbeQuality", adminGetProbeQuality, "List per-node probe data quality")
	reg("getProbeUnknowns", adminGetProbeUnknowns, "List recent probe cycles that could not start")
}

func adminGetProbeQuality(_ context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	all, err := clients.GetAllClientBasicInfo()
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to list clients: "+err.Error(), nil)
	}
	ids := make([]string, 0, len(all))
	for _, client := range all {
		ids = append(ids, client.UUID)
	}
	latest := agentRuntime.GetLatestReport(ids...)
	now := time.Now().UTC()
	out := make([]probeQualityNode, 0, len(all))
	for _, client := range all {
		entry := probeQualityNode{UUID: client.UUID, Name: client.Name}
		if report := latest[client.UUID]; report != nil {
			age := now.Sub(report.ReceivedAt)
			if report.ReceivedAt.IsZero() {
				age = now.Sub(report.UpdatedAt)
			}
			seconds := age.Seconds()
			entry.LastReportAgeSeconds = &seconds
			if !report.SampledAt.IsZero() {
				sampled := report.SampledAt
				entry.SampledAt = &sampled
			}
			if !report.ReceivedAt.IsZero() {
				received := report.ReceivedAt
				entry.ReceivedAt = &received
			}
			entry.SampleIntervalSeconds = report.SampleIntervalSeconds
			if len(report.Quality) > 0 {
				entry.Quality = make(map[string]string, len(report.Quality))
				for key, value := range report.Quality {
					entry.Quality[key] = value
					entry.QualityFields++
					if value != "ok" {
						entry.UnknownFields++
					}
				}
			}
			entry.CPUIntervalSeconds = report.CPU.IntervalSeconds
			entry.CgroupPresent = report.CPU.Cgroup != nil
			entry.DiskMounts = len(report.Disk.Mounts)
			entry.TCPCountersPresent = report.Network.TCPOutSegs != nil && report.Network.TCPRetransSegs != nil
			entry.BootIDHashPresent = report.BootIDHash != ""
		}
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return map[string]any{"now": now, "nodes": out}, nil
}

type probeUnknownItem struct {
	Client   string    `json:"client"`
	Reason   string    `json:"reason"`
	Time     time.Time `json:"time"`
	Count    float64   `json:"count"`
	Role     string    `json:"role,omitempty"`
	Protocol string    `json:"protocol,omitempty"`
}

// Recent unknown results are explicitly time bounded and requested on demand.
// This is historical evidence, not a statement about the current probe state.
func adminGetProbeUnknowns(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		TaskID uint `json:"task_id"`
	}
	if err := req.BindParams(&params); err != nil || params.TaskID == 0 {
		return nil, rpc.MakeError(rpc.InvalidParams, "task_id is required", nil)
	}
	store := metricstore.GetStore()
	if store == nil {
		return nil, rpc.MakeError(rpc.Unavailable, "metric store unavailable", nil)
	}
	now := time.Now().UTC()
	points, err := store.Series(ctx, metric.AggregateQuery{Query: metric.Query{MetricName: metricstore.MetricPingUnknown, Tags: map[string]string{"task_id": strconv.FormatUint(uint64(params.TaskID), 10)}, Start: now.Add(-time.Hour), End: now, Order: metric.OrderDesc}, Aggregation: metric.AggSum, Interval: time.Minute, PreserveSeries: true}, now)
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to read probe diagnostics", nil)
	}
	result := make([]probeUnknownItem, 0, min(len(points), 100))
	for _, point := range points {
		if len(result) == 100 {
			break
		}
		result = append(result, probeUnknownItem{Client: point.EntityID, Reason: point.Tags["reason"], Time: point.Bucket, Count: point.Value, Role: point.Tags["role"], Protocol: point.Tags["protocol"]})
	}
	return map[string]any{"since": now.Add(-time.Hour), "until": now, "items": result, "limited": len(points) > 100}, nil
}
