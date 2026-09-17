package notifier

// ping_alert.go —— 延迟监测（ping）的告警求值
//
// 为什么需要它：在此之前，ping 结果只进图表，不进任何通知通道 ——
// 一个任务 100% 丢包跑三天也不会有任何提示。主机指标（cpu/ram/…）早就有
// LoadNotification 这条通道，ping 却没有。
//
// 实现上刻意【复用 LoadNotification 规则体系】而不是新造一套：模型、增删改查、
// 调度器、通知派发、前端页面全部共用，这里只补一个求值分支。
// 规则通过 Metric = ping_latency / ping_loss 与 Tasks 字段进入这条路径。

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/database/tasks"
	logger "github.com/Aone2233/nekomari/utils/log"
)

const (
	// PingAlertMetricLatency 以平均延迟（毫秒）为判据。
	PingAlertMetricLatency = "ping_latency"
	// PingAlertMetricLoss 以丢包率（百分比）为判据。
	PingAlertMetricLoss = "ping_loss"
)

// IsPingAlertMetric 判断该规则是否走 ping 求值路径。
func IsPingAlertMetric(metric string) bool {
	return metric == PingAlertMetricLatency || metric == PingAlertMetricLoss
}

// pingMetricSeries 把窗口内的记录转成逐样本序列，供基线计算使用。
//
// latency：只取未丢包的样本（丢包没有延迟可言，混进去会把基线拉低）
// loss：丢包记为 100，正常记为 0
func pingMetricSeries(recs []models.PingRecord, metric string) ([]float32, bool) {
	if len(recs) == 0 {
		return nil, false
	}
	values := make([]float32, 0, len(recs))
	for _, r := range recs {
		switch metric {
		case PingAlertMetricLatency:
			if r.Value < 0 {
				continue // 丢包样本不参与延迟统计
			}
			values = append(values, float32(r.Value))
		case PingAlertMetricLoss:
			if r.Value < 0 {
				values = append(values, 100)
			} else {
				values = append(values, 0)
			}
		}
	}
	if len(values) == 0 {
		return nil, false
	}
	return values, true
}

// pingMetricValue 把整个窗口归约成一个用于比较的数值。
//
// latency -> 窗口内平均延迟（毫秒）
// loss    -> 窗口内丢包率（百分比）
//
// 用整窗聚合而不是逐样本比阈值：ping 的延迟抖动本来就大，逐样本判会一路误报；
// 「这段时间平均多少、丢了多少」才是运维真正关心的。
func pingMetricValue(recs []models.PingRecord, metric string) (float32, bool) {
	series, ok := pingMetricSeries(recs, metric)
	if !ok {
		return 0, false
	}
	var sum float32
	for _, v := range series {
		sum += v
	}
	return sum / float32(len(series)), true
}

// evaluatePingRules 对规则的每个 ping 任务 × 每台运行它的服务器求值，
// 返回应当告警的服务器集合。
func evaluatePingRules(task models.LoadNotification, now, windowStart time.Time) []string {
	taskIndex, err := pingTaskIndex()
	if err != nil {
		logger.Errorf("notifier", "Failed to load ping tasks for rule %d: %v", task.Id, err)
		return nil
	}

	alerted := map[string]bool{}
	for _, raw := range task.Tasks {
		id, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || id <= 0 {
			continue
		}
		pingTask, ok := taskIndex[uint(id)]
		if !ok {
			// 任务已被删除：跳过而不是当成异常，否则规则会永远报错。
			continue
		}

		// 该任务由哪些服务器执行
		clients := pingTask.Clients
		if len(task.Clients) > 0 {
			// 规则里显式选了服务器时以规则为准，便于只盯其中几台。
			allowed := make(map[string]bool, len(task.Clients))
			for _, c := range task.Clients {
				allowed[c] = true
			}
			filtered := make([]string, 0, len(clients))
			for _, c := range clients {
				if allowed[c] {
					filtered = append(filtered, c)
				}
			}
			clients = filtered
		}

		for _, uuid := range clients {
			recs, err := tasks.GetPingRecords(uuid, id, windowStart, now)
			if err != nil || len(recs) == 0 {
				continue
			}
			value, ok := pingMetricValue(recs, task.Metric)
			if !ok {
				continue
			}

			threshold := task.Threshold
			if task.UsesBaseline() {
				// 基线窗口排除当前窗口，否则正在发生的异常会抬高自己的基线。
				history, err := tasks.GetPingRecords(uuid, id, windowStart.Add(-task.BaselineWindow()), windowStart)
				if err != nil {
					continue
				}
				histSeries, ok := pingMetricSeries(history, task.Metric)
				if !ok {
					continue
				}
				base, ok := computeBaselineThreshold(histSeries, task.EffectiveMultiplier(), task.Threshold)
				if !ok {
					// 历史样本不足：宁可不报，也不用凭空造出来的阈值比较。
					continue
				}
				threshold = base
			}

			if value >= threshold {
				alerted[uuid] = true
			}
		}
	}

	out := make([]string, 0, len(alerted))
	for uuid := range alerted {
		out = append(out, uuid)
	}
	sort.Strings(out)
	return out
}

// pingTaskIndex 返回 task_id -> PingTask 的索引。
func pingTaskIndex() (map[uint]models.PingTask, error) {
	list, err := tasks.GetAllPingTasks()
	if err != nil {
		return nil, err
	}
	index := make(map[uint]models.PingTask, len(list))
	for _, t := range list {
		index[t.Id] = t
	}
	return index, nil
}
