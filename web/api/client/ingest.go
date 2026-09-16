package client

import (
	"context"
	"time"

	"github.com/Aone2233/nekomari/database/clients"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/database/tasks"
	"github.com/Aone2233/nekomari/internal/metricstore"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
	agent_runtime "github.com/Aone2233/nekomari/web/agent"
)

// ingest.go
// agent 上报数据的传输无关处理逻辑。v2 的 HTTP/WebSocket JSON-RPC 入口
// 经过协议解析后，统一调用这里的函数落库并更新运行时状态。

// ingestReport 保存一次负载上报并刷新运行时状态。
// markPresence 为 true 时按 POST 上报会话刷新在线状态（WS 连接自行管理在线状态，应传 false）。
func ingestReport(uuid string, report v2.Report, markPresence bool) error {
	report.UUID = uuid
	report.UpdatedAt = time.Now().UTC()
	if err := clients.ReportVerify(report); err != nil {
		return err
	}
	savedReport, err := metricstore.WriteReport(context.Background(), report)
	if err != nil {
		return err
	}
	agent_runtime.RecordReport(savedReport)
	agent_runtime.MarkV2Client(uuid)
	if markPresence {
		refreshPostPresence(uuid)
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

// ingestPingResult 保存一条 ping 探测结果。
// pingType 是实际使用的探测协议（icmp/tcp/http）；双协议并列探测时
// 同一任务会先后上报多条不同协议的结果，必须保留该字段以示区分。
func ingestPingResult(uuid string, taskID uint, pingType, role string, value int) error {
	return tasks.SavePingRecord(models.PingRecord{
		Client:   uuid,
		TaskId:   taskID,
		PingType: pingType,
		Role:     role,
		Value:    value,
		Time:     time.Now().UTC(),
	})
}
