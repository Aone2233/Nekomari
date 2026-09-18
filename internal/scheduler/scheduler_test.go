package scheduler

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestCronScheduleUsesSystemLocalWallClock(t *testing.T) {
	originalLocal := time.Local
	time.Local = time.FixedZone("UTC+8", 8*60*60)
	t.Cleanup(func() { time.Local = originalLocal })

	schedule, err := Parse("0 0 9 * * *")
	if err != nil {
		t.Fatalf("parse schedule: %v", err)
	}
	after := time.Date(2026, 7, 17, 0, 30, 0, 0, time.UTC)
	want := time.Date(2026, 7, 17, 1, 0, 0, 0, time.UTC)
	if got := schedule.Next(after); !got.Equal(want) {
		t.Fatalf("next run = %s, want %s", got, want)
	} else if got.Location() != time.UTC {
		t.Fatalf("next run location = %s, want UTC", got.Location())
	}
}

func TestEverySchedulePreservesElapsedDuration(t *testing.T) {
	schedule, err := Parse("@every 90s")
	if err != nil {
		t.Fatalf("parse schedule: %v", err)
	}
	after := time.Now()
	if got := schedule.Next(after); got.Sub(after) != 90*time.Second {
		t.Fatalf("interval = %s, want 90s", got.Sub(after))
	}
}

// A spec can parse and still never come due: cronSchedule.Next scans a year of
// seconds and gives up, and 30 February never exists.
func TestCronSpecCanParseYetNeverComeDue(t *testing.T) {
	schedule, err := Parse("0 0 0 30 2 *")
	if err != nil {
		t.Skipf("parser rejects 30 February outright, so the case cannot arise: %v", err)
	}
	if got := schedule.Next(time.Now()); !got.IsZero() {
		t.Fatalf("Next(30 February) = %s, want the zero time", got)
	}
}

// Such a job used to be accepted -- AddContextFunc returned nil -- and then run()
// logged a warning and returned, so it never executed and nothing surfaced anywhere
// an operator would look. Whatever it drove simply stopped.
func TestAddContextFuncRejectsSpecThatNeverComesDue(t *testing.T) {
	m := &Manager{jobs: map[string]job{}}

	err := m.AddContextFunc("never-fires", "0 0 0 30 2 *", false, func(context.Context) {})
	if err == nil {
		t.Fatal("AddContextFunc accepted a job that can never run")
	}
	if !strings.Contains(err.Error(), "never comes due") {
		t.Fatalf("error %q does not explain that the job never comes due", err)
	}
	if len(m.jobs) != 0 {
		t.Fatalf("a rejected job was still registered: %v", m.jobs)
	}
}

// The guard must not reject ordinary schedules.
func TestAddContextFuncAcceptsOrdinarySpec(t *testing.T) {
	m := &Manager{jobs: map[string]job{}}

	if err := m.AddContextFunc("ordinary", "*/5 * * * *", false, func(context.Context) {}); err != nil {
		t.Fatalf("AddContextFunc with a valid spec = %v, want nil", err)
	}
	m.StopAll()
}
