package notifier

import (
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/models"
)

func TestIsPingAlertMetric(t *testing.T) {
	if !IsPingAlertMetric(PingAlertMetricLatency) || !IsPingAlertMetric(PingAlertMetricLoss) {
		t.Error("ping_latency / ping_loss 应被识别为 ping 指标")
	}
	for _, m := range []string{"cpu", "ram", "backup_age", ""} {
		if IsPingAlertMetric(m) {
			t.Errorf("%q 不应被当作 ping 指标", m)
		}
	}
}

// ping 记录：Value < 0 表示丢包，否则是毫秒延迟。
func recsFor(values ...int) []models.PingRecord {
	out := make([]models.PingRecord, 0, len(values))
	base := time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)
	for i, v := range values {
		out = append(out, models.PingRecord{Value: v, Time: base.Add(time.Duration(i) * time.Minute)})
	}
	return out
}

func TestPingMetricSeries(t *testing.T) {
	// 延迟：丢包样本不参与，否则会把平均拉低
	lat, ok := pingMetricSeries(recsFor(10, -1, 20, -1, 30), PingAlertMetricLatency)
	if !ok {
		t.Fatal("应能取出序列")
	}
	if len(lat) != 3 {
		t.Fatalf("延迟序列应跳过丢包样本，实际 %d 个: %v", len(lat), lat)
	}
	for i, want := range []float32{10, 20, 30} {
		if lat[i] != want {
			t.Errorf("lat[%d] = %v, want %v", i, lat[i], want)
		}
	}

	// 丢包：每个样本映射成 100/0
	loss, ok := pingMetricSeries(recsFor(10, -1, 20, -1, 30), PingAlertMetricLoss)
	if !ok {
		t.Fatal("应能取出序列")
	}
	if len(loss) != 5 {
		t.Fatalf("丢包序列应保留全部样本，实际 %d 个: %v", len(loss), loss)
	}
	for i, want := range []float32{0, 100, 0, 100, 0} {
		if loss[i] != want {
			t.Errorf("loss[%d] = %v, want %v", i, loss[i], want)
		}
	}

	// 空输入
	if _, ok := pingMetricSeries(nil, PingAlertMetricLoss); ok {
		t.Error("空记录应返回 ok=false")
	}
	// 全部丢包时延迟序列为空
	if _, ok := pingMetricSeries(recsFor(-1, -1), PingAlertMetricLatency); ok {
		t.Error("全丢包时延迟序列应为空（没有延迟可言）")
	}
	// 未知指标
	if _, ok := pingMetricSeries(recsFor(1), "cpu"); ok {
		t.Error("非 ping 指标不应产出序列")
	}
}

func TestPingMetricValue(t *testing.T) {
	// 延迟取平均（只算未丢包样本）：(10+20+30)/3 = 20
	got, ok := pingMetricValue(recsFor(10, -1, 20, -1, 30), PingAlertMetricLatency)
	if !ok || got != 20 {
		t.Errorf("平均延迟 = %v (ok=%v), want 20", got, ok)
	}

	// 丢包率：5 个样本里 2 个丢 -> 40%
	got, ok = pingMetricValue(recsFor(10, -1, 20, -1, 30), PingAlertMetricLoss)
	if !ok || got != 40 {
		t.Errorf("丢包率 = %v (ok=%v), want 40", got, ok)
	}

	// 全丢包 -> 100%
	got, ok = pingMetricValue(recsFor(-1, -1, -1), PingAlertMetricLoss)
	if !ok || got != 100 {
		t.Errorf("全丢包应为 100%%, 实际 %v (ok=%v)", got, ok)
	}

	// 全正常 -> 0%
	got, ok = pingMetricValue(recsFor(5, 6, 7), PingAlertMetricLoss)
	if !ok || got != 0 {
		t.Errorf("全正常应为 0%%, 实际 %v", got)
	}
}

// 场景：目标只答 TCP 而被配成 icmp，于是 100% 丢包。
// 「丢包率 > 20%」的规则必须报出来，而正常窗口不能报。
func TestPingAlertScenarioSustainedLoss(t *testing.T) {
	outage := recsFor(-1, -1, -1, -1, -1)
	v, ok := pingMetricValue(outage, PingAlertMetricLoss)
	if !ok || v != 100 {
		t.Fatalf("持续丢包应算出 100%%，实际 %v", v)
	}
	if v < 20 {
		t.Error("100% 丢包应超过 20% 阈值")
	}

	healthy := recsFor(12, 15, 11, 13, 14)
	v, ok = pingMetricValue(healthy, PingAlertMetricLoss)
	if !ok || v != 0 {
		t.Fatalf("健康窗口丢包率应为 0%%，实际 %v", v)
	}
	if v >= 20 {
		t.Error("健康窗口不应触发 20% 阈值")
	}
}

// 场景：延迟劣化（基线 30~36ms，倍数 3 -> 阈值约 115ms）。
// 复用与主机指标相同的 computeBaselineThreshold，行为必须一致。
func TestPingAlertScenarioBaselineLatency(t *testing.T) {
	hist := make([]float32, 0, 48)
	for i := 0; i < 48; i++ {
		hist = append(hist, 30+float32(i%7))
	}
	threshold, ok := computeBaselineThreshold(hist, 3, 0)
	if !ok {
		t.Fatal("应算出基线阈值")
	}
	if threshold <= 100 || threshold > 115 {
		t.Fatalf("阈值应在 100~115ms，实际 %v", threshold)
	}

	spike := float32(0)
	v, _ := pingMetricValue(recsFor(320, 310, 330), PingAlertMetricLatency)
	spike = v
	if spike < threshold {
		t.Errorf("320ms 级别的尖峰应超过阈值 %v，实际 %v", threshold, spike)
	}

	normal, _ := pingMetricValue(recsFor(33, 35, 31), PingAlertMetricLatency)
	if normal >= threshold {
		t.Errorf("正常波动不应超过阈值 %v，实际 %v", threshold, normal)
	}
}
