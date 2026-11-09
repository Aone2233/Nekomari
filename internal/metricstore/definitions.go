package metricstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/Aone2233/nekomari/pkg/metric"
)

const (
	// Panel-only history is cheap to rebuild and stays at one day.
	defaultBuiltinMetricRetentionDays = 1
	// New installs keep a useful personal history: 30 days of node metrics and
	// 7 days of ping/HTTP. Existing definitions keep their stored retention.
	defaultNodeMetricRetentionDays = 30
	defaultPingMetricRetentionDays = 7
)

// RetentionSummary is the compatibility view of node metric policies.
type RetentionSummary struct {
	AllPositive bool
	MaxDays     int
}

// GetRetentionSummary excludes panel-only definitions from node compatibility
// settings. An empty node definition set is not considered record-enabled.
func GetRetentionSummary(ctx context.Context) (RetentionSummary, error) {
	s := GetStore()
	if s == nil {
		return RetentionSummary{}, fmt.Errorf("metric store not initialized")
	}
	defs, err := s.ListMetrics(ctx)
	if err != nil {
		return RetentionSummary{}, err
	}
	return summarizeRetentionDefinitions(defs), nil
}

func summarizeRetentionDefinitions(defs []metric.Definition) RetentionSummary {
	summary := RetentionSummary{AllPositive: true}
	count := 0
	for _, def := range defs {
		// Panel history is independently optional and must not change the
		// compatibility status of node record retention.
		if IsPanelMetric(def.Name) {
			continue
		}
		count++
		if def.RetentionDays <= 0 {
			summary.AllPositive = false
		}
		if def.RetentionDays > summary.MaxDays {
			summary.MaxDays = def.RetentionDays
		}
	}
	if count == 0 {
		return RetentionSummary{}
	}
	return summary
}

// createMetricDefinitions creates built-in definitions with explicit policies.
func createMetricDefinitions(ctx context.Context, s *metric.Store) error {
	return createMetricDefinitionsWithDefaultRetention(ctx, s, defaultNodeMetricRetentionDays)
}

// EnsureBuiltinMetricDefinitions registers definitions for the server's
// built-in report and ping writers before a standalone Store receives points.
func EnsureBuiltinMetricDefinitions(ctx context.Context, s *metric.Store) error {
	return createMetricDefinitions(ctx, s)
}

