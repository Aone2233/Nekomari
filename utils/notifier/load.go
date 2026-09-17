package notifier

import (
	"fmt"
	"math"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/Aone2233/nekomari/database/clients"
	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	messageevent "github.com/Aone2233/nekomari/database/models/messageEvent"
	"github.com/Aone2233/nekomari/database/records"
	"github.com/Aone2233/nekomari/internal/scheduler"
	logger "github.com/Aone2233/nekomari/utils/log"
	"github.com/Aone2233/nekomari/utils/messageSender"
)

// LoadNotificationService 管理定时器和任务
type LoadNotificationService struct {
	mu    sync.Mutex
	tasks map[int][]models.LoadNotification
}

var LoadNotificationManager = &LoadNotificationService{
	tasks: make(map[int][]models.LoadNotification),
}

// Reload 重载时间表
func (m *LoadNotificationService) Reload(loadNotifications []models.LoadNotification) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	scheduler.RemovePrefix("load-notification:")
	m.tasks = make(map[int][]models.LoadNotification)

	// 按Interval分组任务
	taskGroups := make(map[int][]models.LoadNotification)
	for _, task := range loadNotifications {
		taskGroups[task.Interval] = append(taskGroups[task.Interval], task)
	}

	// 为每个唯一的Interval创建定时器
	for interval, tasks := range taskGroups {
		interval := interval
		tasks := append([]models.LoadNotification(nil), tasks...)
		m.tasks[interval] = tasks
		if err := scheduler.AddFunc(fmt.Sprintf("load-notification:%d", interval), scheduler.Every(time.Duration(interval)*time.Minute), func() {
			for _, task := range tasks {
				go executeLoadNotificationTask(task)
			}
		}); err != nil {
			return err
		}
	}

	return nil
}

// executeLoadNotificationTask 执行单个LoadNotificationTask
func executeLoadNotificationTask(task models.LoadNotification) {
	// 检查是否在冷却期内
	if shouldSkipNotification(task) {
		return
	}

	now := time.Now().UTC()
	windowStart := now.Add(-time.Duration(task.Interval) * time.Minute)

	// ping 指标走独立求值分支：数据来自延迟监测任务的记录，而不是主机指标。
	// 规则本身、调度器与通知派发都与主机指标共用（见 ping_alert.go）。
	if IsPingAlertMetric(task.Metric) {
		sendLoadNotification(evaluatePingRules(task, now, windowStart), task)
		updateLastNotified(task.Id, now)
		return
	}

	overloadClients := make([]string, 0)
	for _, clientUUID := range task.Clients {
		// 仅查询当前通知使用的指标，避免重建完整监控记录。
		records, err := getMetricRecordsForClient(clientUUID, task.Metric, windowStart, now)
		if err != nil {
			continue
		}

		// 取阈值：固定模式直接用配置值；基线模式用该客户端自身的历史基线。
		threshold := task.Threshold
		if task.UsesBaseline() {
			base, ok := resolveBaselineThreshold(clientUUID, task, now, windowStart)
			if !ok {
				// 历史样本不足，宁可不报，也不要凭空造一个阈值出来。
				continue
			}
			threshold = base
		}

		// 检查指标是否达到阈值
		if checkMetricThresholdAt(records, task, threshold) {
			overloadClients = append(overloadClients, clientUUID)
		}

	}
	sendLoadNotification(overloadClients, task)
	updateLastNotified(task.Id, now)
}

// resolveBaselineThreshold 用「当前窗口之前」的历史样本算出异常阈值。
//
// 关键点：基线窗口必须排除当前窗口，否则正在发生的异常会把自己的基线抬高，
// 导致越异常越不报警。
func resolveBaselineThreshold(clientUUID string, task models.LoadNotification, now, windowStart time.Time) (float32, bool) {
	historyStart := windowStart.Add(-task.BaselineWindow())
	history, err := getMetricRecordsForClient(clientUUID, task.Metric, historyStart, windowStart)
	if err != nil || len(history) == 0 {
		return 0, false
	}
	values := make([]float32, 0, len(history))
	for _, record := range history {
		values = append(values, getMetricValue(record, task.Metric))
	}
	return computeBaselineThreshold(values, task.EffectiveMultiplier(), task.Threshold)
}

