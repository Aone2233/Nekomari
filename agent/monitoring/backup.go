package monitoring

// backup.go —— 备份新鲜度采集
//
// 动机：备份「有没有在跑」和「最后一次成功是什么时候」通常没人看，直到真要
// 恢复时才发现已经坏了几个月。这里把「距最后一次成功备份多久」变成一个普通
// 指标上报，于是它就能复用主机指标那套阈值 / 基线告警。
//
// 采集方式刻意做成「读一个状态文件」而不是「直接调用备份工具」：
//   - 与具体备份工具解耦（restic / borg / rsync / 自写脚本都能用）
//   - agent 不需要知道仓库密码等敏感信息
//   - 备份脚本只需在成功/失败时原子地写一个 JSON 文件
//
// 该功能默认关闭：未配置 --backup-status-file 时不上报任何 backup 指标，
// 避免给未使用它的部署写入恒为 0 的序列。

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// backupStatusFile 是状态文件的结构。字段名刻意放宽，以兼容不同备份脚本：
// 时间可取 epoch / last_backup_epoch / timestamp 之一，或 RFC3339 的 time 字符串。
type backupStatusFile struct {
	Status          string `json:"status"`
	Epoch           int64  `json:"epoch"`
	LastBackupEpoch int64  `json:"last_backup_epoch"`
	Timestamp       int64  `json:"timestamp"`
	Time            string `json:"time"`
	Message         string `json:"message"`
	Error           string `json:"error"`
}

type backupReport struct {
	AgeSeconds int64  `json:"age_seconds"`
	Ok         int    `json:"ok"`
	Message    string `json:"message,omitempty"`
}

// EvaluateBackupStatus 解析状态文件内容，返回距最后一次成功备份的秒数与成功标志。
//
// 这是一个纯函数（不碰文件系统、不读时钟），因此可以完整地单测。
//
// 约定：
//   - 解析失败或取不到时间 -> age = -1、ok = 0，message 说明原因。
//     age 用 -1 而不是 0：0 会被误读成「刚刚备份成功」，而实际是「不知道」。
//   - status 明确表示失败 -> ok = 0，但 age 仍按最后成功时间给出（如果文件里有），
//     这样「上次成功是 3 天前且最近一次失败」能被区分出来。
//   - 没有 status 字段时按时间推断成功（很多脚本只写时间戳）。
func EvaluateBackupStatus(content []byte, now time.Time) (int64, int, string) {
	var f backupStatusFile
	if err := json.Unmarshal(content, &f); err != nil {
		return -1, 0, fmt.Sprintf("cannot parse backup status file: %v", err)
	}

	epoch := firstNonZero(f.Epoch, f.LastBackupEpoch, f.Timestamp)
	if epoch == 0 && strings.TrimSpace(f.Time) != "" {
		if t, err := time.Parse(time.RFC3339, strings.TrimSpace(f.Time)); err == nil {
			epoch = t.Unix()
		} else {
			// 也接受 "2006-01-02 15:04:05" 这种常见写法（按本地时区解释）
			if t, err2 := time.ParseInLocation("2006-01-02 15:04:05", strings.TrimSpace(f.Time), time.Local); err2 == nil {
				epoch = t.Unix()
			}
		}
	}
	if epoch <= 0 {
		return -1, 0, "backup status file has no usable timestamp"
	}

	age := now.Unix() - epoch
	if age < 0 {
		// 时钟回拨或脚本写入了未来时间：不要报负数年龄
		age = 0
	}

	status := strings.ToLower(strings.TrimSpace(f.Status))
	failed := false
	switch status {
	case "", "ok", "success", "succeeded", "completed", "complete", "done", "finished":
		// 视为成功（空 status 时按时间推断）
	default:
		failed = true
	}

	msg := strings.TrimSpace(f.Message)
	if msg == "" {
		msg = strings.TrimSpace(f.Error)
	}
	if failed {
		if msg == "" {
			msg = "last backup reported status " + status
		}
		return age, 0, msg
	}
	return age, 1, msg
}

// CollectBackupStatus 读取状态文件并解析。文件不存在/不可读时返回
// age=-1、ok=0 并带说明 —— 这本身就是一种值得告警的状态。
func CollectBackupStatus(path string, now time.Time) (int64, int, string) {
	if strings.TrimSpace(path) == "" {
		return 0, 0, ""
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return -1, 0, fmt.Sprintf("cannot read backup status file %s: %v", path, err)
	}
	return EvaluateBackupStatus(content, now)
}

func firstNonZero(values ...int64) int64 {
	for _, v := range values {
		if v != 0 {
			return v
		}
	}
	return 0
}
