package scheduler

import (
	"context"
	"errors"
	"strings"
	"sync"
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

func TestStopAndWaitIncludesRemovedAndReplacedCallbacks(t *testing.T) {
	m := NewManager()
	started := make(chan struct{}, 3)
	release := make(chan struct{})
	callback := func(context.Context) {
		started <- struct{}{}
		<-release
	}
	for _, name := range []string{"replaced", "replaced", "removed"} {
		if err := m.AddContextFunc(name, "@every 1h", true, callback); err != nil {
			t.Fatal(err)
		}
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("callback did not start")
		}
	}
	m.Remove("removed")
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	err := m.StopAndWait(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stop returned before old callbacks finished: %v", err)
	}
	if err := m.AddFunc("late", "@every 1h", func() {}); !errors.Is(err, ErrStopped) {
		t.Fatalf("registration during shutdown = %v", err)
	}
	close(release)
	if err := m.StopAndWait(t.Context()); err != nil {
		t.Fatalf("retry waiting for callbacks: %v", err)
	}
}

func TestStopAndWaitRacesRegistrationsAndLaunches(t *testing.T) {
	m := NewManager()
	var callers sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 24; i++ {
		callers.Add(1)
		go func() {
			defer callers.Done()
			<-start
			for j := 0; j < 20; j++ {
				err := m.AddContextFunc("shared", "@every 1h", true, func(context.Context) {})
				if err != nil && !errors.Is(err, ErrStopped) {
					t.Errorf("registration: %v", err)
					return
				}
			}
		}()
	}
	close(start)
	if err := m.StopAndWait(t.Context()); err != nil {
		t.Fatal(err)
	}
	callers.Wait()
	if err := m.StopAndWait(t.Context()); err != nil {
		t.Fatal(err)
	}
}
