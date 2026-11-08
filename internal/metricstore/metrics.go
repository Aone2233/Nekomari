package metricstore

const (
	MetricCPU                  = "cpu.usage"
	MetricGPU                  = "gpu.usage"
	MetricGPUDeviceUsage       = "gpu.device.usage"
	MetricGPUMem               = "gpu.memory.used"
	MetricGPUMemTotal          = "gpu.memory.total"
	MetricGPUTemp              = "gpu.temperature"
	MetricRAM                  = "memory.used"
	MetricSwap                 = "swap.used"
	MetricLoad                 = "load.average"
	MetricDisk                 = "disk.used"
	MetricNetIn                = "net.in.rate"
	MetricNetOut               = "net.out.rate"
	MetricNetTotalUp           = "net.total.up"
	MetricNetTotalDown         = "net.total.down"
	MetricTrafficUp            = "traffic.up"
	MetricTrafficDown          = "traffic.down"
	MetricTrafficIntervalUp    = "traffic.interval.up"
	MetricTrafficIntervalDown  = "traffic.interval.down"
	MetricTrafficIntervalValid = "traffic.interval.valid"
	MetricCPUInterval          = "cpu.interval_seconds"
	MetricCPUCgroupUsage       = "cpu.cgroup.usage"
	MetricCPUCgroupQuota       = "cpu.cgroup.quota_cores"
	MetricCPUCgroupThrottled   = "cpu.cgroup.throttled_seconds_total"
	MetricRAMAvailable         = "ram.available"
	MetricRAMCached            = "ram.cached"
	MetricRAMBuffers           = "ram.buffers"
	MetricRAMZswap             = "ram.zswap"
	MetricDiskMountUsed        = "disk.mount.used"
	MetricDiskMountTotal       = "disk.mount.total"
	MetricTCPOutSegs           = "tcp.outsegs"
	MetricTCPRetransSegs       = "tcp.retranssegs"
	MetricTCPRetransPct        = "tcp.retrans_pct"
	MetricProcess              = "process.count"
	MetricConnections          = "connections.tcp"
	MetricConnectionsUDP       = "connections.udp"
	MetricUptime               = "system.uptime_seconds"
	MetricRestarts             = "system.restarts"
	MetricPingLatency          = "ping.latency_ms"
	MetricPingSuccessLatency   = "ping.success_latency_ms"
	MetricPingLoss             = "ping.loss"
	MetricPingAttempts         = "ping.attempts"
	MetricPingSuccess          = "ping.success"
	MetricPingLossPct          = "ping.loss_pct"
	MetricPingMin              = "ping.min_ms"
	MetricPingAvg              = "ping.avg_ms"
	MetricPingMax              = "ping.max_ms"
	MetricPingJitter           = "ping.jitter_ms"
	MetricHTTPDNS              = "http.dns_ms"
	MetricHTTPTCP              = "http.tcp_ms"
	MetricHTTPTLS              = "http.tls_ms"
	MetricHTTPTTFB             = "http.ttfb_ms"
	MetricHTTPTotal            = "http.total_ms"
	MetricHTTPStatus           = "http.status_code"
	MetricHTTPRedirects        = "http.redirects"
	MetricHTTPTLSExpiryDays    = "http.tls_expiry_days"
	MetricHTTPReachable        = "http.reachable"
	MetricHTTPAvailable        = "http.available"
	MetricBackupAge            = "backup.age_seconds"
	MetricBackupOK             = "backup.ok"
	MetricPanelRSS             = "panel.self.rss_bytes"
	MetricPanelHeap            = "panel.self.heap_alloc_bytes"
	MetricPanelGoroutines      = "panel.self.goroutines"
	MetricPanelMainWaitCount   = "panel.pool.main.wait_count"
	MetricPanelMainWaitMS      = "panel.pool.main.wait_ms"
	MetricPanelWriteWaitCount  = "panel.pool.metrics_write.wait_count"
	MetricPanelWriteWaitMS     = "panel.pool.metrics_write.wait_ms"
	MetricPanelReadWaitCount   = "panel.pool.metrics_read.wait_count"
	MetricPanelReadWaitMS      = "panel.pool.metrics_read.wait_ms"
	MetricPanelThemeDisk       = "panel.theme_cache.disk_state"
	MetricPanelThemeMemory     = "panel.theme_cache.memory_loaded"
)

