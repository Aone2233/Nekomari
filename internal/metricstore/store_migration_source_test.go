package metricstore

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/pkg/metric"
)

func TestMigrationPreservesDestinationRollupsAndDefinitions(t *testing.T) {
	ctx := t.Context()
	for _, test := range []struct {
		name           string
		sourceDisabled bool
		conflict       bool
	}{
		{name: "different live aggregate", conflict: true},
		{name: "disabled source definition", sourceDisabled: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			src := reservationStore(t, filepath.Join(t.TempDir(), "source.db"))
			dst := reservationStore(t, filepath.Join(t.TempDir(), "target.db"))
			row := reservationRollup(MetricCPU, "13")
			if err := dst.ImportRollups(ctx, []metric.PersistedRollup{row}); err != nil {
				t.Fatal(err)
			}
			if test.conflict {
				changed := row
				changed.Sum, changed.SumSq, changed.Min, changed.Max = 2, 4, 2, 2
				changed.FirstValue, changed.LastValue = 2, 2
				if err := src.ImportRollups(ctx, []metric.PersistedRollup{changed}); err != nil {
					t.Fatal(err)
				}
			}
			if test.sourceDisabled {
				if _, err := src.UpdateMetricRetention(ctx, MetricCPU, 0); err != nil {
					t.Fatal(err)
				}
			}
			export := func() []metric.PersistedRollup {
				var rows []metric.PersistedRollup
				if _, err := dst.ExportRollups(ctx, MetricCPU, 10, func(batch []metric.PersistedRollup) error {
					rows = append(rows, batch...)
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				return rows
			}
			before := export()
			_, err := migrateBetweenStores(ctx, src, dst, nil, nil)
			if err == nil || (test.conflict && !errors.Is(err, metric.ErrImportConflict)) {
				t.Fatalf("migration did not refuse conflict: %v", err)
			}
			if after := export(); !reflect.DeepEqual(before, after) {
				t.Fatalf("destination changed: before=%+v after=%+v", before, after)
			}
			def, err := dst.GetMetric(ctx, MetricCPU)
			if err != nil || def.RetentionDays != 30 {
				t.Fatalf("destination definition changed: %+v, %v", def, err)
			}
		})
	}
}

func TestMigrationReportsDestinationConflictWithoutReplacingIt(t *testing.T) {
	prepareRecoveryTest(t)
	ctx := t.Context()
	targetPath := filepath.Join(t.TempDir(), "target.db")
	sourcePath := filepath.Join(t.TempDir(), "source.db")
	if err := config.SetMany(map[string]any{MetricDBDriverKey: "sqlite", MetricDBDSNKey: targetPath}); err != nil {
		t.Fatal(err)
	}
	if err := InitializeStore(); err != nil {
		t.Fatal(err)
	}
	source, err := metric.Open(ctx, metric.SQLite(sourcePath, metric.WithRollupPolicy(defaultRollupPolicy())))
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureBuiltinMetricDefinitions(ctx, source); err != nil {
		t.Fatal(err)
	}
	targetRow := reservationRollup(MetricCPU, "13")
	sourceRow := targetRow
	sourceRow.Sum, sourceRow.SumSq, sourceRow.Min, sourceRow.Max = 2, 4, 2, 2
	sourceRow.FirstValue, sourceRow.LastValue = 2, 2
	if err := GetStore().ImportRollups(ctx, []metric.PersistedRollup{targetRow}); err != nil {
		t.Fatal(err)
	}
	if err := source.ImportRollups(ctx, []metric.PersistedRollup{sourceRow}); err != nil {
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	if err := StartStoreMigration("sqlite", sourcePath); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for IsStoreMigrationRunning() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	progress := GetStoreMigrationProgress()
	if IsStoreMigrationRunning() || progress.Status != "failed" || !strings.Contains(progress.Error, "existing destination rollup differs") || strings.Contains(progress.Error, targetPath) || strings.Contains(progress.Error, sourcePath) {
		t.Fatalf("unsafe migration status: %+v", progress)
	}
	var rows []metric.PersistedRollup
	if _, err := GetStore().ExportRollups(ctx, MetricCPU, 10, func(batch []metric.PersistedRollup) error {
		rows = append(rows, batch...)
		return nil
	}); err != nil || len(rows) != 1 || rows[0].Sum != 1 {
		t.Fatalf("destination changed: %+v, %v", rows, err)
	}
}

func TestStoreMigrationRefusesSQLiteFileAliasesBeforeCleanup(t *testing.T) {
	for _, aliasKind := range []string{"symlink", "hardlink"} {
		t.Run(aliasKind, func(t *testing.T) {
			prepareRecoveryTest(t)
			ctx := t.Context()
			dir := t.TempDir()
			targetPath, aliasPath := filepath.Join(dir, "target.db"), filepath.Join(dir, "alias.db")
			if err := config.SetMany(map[string]any{MetricDBDriverKey: "sqlite", MetricDBDSNKey: targetPath}); err != nil {
				t.Fatal(err)
			}
			if err := InitializeStore(); err != nil {
				t.Fatal(err)
			}
			row := reservationRollup(MetricPingLoss, "12")
			if err := GetStore().ImportRollups(ctx, []metric.PersistedRollup{row}); err != nil {
				t.Fatal(err)
			}
			if err := config.Database().Create(&models.PingTaskDeletion{TaskID: 12}).Error; err != nil {
				t.Fatal(err)
			}
			var err error
			if aliasKind == "symlink" {
				err = os.Symlink(targetPath, aliasPath)
			} else {
				err = os.Link(targetPath, aliasPath)
			}
			if err != nil {
				t.Skipf("%s unavailable: %v", aliasKind, err)
			}
			if err := StartStoreMigration("sqlite", aliasPath); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(10 * time.Second)
			for IsStoreMigrationRunning() && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			progress := GetStoreMigrationProgress()
			if IsStoreMigrationRunning() || progress.Status != "failed" || !strings.Contains(progress.Error, "distinct file-backed SQLite") {
				t.Fatalf("aliased source was not refused: %+v", progress)
			}
			if got := countReservationRollups(t, GetStore(), MetricPingLoss); got["12"] != 1 {
				t.Fatalf("migration mutated destination before alias check: %v", got)
			}
		})
	}
}

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

func TestMigrationErrorsAndStatusDoNotExposeConnectionStrings(t *testing.T) {
	const secret = "migration-test-secret-83756"
	for _, fingerprint := range []string{secret, "|" + secret} {
		_, err := configFromFingerprint(fingerprint, &MetricStoreConfig{})
		if err == nil || strings.Contains(err.Error(), secret) {
			t.Fatalf("unsafe fingerprint error: %v", err)
		}
	}

	prepareRecoveryTest(t)
	target := filepath.Join(t.TempDir(), "active.db")
	if err := config.SetMany(map[string]any{MetricDBDriverKey: "sqlite", MetricDBDSNKey: target}); err != nil {
		t.Fatal(err)
	}
	if err := InitializeStore(); err != nil {
		t.Fatal(err)
	}
	// Invalid port makes pgx fail locally while parsing, without external I/O.
	source := "postgres://user:" + secret + "@localhost:notaport/metrics?token=" + secret
	if err := StartStoreMigration("postgres", source); err != nil {
		t.Fatalf("start: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for IsStoreMigrationRunning() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if IsStoreMigrationRunning() {
		t.Fatal("migration did not stop")
	}
	progress := GetStoreMigrationProgress()
	data, err := json.Marshal(progress)
	if err != nil {
		t.Fatal(err)
	}
	if progress.Status != "failed" || progress.Error == "" || progress.SourceDSN != "[redacted]" || progress.TargetDSN != "[redacted]" || strings.Contains(string(data), secret) || strings.Contains(string(data), target) {
		t.Fatalf("unsafe migration status: %s", data)
	}
}

func TestMigrationStatusRedactsAllDSNFormats(t *testing.T) {
	for _, dsn := range []string{
		"postgres://user:pass@example/db?token=secret",
		"host=localhost PASSWORD='secret with spaces' dbname=metrics",
		"user:pass@tcp(localhost:3306)/metrics?password=secret",
		"file:metrics.db?key=secret",
		"unrecognized-secret-format",
	} {
		if got := maskDSN(dsn); got != "[redacted]" {
			t.Errorf("unexpected DSN status: %q", got)
		}
	}
	if got := maskDSN(" \t "); got != "" {
		t.Errorf("empty DSN status: %q", got)
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

func TestSameStoreMigrationErrorDoesNotExposeDSN(t *testing.T) {
	prepareRecoveryTest(t)
	const dsn = "postgres://user:migration-test-secret-4512@localhost/metrics?token=other-secret"
	if err := config.SetMany(map[string]any{MetricDBDriverKey: "postgresql", MetricDBDSNKey: dsn}); err != nil {
		t.Fatal(err)
	}
	if err := config.Set(MigrationTargetKey, "postgresql|"+dsn); err != nil {
		t.Fatal(err)
	}
	err := StartStoreMigration("", "")
	if err == nil || !strings.Contains(err.Error(), "same") || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), dsn) {
		t.Fatalf("unsafe same-store result: %v", err)
	}
}
