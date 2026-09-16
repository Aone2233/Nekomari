package monitoring

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 备份新鲜度解析是纯函数，把所有分支钉住。
// age 用 -1（而不是 0）表示「不知道」：0 会被误读成「刚刚备份成功」。
func TestEvaluateBackupStatus(t *testing.T) {
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	nowUnix := now.Unix()

	cases := []struct {
		name    string
		content string
		wantAge int64
		wantOK  int
		wantMsg bool // 是否应当给出说明
	}{
		{
			// 与本仓库 MAC-WAN 上 /var/lib/macwan-backup/last-backup.json 同形
			name:    "标准格式：成功且 2 小时前",
			content: `{"status":"ok","epoch":` + itoa(nowUnix-7200) + `,"snapshot":"abc123","size":123456,"host":"macbook-linux"}`,
			wantAge: 7200, wantOK: 1,
		},
		{
			name:    "status 表示失败：ok=0 但仍给出上次成功年龄",
			content: `{"status":"failed","epoch":` + itoa(nowUnix-3*86400) + `,"error":"rclone: token expired"}`,
			wantAge: 3 * 86400, wantOK: 0, wantMsg: true,
		},
		{
			name:    "status 大小写与常见同义词",
			content: `{"status":"SUCCESS","epoch":` + itoa(nowUnix-60) + `}`,
			wantAge: 60, wantOK: 1,
		},
		{
			name:    "无 status 字段时按时间推断成功",
			content: `{"timestamp":` + itoa(nowUnix-3600) + `}`,
			wantAge: 3600, wantOK: 1,
		},
		{
			name:    "兼容 last_backup_epoch 字段名",
			content: `{"last_backup_epoch":` + itoa(nowUnix-900) + `}`,
			wantAge: 900, wantOK: 1,
		},
		{
			name:    "RFC3339 时间字符串",
			content: `{"status":"ok","time":"` + now.Add(-2*time.Hour).Format(time.RFC3339) + `"}`,
			wantAge: 7200, wantOK: 1,
		},
		{
			name:    "空格分隔的本地时间字符串",
			content: `{"time":"2026-09-16 10:00:00"}`,
			// 按本地时区解释，因此只断言「有值且非负」，不写死具体秒数
			wantAge: 0, wantOK: 1,
		},
		{
			name:    "没有可用时间戳 -> 未知",
			content: `{"status":"ok"}`,
			wantAge: -1, wantOK: 0, wantMsg: true,
		},
		{
			name:    "JSON 损坏 -> 未知",
			content: `{not json`,
			wantAge: -1, wantOK: 0, wantMsg: true,
		},
		{
			name:    "空文件 -> 未知",
			content: ``,
			wantAge: -1, wantOK: 0, wantMsg: true,
		},
		{
			// 时钟回拨或脚本写了未来时间：不能报负数年龄
			name:    "未来时间 -> 钳制为 0",
			content: `{"status":"ok","epoch":` + itoa(nowUnix+9999) + `}`,
			wantAge: 0, wantOK: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			age, ok, msg := EvaluateBackupStatus([]byte(tc.content), now)
			if ok != tc.wantOK {
				t.Errorf("ok = %d, want %d", ok, tc.wantOK)
			}
			if tc.name == "空格分隔的本地时间字符串" {
				if age < 0 {
					t.Errorf("本地时间字符串应能解析出非负年龄，实际 %d", age)
				}
			} else if age != tc.wantAge {
				t.Errorf("age = %d, want %d", age, tc.wantAge)
			}
			if tc.wantMsg && msg == "" {
				t.Error("应当给出说明信息，便于排查")
			}
		})
	}
}

// 读取路径：文件不存在本身就是一种应当被看见的状态。
func TestCollectBackupStatusFile(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()

	good := filepath.Join(dir, "ok.json")
	if err := os.WriteFile(good, []byte(`{"status":"ok","epoch":`+itoa(now.Unix()-120)+`}`), 0o644); err != nil {
		t.Fatal(err)
	}
	age, ok, msg := CollectBackupStatus(good, now)
	if ok != 1 || age < 100 || age > 200 {
		t.Fatalf("正常文件应解析出 ok=1 且约 120 秒，实际 ok=%d age=%d msg=%s", ok, age, msg)
	}

	missing := filepath.Join(dir, "nope.json")
	age, ok, msg = CollectBackupStatus(missing, now)
	if ok != 0 || age != -1 {
		t.Errorf("文件缺失应返回 ok=0/age=-1，实际 ok=%d age=%d", ok, age)
	}
	if msg == "" {
		t.Error("文件缺失应给出说明")
	}

	// 未配置路径时应完全静默（返回零值，调用方不会上报）
	if age, ok, msg := CollectBackupStatus("", now); age != 0 || ok != 0 || msg != "" {
		t.Errorf("空路径应返回零值，实际 %d/%d/%q", age, ok, msg)
	}
}

// 回到真实场景：一份 26 小时前的成功备份 + 一个失败的最新状态。
// 前者用于「太久没备份」的告警，后者用于「最近一次失败」的告警。
func TestBackupScenarioStaleness(t *testing.T) {
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)

	stale, ok, _ := EvaluateBackupStatus([]byte(`{"status":"ok","epoch":`+itoa(now.Add(-26*time.Hour).Unix())+`}`), now)
	if ok != 1 {
		t.Fatal("26 小时前的成功备份仍应标记为 ok=1")
	}
	if stale != 26*3600 {
		t.Errorf("年龄应为 26 小时，实际 %d", stale)
	}

	fresh, ok, _ := EvaluateBackupStatus([]byte(`{"status":"ok","epoch":`+itoa(now.Add(-2*time.Hour).Unix())+`}`), now)
	if ok != 1 || fresh >= stale {
		t.Errorf("2 小时前的备份年龄应明显小于 26 小时：%d vs %d", fresh, stale)
	}
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [24]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
