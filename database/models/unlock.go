package models

import "time"

// UnlockReport 是某个节点最近一次「流媒体 / AI 解锁」探测的结果。
//
// 按节点存而不是按 IP 存：面板问的是「这个节点能不能用」，而出口地址不一定是节点
// 自己的地址 —— 本机群里有主机是经由另一台节点出网的。所以出口地址单独存一份，
// 面板才能说清楚这份结论测的到底是哪个出口。
//
// Results 是 JSON 数组，元素形状见 protocol/v2.UnlockItem。放 text 而不是拆表，
// 是因为它是每次探测整体替换的一份快照，没有按行查询的需求。
type UnlockReport struct {
	UUID         string    `json:"uuid" gorm:"primaryKey;type:varchar(64)"`
	EgressIP     string    `json:"egress_ip" gorm:"type:varchar(100)"`
	EgressRegion string    `json:"egress_region" gorm:"type:varchar(8)"`
	ProbedAt     time.Time `json:"probed_at"`
	Results      string    `json:"results" gorm:"type:text"`
	UpdatedAt    time.Time `json:"updated_at"`
}
