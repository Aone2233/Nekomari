package notifier

import (
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/models"
)

func TestPercentile(t *testing.T) {
	vals := []float32{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	cases := []struct {
		p    float64
		want float32
	}{
		{0.0, 1},
		{0.5, 5},
		{0.95, 10}, // 最近秩法：ceil(0.95*10)=10 -> 第 10 个
		{1.0, 10},
	}
	for _, tc := range cases {
		if got := percentile(vals, tc.p); got != tc.want {
			t.Errorf("percentile(p=%.2f) = %v, want %v", tc.p, got, tc.want)
		}
	}
	if got := percentile(nil, 0.95); got != 0 {
		t.Errorf("空切片应返回 0，实际 %v", got)
	}
	// 不应修改入参顺序
	orig := append([]float32(nil), vals...)
	_ = percentile(vals, 0.5)
	for i := range vals {
		if vals[i] != orig[i] {
			t.Fatalf("percentile 修改了入参顺序: %v", vals)
		}
	}
}

// 基线阈值的所有分支。
func TestComputeBaselineThreshold(t *testing.T) {
	// 24 个样本，P95 落在最大值附近
	steady := func(max float32) []float32 {
		v := make([]float32, 24)
		for i := range v {
			v[i] = max * float32(i+1) / 24
		}
		return v
	}

	t.Run("样本不足时不给阈值", func(t *testing.T) {
		if _, ok := computeBaselineThreshold([]float32{1, 2, 3}, 3, 0); ok {
			t.Error("样本过少应返回 ok=false，避免用无统计意义的 P95")
		}
		if _, ok := computeBaselineThreshold(nil, 3, 0); ok {
			t.Error("空样本应返回 ok=false")
		}
	})

	t.Run("常规：P95 乘倍数", func(t *testing.T) {
		// steady(40) 的 P95 = 40*23/24 ≈ 38.33 -> ×3 ≈ 115
		got, ok := computeBaselineThreshold(steady(40), 3, 0)
		if !ok {
			t.Fatal("应给出阈值")
		}
		if got < 114 || got > 116 {
			t.Errorf("阈值 = %v，期望约 115", got)
		}
	})

	t.Run("下限生效：基线接近 0 时不产生噪声告警", func(t *testing.T) {
		// 丢包率平时为 0，下限设 1 表示「有丢包就报」
		zeros := make([]float32, 24)
		got, ok := computeBaselineThreshold(zeros, 3, 1)
		if !ok {
			t.Fatal("有下限时应给出阈值")
		}
		if got != 1 {
			t.Errorf("阈值应等于下限 1，实际 %v", got)
		}
	})

	t.Run("基线与下限都为 0 时无法判定", func(t *testing.T) {
		zeros := make([]float32, 24)
		if _, ok := computeBaselineThreshold(zeros, 3, 0); ok {
			t.Error("全 0 且无下限时应返回 ok=false")
		}
	})

	t.Run("倍数为 0 时回退到 3", func(t *testing.T) {
		a, ok1 := computeBaselineThreshold(steady(40), 0, 0)
		b, ok2 := computeBaselineThreshold(steady(40), 3, 0)
		if !ok1 || !ok2 || a != b {
			t.Errorf("倍数为 0 应等价于 3：%v/%v vs %v/%v", a, ok1, b, ok2)
		}
	})
}

func TestCheckMetricThresholdAtRatio(t *testing.T) {
	rec := func(cpu float32) models.Record { return models.Record{Cpu: cpu} }
	// 10 个样本：4 个超阈值
	records := []models.Record{
		rec(90), rec(95), rec(99), rec(100),
		rec(10), rec(20), rec(30), rec(40), rec(50), rec(60),
	}
	task := models.LoadNotification{Metric: "cpu", Ratio: 0.4}

	if !checkMetricThresholdAt(records, task, 90) {
		t.Error("4/10 达标、ratio=0.4 应判定为超阈值")
	}
	if checkMetricThresholdAt(records, task, 99) {
		t.Error("只有 2/10 达标、ratio=0.4 不应判定为超阈值")
	}
	if checkMetricThresholdAt(nil, task, 90) {
		t.Error("无样本不应告警")
	}
	// ratio 为 0 时至少要求 1 条
	zero := models.LoadNotification{Metric: "cpu", Ratio: 0}
	if z := checkMetricThresholdAt(records, zero, 10); !z {
		// ratio=0 时 minRequiredRecords 兜底为 1，应当命中
		t.Error("ratio=0 应兜底为至少 1 条")
	}
}

func TestLoadNotificationBaselineHelpers(t *testing.T) {
	n := models.LoadNotification{}
	if n.UsesBaseline() {
		t.Error("默认应为固定模式")
	}
	if got := n.BaselineWindow(); got != 7*24*time.Hour {
		t.Errorf("默认基线窗口应为 7 天，实际 %v", got)
	}
	if got := n.EffectiveMultiplier(); got != 3 {
		t.Errorf("默认倍数应为 3，实际 %v", got)
	}

	n = models.LoadNotification{Mode: models.LoadThresholdModeBaseline, BaselineDays: -5, Multiplier: 0}
	if !n.UsesBaseline() {
		t.Error("baseline 模式应被识别")
	}
	if got := n.BaselineWindow(); got != 7*24*time.Hour {
		t.Errorf("非法天数应回退 7 天，实际 %v", got)
	}
	if got := n.EffectiveMultiplier(); got != 3 {
		t.Errorf("非法倍数应回退 3，实际 %v", got)
	}

	n = models.LoadNotification{BaselineDays: 100000}
	if got := n.BaselineWindow(); got != 365*24*time.Hour {
		t.Errorf("超大天数应被限制在 365 天，实际 %v", got)
	}
}

// 真实场景：某台机器负载平时 30~36，倍数 3 -> 阈值约 105。
// 一次 320 的尖峰应当报出来，而 40 的正常波动不应报。
//
// 注意：这里的指标走的是既有 LoadNotification 通道（cpu/ram/load/net/... 等
// 主机指标）。ping 延迟/丢包位于 metric store 的另一条序列上，需要独立的、
// 带 task_id 的求值器 —— 见本仓库 issue/后续计划，不在本次范围内。
func TestBaselineScenarioLoadSpike(t *testing.T) {
	baseline := []float32{}
	for i := 0; i < 48; i++ { // 48 个历史样本，围绕 30~36 波动
		baseline = append(baseline, 30+float32(i%7))
	}
	threshold, ok := computeBaselineThreshold(baseline, 3, 0)
	if !ok {
		t.Fatal("应算出阈值")
	}
	if threshold <= 100 || threshold > 115 {
		t.Fatalf("阈值应在 100~115 之间，实际 %v", threshold)
	}

	spike := []models.Record{{Load: 320}}
	task := models.LoadNotification{Metric: "load", Ratio: 1}
	if !checkMetricThresholdAt(spike, task, threshold) {
		t.Errorf("320 的尖峰应超过阈值 %v", threshold)
	}

	normal := []models.Record{{Load: 40}}
	if checkMetricThresholdAt(normal, task, threshold) {
		t.Errorf("40 的正常波动不应超过阈值 %v", threshold)
	}
}