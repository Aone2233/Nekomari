package metricstore

import (
	"strings"
	"testing"

	"github.com/Aone2233/nekomari/internal/config"
)

// ResolveStoreMigrationSourceFingerprint 的优先级是「显式参数 → 上次记录的目标指纹 →
// 默认 SQLite」。最后一档最容易被忽略，而它决定了「管理员不填 source 时从哪里搬」；
// 中间那一档与 MigrationTargetKey 的写入时机（迁移成功后，以及每次恢复/打开时）
// 绑在一起，所以这里把三档都钉住。
func TestResolveStoreMigrationSourceFingerprintOrder(t *testing.T) {
	prepareRecoveryTest(t)

	// 1. 显式传入的 driver+dsn 优先。
	explicitCfg := MetricStoreConfig{Driver: "postgres", DSN: "postgres://example/old"}
	if got, want := ResolveStoreMigrationSourceFingerprint("postgres", "postgres://example/old"), targetFingerprint(&explicitCfg); got != want {
		t.Fatalf("explicit source = %q, want %q", got, want)
	}

	// 2. 没有显式参数时，用记录下来的那个目标指纹。
	const recorded = "mysql|user@tcp(old-host:3306)/metrics"
	if err := config.Set(MigrationTargetKey, recorded); err != nil {
		t.Fatalf("record %s: %v", MigrationTargetKey, err)
	}
	if got := ResolveStoreMigrationSourceFingerprint("", ""); got != recorded {
		t.Fatalf("inferred source = %q, want the recorded target %q", got, recorded)
	}

	// 3. 两者都没有时回到默认 SQLite 指纹。
	if err := config.Set(MigrationTargetKey, ""); err != nil {
		t.Fatalf("clear %s: %v", MigrationTargetKey, err)
	}
	if got, want := ResolveStoreMigrationSourceFingerprint("", ""), defaultSQLiteFingerprint(); got != want {
		t.Fatalf("default source = %q, want %q", got, want)
	}
}

// 迁移完成后，不带 source 的第二次迁移必须被【明确拒绝】，而不是静默地「搬自己」。
// 拒绝信息里要能看出该怎么做（显式传 source）。
func TestStartStoreMigrationRefusesWhenTheInferredSourceIsTheTarget(t *testing.T) {
	prepareRecoveryTest(t)

	cfg, err := config.GetManyAs[MetricStoreConfig]()
	if err != nil {
		t.Fatalf("load metric store config: %v", err)
	}
	if err := config.Set(MigrationTargetKey, targetFingerprint(cfg)); err != nil {
		t.Fatalf("record current target: %v", err)
	}

	err = StartStoreMigration("", "")
	if err == nil {
		t.Fatal("a migration whose source equals its target must be refused")
	}
	if !strings.Contains(err.Error(), "pass the source driver and DSN explicitly") {
		t.Fatalf("refusal must say what to do, got: %v", err)
	}
}
