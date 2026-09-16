package metricstore

import (
	"testing"
	"time"

	v2 "github.com/Aone2233/nekomari/protocol/v2"
)

// 备份指标只在 agent 上报了 backup 段时才产生 point。
// 否则未使用该功能的部署会被写入恒为 0 的序列，图表上看起来像
// 「备份一直是 0 秒前完成」—— 那是错误信息，比没有数据更糟。
func TestReportMetricPointsBackup(t *testing.T) {
	base := v2.Report{
		UUID:      "node-a",
		UpdatedAt: time.Now().UTC(),
	}

	// 未配置：不应出现任何 backup point
	without := reportMetricPoints(base, 0, 0)
	for _, p := range without {
		if p.MetricName == MetricBackupAge || p.MetricName == MetricBackupOK {
			t.Fatalf("未上报 backup 时不应写入 %s", p.MetricName)
		}
	}

	// 已配置：应出现 age 与 ok 两个 point，且值正确
	with := base
	with.Backup = &v2.BackupReport{AgeSeconds: 93600, Ok: 1} // 26 小时
	points := reportMetricPoints(with, 0, 0)

	var gotAge, gotOK *float64
	for i := range points {
		switch points[i].MetricName {
		case MetricBackupAge:
			v := points[i].Value
			gotAge = &v
		case MetricBackupOK:
			v := points[i].Value
			gotOK = &v
		}
	}
	if gotAge == nil || gotOK == nil {
		t.Fatal("配置了 backup 时应同时写入 age 与 ok 两个指标")
	}
	if *gotAge != 93600 {
		t.Errorf("backup age = %v, want 93600", *gotAge)
	}
	if *gotOK != 1 {
		t.Errorf("backup ok = %v, want 1", *gotOK)
	}
}

// 未知状态用 -1 表示，且 ok=0 —— 这样「不知道」不会被误读成「刚刚成功」。
func TestReportMetricPointsBackupUnknown(t *testing.T) {
	rep := v2.Report{
		UUID:      "node-b",
		UpdatedAt: time.Now().UTC(),
		Backup:    &v2.BackupReport{AgeSeconds: -1, Ok: 0, Message: "status file missing"},
	}
	points := reportMetricPoints(rep, 0, 0)
	for _, p := range points {
		if p.MetricName == MetricBackupAge && p.Value != -1 {
			t.Errorf("未知状态的 age 应为 -1，实际 %v", p.Value)
		}
		if p.MetricName == MetricBackupOK && p.Value != 0 {
			t.Errorf("未知状态的 ok 应为 0，实际 %v", p.Value)
		}
	}
}
