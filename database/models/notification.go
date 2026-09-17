package models

import "time"

// Notification 定义了通知相关的数据库模型
type OfflineNotification struct {
	Client     string `json:"client" gorm:"type:varchar(36);not null;index;unique;constraint:OnDelete:CASCADE,OnUpdate:CASCADE;foreignKey:client;references:UUID"`
	ClientInfo Client `json:"client_info,omitempty" gorm:"foreignKey:Client;references:UUID"`
	Enable     bool   `json:"enable" gorm:"type:boolean;default:false"`
	//Cooldown     int       `json:"cooldown" gorm:"type:int;not null;default:1800"`                // 冷却时间（秒），默认 30 分钟
	GracePeriod  int        `json:"grace_period" gorm:"type:int;not null;default:180"` // 宽限期（秒），默认 3 分钟
	LastNotified *time.Time `json:"last_notified"`                                     // 上次通知时间
}

// 负载通知的阈值模式。
const (
	// LoadThresholdModeFixed 使用固定阈值（Threshold 字段），即历史行为。
	LoadThresholdModeFixed = "fixed"
	// LoadThresholdModeBaseline 用「该指标自身的历史基线」算出阈值，
	// 从而不必为每台机器/每个指标手工调一个绝对值。
	LoadThresholdModeBaseline = "baseline"
)

// LoadNotification 定义了基于资源占用达标时间比的负载通知规则
type LoadNotification struct {
	Id           uint        `json:"id,omitempty" gorm:"primaryKey;autoIncrement"`
	Name         string      `json:"name" gorm:"type:varchar(255)"`
	Clients      StringArray `json:"clients" gorm:"type:longtext"`
	Metric       string      `json:"metric" gorm:"type:varchar(50);not null;default:'cpu'"`     // 监控指标，如 cpu, ram, load
	Threshold    float32     `json:"threshold" gorm:"type:decimal(5,2);not null;default:80.00"` // 固定阈值；baseline 模式下作为【下限】使用
	Ratio        float32     `json:"ratio" gorm:"type:decimal(5,2);not null;default:0.80"`      // 达标时间比
	Interval     int         `json:"interval" gorm:"type:int;not null;default:15"`              // 监测间隔（分钟）
	LastNotified *time.Time  `json:"last_notified"`                                             // 上次通知时间

	// --- 基线模式（Mode = "baseline"）---
	//
	// 动机：固定阈值对不同的机器/指标要各自调参，且无法表达「这台机器今天明显
	// 比它平时差」。基线模式改为与自身历史比较：
	//     阈值 = max(P95(历史窗口) × Multiplier, Threshold)
	// 其中 Threshold 退化为【下限】，用来避免基线接近 0 时产生噪声告警
	// （例如丢包率平时为 0，可把下限设成 1 表示「有丢包就报」）。
	Mode         string  `json:"mode" gorm:"type:varchar(12);not null;default:'fixed'"`     // fixed | baseline
	BaselineDays int     `json:"baseline_days" gorm:"type:int;not null;default:7"`          // 基线回看天数
	Multiplier   float32 `json:"multiplier" gorm:"type:decimal(6,2);not null;default:3.00"` // 超过基线的倍数即视为异常

	// Tasks 仅在 Metric 为 ping 指标（ping_latency / ping_loss）时使用，
	// 存放延迟监测任务的 id。告警会应用到「运行该任务的服务器」上，
	// 因此不需要再单独选客户端；若同时选了客户端，则只在其中求值。
	Tasks StringArray `json:"tasks" gorm:"type:longtext"`
}

// UsesBaseline 报告该规则是否按历史基线取阈值。
func (n LoadNotification) UsesBaseline() bool {
	return n.Mode == LoadThresholdModeBaseline
}

// BaselineWindow 返回基线回看的时长（天 -> Duration），非法值回退到 7 天。
func (n LoadNotification) BaselineWindow() time.Duration {
	days := n.BaselineDays
	if days <= 0 {
		days = 7
	}
	if days > 365 {
		days = 365
	}
	return time.Duration(days) * 24 * time.Hour
}

// EffectiveMultiplier 返回有效的倍数，非法值回退到 3。
func (n LoadNotification) EffectiveMultiplier() float32 {
	if n.Multiplier <= 0 {
		return 3
	}
	return n.Multiplier
}

// TrafficReportNotification 定义了流量定时报告的数据库模型
type TrafficReportNotification struct {
	Client     string `json:"client" gorm:"type:varchar(36);not null;index;unique;constraint:OnDelete:CASCADE,OnUpdate:CASCADE;foreignKey:client;references:UUID"`
	ClientInfo Client `json:"client_info,omitempty" gorm:"foreignKey:Client;references:UUID"`
	Enable     bool   `json:"enable" gorm:"type:boolean;default:false"`
	Daily      bool   `json:"daily" gorm:"type:boolean;default:false"`   // 日报
	Weekly     bool   `json:"weekly" gorm:"type:boolean;default:false"`  // 周报
	Monthly    bool   `json:"monthly" gorm:"type:boolean;default:false"` // 月报
}
