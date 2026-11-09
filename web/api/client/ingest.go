package client

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Aone2233/nekomari/database/clients"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/metricstore"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
	logger "github.com/Aone2233/nekomari/utils/log"
	agent_runtime "github.com/Aone2233/nekomari/web/agent"
)

// ingest.go
// agent 上报数据的传输无关处理逻辑。v2 的 HTTP/WebSocket JSON-RPC 入口
// 经过协议解析后，统一调用这里的函数落库并更新运行时状态。

// ingestReport 保存一次负载上报并刷新运行时状态。
// markPresence 为 true 时按 POST 上报会话刷新在线状态（WS 连接自行管理在线状态，应传 false）。
func ingestReport(uuid string, report v2.Report, markPresence bool) error {
	return ingestReportContext(context.Background(), uuid, report, markPresence)
}

func ingestReportContext(ctx context.Context, uuid string, report v2.Report, markPresence bool) error {
	report.UUID = uuid
	if err := normalizeReportTimestamp(&report, time.Now()); err != nil {
		return err
	}
	clamped, err := clients.NormalizeReportResources(&report)
	if err != nil {
		return err
	}
	savedReport, err := metricstore.WriteReport(ctx, report)
	if err != nil {
		return err
	}
	agent_runtime.RecordReport(savedReport)
	if fields := strings.Join(clamped, ","); agent_runtime.NoteReportClamp(uuid, fields) {
		logger.Warnf("client-api", "Clamped resource used to total for client %s: %s", uuid, fields)
	}
	agent_runtime.MarkV2Client(uuid)
	if markPresence {
		refreshPostPresence(uuid)
	}
	return nil
}

func normalizeReportTimestamp(report *v2.Report, receivedAt time.Time) error {
	report.ReceivedAt = receivedAt.UTC()
	if report.SampledAt.IsZero() {
		report.UpdatedAt = report.ReceivedAt
		if report.Quality == nil {
			report.Quality = make(map[string]string)
		}
		report.Quality["timestamp"] = "server_received_legacy"
	} else {
		// Reject implausible clocks rather than silently moving measured samples.
		if report.SampledAt.After(report.ReceivedAt.Add(30*time.Second)) || report.SampledAt.Before(report.ReceivedAt.Add(-5*time.Minute)) {
			return fmt.Errorf("sampled_at is outside the accepted clock window")
		}
		report.UpdatedAt = report.SampledAt.UTC()
	}
	return nil
}

// ingestBasicInfo 保存客户端基础信息。fallbackIP 在上报未携带 IP 时用作兜底。
func ingestBasicInfo(uuid string, info map[string]interface{}, fallbackIP string) error {
	if info == nil {
		info = map[string]interface{}{}
	}
	return saveClientBasicInfo(info, uuid, fallbackIP)
}

// newPingRecord 把一条上报结果组装成 PingRecord。
//
// 单独成函数是为了可测：ValueUS 与 Value 的共存关系（微秒精度优先、0 表示旧 agent）
// 是这次改动的契约，而落库与聚合那一侧由 metricstore 的用例覆盖。
func newPingRecord(uuid string, taskID uint, pingType, role, family string, value int, valueUS int64, at time.Time) models.PingRecord {
	return newPingRecordWithStats(uuid, taskID, pingType, role, family, value, valueUS, nil, at)
}

// pingStatsFromParams returns nil for a legacy single-probe payload, so the
// metric writer can keep the exact old point set.
func pingStatsFromParams(params v2.PingResultParams) *v2.PingProbeStats {
	if params.Attempts == nil && params.Success == nil && params.LossPct == nil &&
		params.MinUS == nil && params.AvgUS == nil && params.MaxUS == nil &&
		params.JitterUS == nil && params.HTTP == nil &&
		params.ResolvedIP == "" && params.ResolvedFamily == "" && params.ProbeMode == "" {
		return nil
	}
	return &v2.PingProbeStats{
		Attempts: params.Attempts, Success: params.Success, LossPct: params.LossPct,
		MinUS: params.MinUS, AvgUS: params.AvgUS, MaxUS: params.MaxUS,
		JitterUS: params.JitterUS, HTTP: params.HTTP,
		ResolvedIP: params.ResolvedIP, ResolvedFamily: params.ResolvedFamily,
		ProbeMode: params.ProbeMode,
	}
}

// newPingRecordWithStats carries the additive v1.8 probe summary. A nil stats
// pointer is the exact legacy record shape.
func newPingRecordWithStats(uuid string, taskID uint, pingType, role, family string, value int, valueUS int64, stats *v2.PingProbeStats, at time.Time) models.PingRecord {
	rec := models.PingRecord{
		Client:   uuid,
		TaskId:   taskID,
		PingType: pingType,
		Role:     role,
		Family:   family,
		Value:    value,
		ValueUS:  valueUS,
		Time:     at,
	}
	if stats == nil {
		return rec
	}
	if stats.Attempts != nil {
		rec.Attempts = *stats.Attempts
	}
	if stats.Success != nil {
		rec.Success = *stats.Success
	}
	if stats.LossPct != nil {
		rec.LossPct = *stats.LossPct
	}
	if stats.MinUS != nil {
		rec.MinUS = *stats.MinUS
	}
	if stats.AvgUS != nil {
		rec.AvgUS = *stats.AvgUS
	}
	if stats.MaxUS != nil {
		rec.MaxUS = *stats.MaxUS
	}
	if stats.JitterUS != nil {
		rec.JitterUS = *stats.JitterUS
	}
	if http := stats.HTTP; http != nil {
		rec.HTTP = &models.PingHTTPObservation{
			DNSUS: http.DNSUS, TCPUS: http.TCPUS, TLSUS: http.TLSUS,
			TTFBUS: http.TTFBUS, TotalUS: http.TotalUS,
			StatusCode: http.StatusCode, Redirects: http.Redirects,
			TLSExpiryDays: http.TLSExpiryDays,
			Reachable:     http.Reachable, Available: http.Available,
		}
	}
	return rec
}
