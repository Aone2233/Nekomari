package admin

import (
	"testing"

	"github.com/Aone2233/nekomari/cmd/flags"
	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/internal/config"
)

// TestSSRFProtectionDefaultsToEnabled 钉住「这条设置没有值」的含义是「开着」。
//
// 以前 ssrfProtectionEnabled() 在记录不存在时【写入 false】并返回 false，于是默认
// 部署下市场下载没有任何地址过滤 —— 而默认市场源是第三方目录，下载地址就写在目录里，
// 抓取目标由远端内容决定。同一个文件里的 IsSSRFProtectionEnabled 却把读不到当成
// 已启用，状态显示与实际行为相反。现在两边都是 fail-closed。
func TestSSRFProtectionDefaultsToEnabled(t *testing.T) {
	// 本包没有 TestMain：先到者初始化内存库，后到者沿用（改 flags 对已初始化的库无效）。
	flags.DatabaseType = flags.DatabaseTypeSQLite
	flags.DatabaseFile = "file:ssrf-default?mode=memory&cache=shared"

	db := dbcore.GetDBInstance()
	if err := db.Exec("DELETE FROM configs WHERE key = ?", config.SSRFProtectionEnabledKey).Error; err != nil {
		t.Fatalf("clear %s: %v", config.SSRFProtectionEnabledKey, err)
	}

	enabled, err := ssrfProtectionEnabled()
	if err != nil {
		t.Fatalf("ssrfProtectionEnabled() error = %v", err)
	}
	if !enabled {
		t.Fatal("an unset ssrf_protection_enabled must mean enabled, not disabled")
	}
	if !IsSSRFProtectionEnabled() {
		t.Fatal("IsSSRFProtectionEnabled must agree with ssrfProtectionEnabled")
	}

	// 显式关闭仍然要生效：默认值不能把运维的决定盖掉。
	if err := config.Set(config.SSRFProtectionEnabledKey, false); err != nil {
		t.Fatalf("set the setting to false: %v", err)
	}
	enabled, err = ssrfProtectionEnabled()
	if err != nil {
		t.Fatalf("ssrfProtectionEnabled() error = %v", err)
	}
	if enabled {
		t.Fatal("an explicit false must still disable the protection")
	}
}
