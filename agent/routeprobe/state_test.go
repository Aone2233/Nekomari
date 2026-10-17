package routeprobe

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDailyScheduleSurvivesRestarts(t *testing.T) {
	file := filepath.Join(t.TempDir(), "state.json")
	now := time.Now().UTC()
	targets := []Target{{Carrier: "telecom", IP: "1.1.1.1"}}
	calls := 0
	probe := func(context.Context, []Target) []Result {
		calls++
		return []Result{{Carrier: "telecom", Target: "1.1.1.1", Error: "timeout"}}
	}
	for _, hours := range []int{0, 1, 6, 12, 18, 23, 24, 25} {
		s, err := CachedProbe(context.Background(), file, targets, now.Add(time.Duration(hours)*time.Hour), probe)
		if err != nil || len(s.Results) != 1 {
			t.Fatal(s, err)
		}
		if hours < 24 && !s.ProbedAt.Equal(now) {
			t.Fatal("cached timestamp was refreshed")
		}
	}
	if calls != 2 {
		t.Fatalf("probed %d times, want twice over 25h", calls)
	}
	info, _ := os.Stat(file)
	if info.Mode().Perm() != 0600 {
		t.Fatal("state must be private")
	}
}

func TestUnwritableOrCorruptStateDoesNotProbe(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "state.json")
	os.WriteFile(file, []byte("broken"), 0600)
	for _, file := range []string{file, filepath.Join(dir, "absent", "state.json")} {
		_, err := CachedProbe(context.Background(), file, nil, time.Now(), func(context.Context, []Target) []Result { t.Fatal("unscheduled probe"); return nil })
		if err == nil {
			t.Fatal("expected state error")
		}
	}
}

func TestInvalidAndOversizedStateDoesNotProbe(t *testing.T) {
	for _, data := range [][]byte{[]byte(`{}`), make([]byte, 64*1024+1)} {
		file := filepath.Join(t.TempDir(), "state.json")
		if err := os.WriteFile(file, data, 0600); err != nil {
			t.Fatal(err)
		}
		_, err := CachedProbe(context.Background(), file, []Target{{Carrier: "telecom", IP: "1.1.1.1"}}, time.Now(), func(context.Context, []Target) []Result {
			t.Fatal("invalid state triggered network probe")
			return nil
		})
		if err == nil {
			t.Fatal("invalid state accepted")
		}
	}
}

func TestInterruptedProbeKeepsDailyReservation(t *testing.T) {
	file := filepath.Join(t.TempDir(), "state.json")
	now := time.Now().UTC()
	targets := []Target{{Carrier: "telecom", IP: "1.1.1.1"}}
	func() {
		defer func() { _ = recover() }()
		_, _ = CachedProbe(context.Background(), file, targets, now, func(context.Context, []Target) []Result { panic("simulated interruption") })
	}()
	state, err := CachedProbe(context.Background(), file, targets, now.Add(time.Hour), func(context.Context, []Target) []Result {
		t.Fatal("restart repeated interrupted daily probe")
		return nil
	})
	if err != nil || len(state.Results) != 1 || state.Results[0].Error == "" || !state.ProbedAt.Equal(now) {
		t.Fatal(state, err)
	}
}
