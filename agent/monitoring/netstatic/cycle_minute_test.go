package netstatic

import (
	"testing"
	"time"
)

func TestCycleResetMinuteBoundary(t *testing.T) {
	zone := time.FixedZone("UTC+8", 8*60*60)
	for _, tc := range []struct {
		at, want time.Time
	}{
		{time.Date(2026, 11, 9, 0, 4, 59, 0, zone), time.Date(2026, 10, 9, 0, 5, 0, 0, zone)},
		{time.Date(2026, 11, 9, 0, 5, 0, 0, zone), time.Date(2026, 11, 9, 0, 5, 0, 0, zone)},
		{time.Date(2026, 12, 8, 23, 59, 59, 0, zone), time.Date(2026, 11, 9, 0, 5, 0, 0, zone)},
		{time.Date(2026, 12, 9, 0, 5, 0, 0, zone), time.Date(2026, 12, 9, 0, 5, 0, 0, zone)},
	} {
		got := cycleResetTime(9, 5, tc.at)
		if got != uint64(tc.want.Unix()) {
			t.Errorf("at %s got %s, want %s", tc.at, time.Unix(int64(got), 0).In(zone), tc.want)
		}
	}
}

func TestAccumulatorCrossesFiveMinuteBoundary(t *testing.T) {
	resetState(t)
	mu.Lock()
	config = configOrDefault(NetStaticConfig{MonthRotate: 9, MonthRotateMinute: 5})
	store.Config = config
	store.Cycles = map[string]CycleTraffic{}
	mu.Unlock()
	boundary := time.Date(2026, 11, 9, 0, 5, 0, 0, time.Local)
	accumulateCycleLocked("eth0", 100, 200, uint64(boundary.Add(-time.Second).Unix()))
	accumulateCycleLocked("eth0", 3, 4, uint64(boundary.Unix()))
	entry := store.Cycles["eth0"]
	if entry.Tx != 3 || entry.Rx != 4 || entry.ResetDay != uint64(boundary.Unix()) {
		t.Fatalf("incorrect cycle after boundary: %+v", entry)
	}
}

func TestResetMinuteCanReturnToMidnight(t *testing.T) {
	resetState(t)
	if err := SetNewConfig(NetStaticConfig{MonthRotate: 9, MonthRotateMinute: 5}); err != nil {
		t.Fatal(err)
	}
	if err := SetNewConfig(NetStaticConfig{MonthRotate: 9}); err != nil {
		t.Fatal(err)
	}
	if config.MonthRotateMinute != 0 {
		t.Fatalf("minute remains %d after resetting configuration", config.MonthRotateMinute)
	}
	if err := SetNewConfig(NetStaticConfig{MonthRotate: 9, MonthRotateMinute: 1440}); err == nil {
		t.Fatal("invalid minute accepted")
	}
}

func TestConfiguredCycleTimezoneIsIndependentOfSystemTimezone(t *testing.T) {
	resetState(t)
	if err := SetNewConfig(NetStaticConfig{MonthRotate: 9, MonthRotateMinute: 5, MonthRotateTimezone: "Asia/Shanghai"}); err != nil {
		t.Fatal(err)
	}
	before := time.Date(2026, 11, 8, 16, 4, 59, 0, time.UTC)
	after := before.Add(time.Second)
	if got, want := configuredCycleReset(before), uint64(time.Date(2026, 10, 8, 16, 5, 0, 0, time.UTC).Unix()); got != want {
		t.Fatalf("before boundary %d, want %d", got, want)
	}
	if got, want := configuredCycleReset(after), uint64(after.Unix()); got != want {
		t.Fatalf("at boundary %d, want %d", got, want)
	}
	if err := SetNewConfig(NetStaticConfig{MonthRotate: 9, MonthRotateTimezone: "Invalid/Timezone"}); err == nil {
		t.Fatal("invalid timezone accepted")
	}
	if err := SetNewConfig(NetStaticConfig{MonthRotate: 9}); err != nil || config.MonthRotateTimezone != "" {
		t.Fatalf("failed to restore system timezone: %s, %v", config.MonthRotateTimezone, err)
	}
}