// computeBaselineThreshold 由历史样本算出阈值 = max(P95 × multiplier, floor)。
//
// 用 P95 而不是平均值：平均会被偶发尖峰拉高，P95 更贴近「日常水位」，
// 同时对持续劣化仍然敏感。
// floor 就是配置里的 Threshold —— 在基线模式下退化为下限，用来避免
// 基线接近 0 时（例如丢包率平时为 0）产生噪声告警；也避免「基线为 0 时
// 乘任何倍数都是 0」这种退化情形。
//
// 返回 ok=false 表示样本不足，调用方应当跳过而不是用 0 去比较。
func computeBaselineThreshold(values []float32, multiplier, floor float32) (float32, bool) {
	// 样本太少时 P95 没有统计意义（3~4 个点会直接退化成最大值）。
	const minSamples = 8
	if len(values) < minSamples {
		return 0, false
	}
	if multiplier <= 0 {
		multiplier = 3
	}
	threshold := percentile(values, 0.95) * multiplier
	if threshold < floor {
		threshold = floor
	}
	// 基线与下限都为 0 时无法形成有意义的判定条件。
	if threshold <= 0 {
		return 0, false
	}
	return threshold, true
}

// percentile 返回 p 分位数（0<=p<=1），使用最近秩法，不修改入参顺序。
func percentile(values []float32, p float64) float32 {
	if len(values) == 0 {
		return 0
	}
	if p < 0 {
		p = 0
	}
	if p > 1 {
		p = 1
	}
	sorted := append([]float32(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := int(math.Ceil(p*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// shouldSkipNotification 检查是否应该跳过通知（冷却期检查）
func shouldSkipNotification(task models.LoadNotification) bool {
	if task.LastNotified == nil || task.LastNotified.IsZero() {
		return false
	}

	// 计算冷却期（使用 interval 作为冷却期）
	cooldownPeriod := time.Duration(task.Interval) * time.Minute
	timeSinceLastNotified := time.Since(*task.LastNotified)

	return timeSinceLastNotified < cooldownPeriod
}

// getMetricRecordsForClient 获取指定客户端在时间窗口内的单项指标最大值。
func getMetricRecordsForClient(clientUUID, metricName string, start, end time.Time) ([]models.Record, error) {
	return records.GetRecordMetricMaxByClientAndTime(clientUUID, metricName, start, end)
}

// checkMetricThreshold 检查指标是否达到【任务自身配置的】阈值。
func checkMetricThreshold(records []models.Record, task models.LoadNotification) bool {
	return checkMetricThresholdAt(records, task, task.Threshold)
}

// checkMetricThresholdAt 检查指标是否达到给定阈值（基线模式下阈值由历史算出）。
func checkMetricThresholdAt(records []models.Record, task models.LoadNotification, threshold float32) bool {
	if len(records) == 0 {
		return false
	}

	// 计算需要达标的最小记录数
	minRequiredRecords := int(float32(len(records)) * task.Ratio)
	if minRequiredRecords == 0 {
		minRequiredRecords = 1
	}

	exceededCount := 0

	for _, record := range records {
		metricValue := getMetricValue(record, task.Metric)
		if metricValue >= threshold {
			exceededCount++
		}
	}

	return exceededCount >= minRequiredRecords
}

// getMetricValue 根据指标名称获取记录中的对应值
func getMetricValue(record models.Record, metric string) float32 {
	switch metric {
	case "cpu":
		return record.Cpu
	case "gpu":
		return record.Gpu
	case "net_in", "netin":
		return bytesPerSecondToMbps(record.NetIn)
	case "net_out", "netout":
		return bytesPerSecondToMbps(record.NetOut)
	case "ram":
		client, err := clients.GetClientByUUID(record.Client) // 确保客户端信息已加载
		if err != nil {
			logger.Errorf("notifier", "Failed to get client info for %s: %v", record.Client, err)
			return 0
		}
		if client.MemTotal > 0 {
			return float32(record.Ram) / float32(client.MemTotal) * 100
		}
		return 0
	case "swap":
		client, err := clients.GetClientByUUID(record.Client) // 确保客户端信息已加载
		if err != nil {
			logger.Errorf("notifier", "Failed to get client info for %s: %v", record.Client, err)
			return 0
		}
		if client.SwapTotal > 0 {
			return float32(record.Swap) / float32(client.SwapTotal) * 100
		}
		return 0
	case "load":
		return record.Load
	case "temp":
		return record.Temp
	// 备份新鲜度：由 agent 读备份状态文件上报（见 agent/monitoring/backup.go）。
	// 不依赖反射而写显式分支，因为 UI 传的是 snake_case（backup_age），
	// 而反射要找的是 Go 字段名（BackupAge）。
	case "backup_age":
		return float32(record.BackupAge)
	case "backup_ok":
		return float32(record.BackupOk)
	case "disk":
		client, err := clients.GetClientByUUID(record.Client) // 确保客户端信息已加载
		if err != nil {
			logger.Errorf("notifier", "Failed to get client info for %s: %v", record.Client, err)
			return 0
		}
		if client.DiskTotal > 0 {
			return float32(record.Disk) / float32(client.DiskTotal) * 100
		}
		return 0
	default:
		// 尝试通过反射获取字段值
		v := reflect.ValueOf(record)
		field := v.FieldByName(metric)
		if field.IsValid() && field.CanInterface() {
			switch field.Kind() {
			case reflect.Float32:
				return float32(field.Float())
			case reflect.Float64:
				return float32(field.Float())
			case reflect.Int, reflect.Int32, reflect.Int64:
				return float32(field.Int())
			}
		}
		return 0
	}
}

func bytesPerSecondToMbps(bytesPerSecond int64) float32 {
	if bytesPerSecond <= 0 {
		return 0
	}

	// 采用十进制 Mbps：1 Mbps = 1,000,000 bit/s
	return float32(float64(bytesPerSecond) * 8 / 1_000_000)
}

// sendLoadNotification 发送负载通知
func sendLoadNotification(clientUUIDs []string, task models.LoadNotification) {
	if len(clientUUIDs) == 0 {
		return
	}
	eventClients := make([]models.Client, 0, len(clientUUIDs))
	for _, uuid := range clientUUIDs {
		eventClients = append(eventClients, models.Client{UUID: uuid})
	}
	go func() {
		if err := messageSender.SendNotification(models.EventMessage{
			Event:   messageevent.Alert,
			Clients: eventClients,
			Time:    time.Now().UTC(),
			Emoji:   "⚠️",
			Message: task.Name,
		}); err != nil {
			logger.Errorf("notifier", "Failed to send load notification for task %d: %v", task.Id, err)
		}
	}()
}

// updateLastNotified 更新最后通知时间
func updateLastNotified(taskId uint, notifyTime time.Time) {
	db := dbcore.GetDBInstance()
	if err := db.Model(&models.LoadNotification{}).Where("id = ?", taskId).Update("last_notified", notifyTime.UTC()).Error; err != nil {
		logger.Errorf("notifier", "Failed to update last_notified for task %d: %v", taskId, err)
	}
}

// ReloadLoadNotificationSchedule 加载或重载时间表
func ReloadLoadNotificationSchedule(loadNotifications []models.LoadNotification) error {
	return LoadNotificationManager.Reload(loadNotifications)
}