// loadRecordMetricNames are the entity-level metrics used to reconstruct the
// legacy Record response shape.
var loadRecordMetricNames = []string{
	MetricCPU, MetricGPU, MetricRAM, MetricSwap, MetricLoad, MetricDisk, MetricNetIn, MetricNetOut,
	MetricNetTotalUp, MetricNetTotalDown, MetricTrafficUp, MetricTrafficDown,
	MetricProcess, MetricConnections, MetricConnectionsUDP,
	MetricBackupAge, MetricBackupOK,
}

// gpuDeviceRecordMetricNames are stored separately from the entity-level GPU
// average and are included when deleting all system records.
var gpuDeviceRecordMetricNames = []string{
	MetricGPUDeviceUsage, MetricGPUMem, MetricGPUMemTotal, MetricGPUTemp,
}

var recordMetricNames = joinMetricNames(loadRecordMetricNames, gpuDeviceRecordMetricNames, []string{
	MetricTrafficIntervalUp, MetricTrafficIntervalDown, MetricTrafficIntervalValid, MetricUptime, MetricRestarts,
	MetricCPUInterval, MetricCPUCgroupUsage, MetricCPUCgroupQuota, MetricCPUCgroupThrottled,
	MetricRAMAvailable, MetricRAMCached, MetricRAMBuffers, MetricRAMZswap,
	MetricDiskMountUsed, MetricDiskMountTotal,
	MetricTCPOutSegs, MetricTCPRetransSegs, MetricTCPRetransPct,
})

// Ping has an independent retention and cleanup boundary.
var pingMetricNames = []string{
	MetricPingLatency, MetricPingLoss, MetricPingSuccessLatency,
	MetricPingAttempts, MetricPingSuccess, MetricPingLossPct,
	MetricPingMin, MetricPingAvg, MetricPingMax, MetricPingJitter,
	MetricHTTPDNS, MetricHTTPTCP, MetricHTTPTLS, MetricHTTPTTFB, MetricHTTPTotal,
	MetricHTTPStatus, MetricHTTPRedirects, MetricHTTPTLSExpiryDays,
	MetricHTTPReachable, MetricHTTPAvailable,
}

var panelPoolMetricNames = []string{
	MetricPanelMainWaitCount, MetricPanelMainWaitMS,
	MetricPanelWriteWaitCount, MetricPanelWriteWaitMS,
	MetricPanelReadWaitCount, MetricPanelReadWaitMS,
}

var panelMetricNames = joinMetricNames([]string{MetricPanelRSS, MetricPanelHeap, MetricPanelGoroutines}, panelPoolMetricNames,
	[]string{MetricPanelThemeDisk, MetricPanelThemeMemory})

var builtinMetricNames = joinMetricNames(recordMetricNames, pingMetricNames, panelMetricNames)

// IsPingMetric reports whether a metric belongs to the ping retention family.
func IsPingMetric(name string) bool {
	for _, metricName := range pingMetricNames {
		if name == metricName {
			return true
		}
	}
	return false
}

// Panel metrics are private even when requested through the public node API.
func IsPanelMetric(name string) bool {
	for _, metricName := range panelMetricNames {
		if name == metricName {
			return true
		}
	}
	return false
}

func IsPanelPoolMetric(name string) bool {
	for _, metricName := range panelPoolMetricNames {
		if name == metricName {
			return true
		}
	}
	return false
}

func metricNameForRecordField(name string) (string, bool) {
	switch name {
	case "cpu":
		return MetricCPU, true
	case "gpu":
		return MetricGPU, true
	case "ram":
		return MetricRAM, true
	case "swap":
		return MetricSwap, true
	case "load":
		return MetricLoad, true
	case "disk":
		return MetricDisk, true
	case "net_in", "netin":
		return MetricNetIn, true
	case "net_out", "netout":
		return MetricNetOut, true
	case "net_total_up":
		return MetricNetTotalUp, true
	case "net_total_down":
		return MetricNetTotalDown, true
	case "traffic_up":
		return MetricTrafficUp, true
	case "traffic_down":
		return MetricTrafficDown, true
	case "process":
		return MetricProcess, true
	case "connections":
		return MetricConnections, true
	case "connections_udp":
		return MetricConnectionsUDP, true
	case "backup_age":
		return MetricBackupAge, true
	case "backup_ok":
		return MetricBackupOK, true
	default:
		return "", false
	}
}

func joinMetricNames(groups ...[]string) []string {
	total := 0
	for _, group := range groups {
		total += len(group)
	}
	names := make([]string, 0, total)
	for _, group := range groups {
		names = append(names, group...)
	}
	return names
}