func createMetricDefinitionsWithDefaultRetention(ctx context.Context, s *metric.Store, defaultRetentionDays int) error {
	if defaultRetentionDays < defaultBuiltinMetricRetentionDays {
		defaultRetentionDays = defaultBuiltinMetricRetentionDays
	}
	definitions := []metric.Definition{
		{Name: MetricCPU, Type: metric.TypeGauge, Unit: "%", Description: "CPU usage percentage", RetentionDays: defaultRetentionDays},
		{Name: MetricCPUInterval, Type: metric.TypeGauge, Unit: "s", Description: "Actual host CPU averaging interval", RetentionDays: defaultRetentionDays},
		{Name: MetricCPUCgroupUsage, Type: metric.TypeGauge, Unit: "%", Description: "cgroup CPU usage percentage", RetentionDays: defaultRetentionDays},
		{Name: MetricCPUCgroupQuota, Type: metric.TypeGauge, Unit: "cores", Description: "cgroup CPU quota in cores", RetentionDays: defaultRetentionDays},
		{Name: MetricCPUCgroupThrottled, Type: metric.TypeGauge, Unit: "s", Description: "cgroup cumulative throttled seconds", RetentionDays: defaultRetentionDays},
		{Name: MetricGPU, Type: metric.TypeGauge, Unit: "%", Description: "GPU usage percentage", RetentionDays: defaultRetentionDays},
		{Name: MetricGPUDeviceUsage, Type: metric.TypeGauge, Unit: "%", Description: "Per-device GPU utilization", RetentionDays: defaultRetentionDays},
		{Name: MetricGPUMem, Type: metric.TypeGauge, Unit: "bytes", Description: "GPU memory used", RetentionDays: defaultRetentionDays},
		{Name: MetricGPUMemTotal, Type: metric.TypeGauge, Unit: "bytes", Description: "GPU memory total", RetentionDays: defaultRetentionDays},
		{Name: MetricGPUTemp, Type: metric.TypeGauge, Unit: "°C", Description: "GPU temperature", RetentionDays: defaultRetentionDays},
		{Name: MetricRAM, Type: metric.TypeGauge, Unit: "bytes", Description: "RAM used", RetentionDays: defaultRetentionDays},
		{Name: MetricRAMAvailable, Type: metric.TypeGauge, Unit: "bytes", Description: "RAM available", RetentionDays: defaultRetentionDays},
		{Name: MetricRAMCached, Type: metric.TypeGauge, Unit: "bytes", Description: "RAM cached", RetentionDays: defaultRetentionDays},
		{Name: MetricRAMBuffers, Type: metric.TypeGauge, Unit: "bytes", Description: "RAM buffers", RetentionDays: defaultRetentionDays},
		{Name: MetricRAMZswap, Type: metric.TypeGauge, Unit: "bytes", Description: "zswap compressed pool", RetentionDays: defaultRetentionDays},
		{Name: MetricSwap, Type: metric.TypeGauge, Unit: "bytes", Description: "Swap used", RetentionDays: defaultRetentionDays},
		{Name: MetricLoad, Type: metric.TypeGauge, Unit: "", Description: "System load average", RetentionDays: defaultRetentionDays},
		{Name: MetricDisk, Type: metric.TypeGauge, Unit: "bytes", Description: "Disk used (physical-mount sum)", RetentionDays: defaultRetentionDays},
		{Name: MetricDiskMountUsed, Type: metric.TypeGauge, Unit: "bytes", Description: "Per-mount used bytes (top 20)", RetentionDays: defaultRetentionDays},
		{Name: MetricDiskMountTotal, Type: metric.TypeGauge, Unit: "bytes", Description: "Per-mount total bytes (top 20)", RetentionDays: defaultRetentionDays},
		{Name: MetricNetIn, Type: metric.TypeGauge, Unit: "bytes/s", Description: "Network in rate", RetentionDays: defaultRetentionDays},
		{Name: MetricNetOut, Type: metric.TypeGauge, Unit: "bytes/s", Description: "Network out rate", RetentionDays: defaultRetentionDays},
		{Name: MetricNetTotalUp, Type: metric.TypeCounter, Unit: "bytes", Description: "Network total upload", RetentionDays: defaultRetentionDays},
		{Name: MetricNetTotalDown, Type: metric.TypeCounter, Unit: "bytes", Description: "Network total download", RetentionDays: defaultRetentionDays},
		{Name: MetricTCPOutSegs, Type: metric.TypeCounter, Unit: "count", Description: "Kernel TCP outbound segments", RetentionDays: defaultRetentionDays},
		{Name: MetricTCPRetransSegs, Type: metric.TypeCounter, Unit: "count", Description: "Kernel TCP retransmitted segments", RetentionDays: defaultRetentionDays},
		{Name: MetricTCPRetransPct, Type: metric.TypeGauge, Unit: "%", Description: "Interval TCP retransmit percentage (host-wide)", RetentionDays: defaultRetentionDays},
		{Name: MetricTrafficUp, Type: metric.TypeGauge, Unit: "bytes", Description: "Billing-cycle cumulative upload (last only; legacy history may have mixed semantics)", RetentionDays: defaultRetentionDays},
		{Name: MetricTrafficDown, Type: metric.TypeGauge, Unit: "bytes", Description: "Billing-cycle cumulative download (last only; legacy history may have mixed semantics)", RetentionDays: defaultRetentionDays},
		{Name: MetricTrafficIntervalUp, Type: metric.TypeGauge, Unit: "bytes", Description: "v2 validated upload interval delta; sum is additive; missing intervals are unknown", RetentionDays: defaultRetentionDays},
		{Name: MetricTrafficIntervalDown, Type: metric.TypeGauge, Unit: "bytes", Description: "v2 validated download interval delta; sum is additive; missing intervals are unknown", RetentionDays: defaultRetentionDays},
		{Name: MetricTrafficIntervalValid, Type: metric.TypeGauge, Unit: "ratio", Description: "v2 counter continuity: 1 validated interval, 0 unknown interval", RetentionDays: defaultRetentionDays},
		{Name: MetricProcess, Type: metric.TypeGauge, Unit: "count", Description: "Process count", RetentionDays: defaultRetentionDays},
		{Name: MetricConnections, Type: metric.TypeGauge, Unit: "count", Description: "TCP connections", RetentionDays: defaultRetentionDays},
		{Name: MetricConnectionsUDP, Type: metric.TypeGauge, Unit: "count", Description: "UDP connections", RetentionDays: defaultRetentionDays},
		{Name: MetricUptime, Type: metric.TypeGauge, Unit: "s", Description: "Node uptime in seconds (last sample; not a reboot count)", RetentionDays: defaultRetentionDays},
		{Name: MetricRestarts, Type: metric.TypeCounter, Unit: "count", Description: "Detected node restarts (cumulative; conservative uptime-decrease inference)", RetentionDays: defaultRetentionDays},
		{Name: MetricPingLatency, Type: metric.TypeGauge, Unit: "ms", Description: "Ping latency", RetentionDays: defaultRetentionDays},
		{Name: MetricPingSuccessLatency, Type: metric.TypeGauge, Unit: "ms", Description: "v2 successful Ping latency only; negative failures excluded", RetentionDays: defaultRetentionDays},
		{Name: MetricPingLoss, Type: metric.TypeGauge, Unit: "ratio", Description: "Per-attempt ping failure indicator (legacy 0/1)", RetentionDays: defaultRetentionDays},
		{Name: MetricPingAttempts, Type: metric.TypeGauge, Unit: "count", Description: "Probe attempts in this cycle", RetentionDays: defaultRetentionDays},
		{Name: MetricPingSuccess, Type: metric.TypeGauge, Unit: "count", Description: "Successful probe attempts in this cycle", RetentionDays: defaultRetentionDays},
		{Name: MetricPingLossPct, Type: metric.TypeGauge, Unit: "%", Description: "Cycle loss percentage (0-100); not a per-attempt 0/1", RetentionDays: defaultRetentionDays},
		{Name: MetricPingMin, Type: metric.TypeGauge, Unit: "ms", Description: "Minimum successful RTT in this cycle", RetentionDays: defaultRetentionDays},
		{Name: MetricPingAvg, Type: metric.TypeGauge, Unit: "ms", Description: "Average successful RTT in this cycle", RetentionDays: defaultRetentionDays},
		{Name: MetricPingMax, Type: metric.TypeGauge, Unit: "ms", Description: "Maximum successful RTT in this cycle", RetentionDays: defaultRetentionDays},
		{Name: MetricPingJitter, Type: metric.TypeGauge, Unit: "ms", Description: "RFC3550 instantaneous jitter in this cycle", RetentionDays: defaultRetentionDays},
		{Name: MetricHTTPDNS, Type: metric.TypeGauge, Unit: "ms", Description: "HTTP DNS phase", RetentionDays: defaultRetentionDays},
		{Name: MetricHTTPTCP, Type: metric.TypeGauge, Unit: "ms", Description: "HTTP TCP connect phase", RetentionDays: defaultRetentionDays},
		{Name: MetricHTTPTLS, Type: metric.TypeGauge, Unit: "ms", Description: "HTTP TLS handshake phase", RetentionDays: defaultRetentionDays},
		{Name: MetricHTTPTTFB, Type: metric.TypeGauge, Unit: "ms", Description: "HTTP time to first byte", RetentionDays: defaultRetentionDays},
		{Name: MetricHTTPTotal, Type: metric.TypeGauge, Unit: "ms", Description: "HTTP total attempt time", RetentionDays: defaultRetentionDays},
		{Name: MetricHTTPStatus, Type: metric.TypeGauge, Unit: "code", Description: "HTTP final status code", RetentionDays: defaultRetentionDays},
		{Name: MetricHTTPRedirects, Type: metric.TypeGauge, Unit: "count", Description: "HTTP redirect hops", RetentionDays: defaultRetentionDays},
		{Name: MetricHTTPTLSExpiryDays, Type: metric.TypeGauge, Unit: "days", Description: "Remaining days of the final verified leaf certificate", RetentionDays: defaultRetentionDays},
		{Name: MetricHTTPReachable, Type: metric.TypeGauge, Unit: "bool", Description: "TCP/TLS completed regardless of HTTP status", RetentionDays: defaultRetentionDays},
		{Name: MetricHTTPAvailable, Type: metric.TypeGauge, Unit: "bool", Description: "HTTP status matched the expected set", RetentionDays: defaultRetentionDays},
		{Name: MetricBackupAge, Type: metric.TypeGauge, Unit: "s", Description: "Seconds since the last successful backup", RetentionDays: defaultRetentionDays},
		{Name: MetricBackupOK, Type: metric.TypeGauge, Unit: "bool", Description: "Last backup succeeded (1) or not (0)", RetentionDays: defaultRetentionDays},
		{Name: MetricPanelRSS, Type: metric.TypeGauge, Unit: "bytes", Description: "Panel process resident memory (unavailable readings omitted)", RetentionDays: defaultBuiltinMetricRetentionDays},
		{Name: MetricPanelHeap, Type: metric.TypeGauge, Unit: "bytes", Description: "Panel process allocated Go heap", RetentionDays: defaultBuiltinMetricRetentionDays},
		{Name: MetricPanelGoroutines, Type: metric.TypeGauge, Unit: "count", Description: "Panel process goroutines", RetentionDays: defaultBuiltinMetricRetentionDays},
		{Name: MetricPanelMainWaitCount, Type: metric.TypeGauge, Unit: "count", Description: "Main pool waits per validated sample interval (-1 means unknown)", RetentionDays: defaultBuiltinMetricRetentionDays},
		{Name: MetricPanelMainWaitMS, Type: metric.TypeGauge, Unit: "ms", Description: "Main pool wait time per validated sample interval (-1 means unknown)", RetentionDays: defaultBuiltinMetricRetentionDays},
		{Name: MetricPanelWriteWaitCount, Type: metric.TypeGauge, Unit: "count", Description: "Metric write pool waits per validated sample interval (-1 means unknown)", RetentionDays: defaultBuiltinMetricRetentionDays},
		{Name: MetricPanelWriteWaitMS, Type: metric.TypeGauge, Unit: "ms", Description: "Metric write pool wait time per validated sample interval (-1 means unknown)", RetentionDays: defaultBuiltinMetricRetentionDays},
		{Name: MetricPanelReadWaitCount, Type: metric.TypeGauge, Unit: "count", Description: "Metric read pool waits per validated sample interval (-1 means unknown)", RetentionDays: defaultBuiltinMetricRetentionDays},
		{Name: MetricPanelReadWaitMS, Type: metric.TypeGauge, Unit: "ms", Description: "Metric read pool wait time per validated sample interval (-1 means unknown)", RetentionDays: defaultBuiltinMetricRetentionDays},
		{Name: MetricPanelThemeDisk, Type: metric.TypeGauge, Unit: "state", Description: "Last observed default-theme disk cache state; not continuous disk verification", RetentionDays: defaultBuiltinMetricRetentionDays},
		{Name: MetricPanelThemeMemory, Type: metric.TypeGauge, Unit: "bool", Description: "Default-theme in-memory fallback loaded in this process (0 or 1)", RetentionDays: defaultBuiltinMetricRetentionDays},
	}

	for _, def := range definitions {
		if IsPingMetric(def.Name) {
			def.RetentionDays = defaultPingMetricRetentionDays
		} else if IsPanelMetric(def.Name) {
			def.RetentionDays = defaultBuiltinMetricRetentionDays
		}
		companion := map[string]string{
			MetricTrafficIntervalUp: MetricTrafficUp, MetricTrafficIntervalDown: MetricTrafficDown,
			MetricTrafficIntervalValid: MetricTrafficUp,
			MetricPingSuccessLatency:   MetricPingLatency,
			MetricPingAttempts:         MetricPingLatency, MetricPingSuccess: MetricPingLatency,
			MetricPingLossPct: MetricPingLatency, MetricPingMin: MetricPingLatency,
			MetricPingAvg: MetricPingLatency, MetricPingMax: MetricPingLatency,
			MetricPingJitter:  MetricPingLatency,
			MetricCPUInterval: MetricCPU, MetricCPUCgroupUsage: MetricCPU,
			MetricCPUCgroupQuota: MetricCPU, MetricCPUCgroupThrottled: MetricCPU,
			MetricRAMAvailable: MetricRAM, MetricRAMCached: MetricRAM,
			MetricRAMBuffers: MetricRAM, MetricRAMZswap: MetricRAM,
			MetricDiskMountUsed: MetricDisk, MetricDiskMountTotal: MetricDisk,
			MetricTCPOutSegs: MetricNetIn, MetricTCPRetransSegs: MetricNetIn,
			MetricTCPRetransPct: MetricNetIn,
			MetricHTTPDNS:       MetricPingLatency, MetricHTTPTCP: MetricPingLatency,
			MetricHTTPTLS: MetricPingLatency, MetricHTTPTTFB: MetricPingLatency,
			MetricHTTPTotal: MetricPingLatency, MetricHTTPStatus: MetricPingLatency,
			MetricHTTPRedirects: MetricPingLatency, MetricHTTPTLSExpiryDays: MetricPingLatency,
			MetricHTTPReachable: MetricPingLatency, MetricHTTPAvailable: MetricPingLatency,
		}[def.Name]
		if companion != "" {
			base, err := s.GetMetric(ctx, companion)
			if err == nil {
				def.RetentionDays = base.RetentionDays
			} else if !errors.Is(err, metric.ErrNotFound) {
				return err
			}
		}
		existing, err := s.GetMetric(ctx, def.Name)
		if err != nil && !errors.Is(err, metric.ErrNotFound) {
			return fmt.Errorf("failed to get metric %s: %w", def.Name, err)
		}
		if err == nil {
			if existing.RetentionDays == 0 {
				continue
			}
			def.RetentionDays = existing.RetentionDays
		}
		if err := s.UpsertMetric(ctx, def); err != nil {
			return fmt.Errorf("failed to create metric %s: %w", def.Name, err)
		}
	}
	// Warm the immutable snapshot before a nonblocking admitted write can use
	// it. Later definition edits invalidate it as usual; ordinary reads/writes
	// repopulate it without making a fenced result wait on SQL.
	_, err := s.ListMetrics(ctx)
	return err
}
