// Package unlock 保存与读取节点的「流媒体 / AI 解锁」探测结果。
//
// 结果由节点上的探针产生并上报，服务端只负责存最近一次的快照。之所以必须由探针
// 来测：解锁取决于发起请求的那个 IP，服务端在别的机房，只能测到它自己。
package unlock

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
	"gorm.io/gorm"
)

// Save 覆盖写入一个节点的最近一次探测结果。
func Save(uuid string, params v2.UnlockParams) error {
	encoded, err := json.Marshal(params.Results)
	if err != nil {
		return err
	}
	probedAt := params.ProbedAt
	if probedAt.IsZero() {
		probedAt = time.Now().UTC()
	}
	report := models.UnlockReport{
		UUID:         uuid,
		EgressIP:     params.EgressIP,
		EgressRegion: params.EgressRegion,
		ProbedAt:     probedAt,
		Results:      string(encoded),
		UpdatedAt:    time.Now().UTC(),
	}
	// 一次探测就是一份完整快照，直接整体替换，不需要合并。
	return dbcore.GetDBInstance().Save(&report).Error
}

// Load 读取一个节点的探测结果。没有记录时返回 (nil, nil) —— 「还没测过」不是错误，
// 面板据此显示成「等待探测」而不是报错。
func Load(uuid string) (*v2.UnlockParams, error) {
	var report models.UnlockReport
	err := dbcore.GetDBInstance().Where("uuid = ?", uuid).First(&report).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	params := v2.UnlockParams{
		EgressIP:     report.EgressIP,
		EgressRegion: report.EgressRegion,
		ProbedAt:     report.ProbedAt,
	}
	if report.Results != "" {
		if err := json.Unmarshal([]byte(report.Results), &params.Results); err != nil {
			// 存进去的 JSON 解不出来，说明记录已经损坏。返回空结果而不是让整个
			// ip-info 接口失败 —— 解锁面板是附加信息，不该拖垮地理与延迟数据。
			params.Results = nil
		}
	}
	return &params, nil
}
