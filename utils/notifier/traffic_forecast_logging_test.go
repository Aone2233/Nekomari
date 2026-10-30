package notifier

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	cache "github.com/patrickmn/go-cache"
)

func TestForecastFailureLoggingByNodeAndKind(t *testing.T) {
	var warnings, failures []string
	logger := forecastFailureLogger{
		refusals: cache.New(time.Hour, 0),
		warn:     func(uuid string, err error) { warnings = append(warnings, uuid+":"+err.Error()) },
		fail:     func(uuid string, err error) { failures = append(failures, uuid+":"+err.Error()) },
	}
	first := invalidForecastInput(forecastCounterDecrease, "decreased at 01:00")
	changedBucket := invalidForecastInput(forecastCounterDecrease, "decreased at 02:00")
	missing := invalidForecastInput(forecastMissingDirection, "missing at 02:00")

	logger.failure("a", first)
	logger.failure("a", fmt.Errorf("wrapped: %w", changedBucket))
	logger.failure("b", first)
	logger.failure("a", missing)
	if len(warnings) != 3 {
		t.Fatalf("warnings = %v; want one per node and category", warnings)
	}
	if !strings.Contains(warnings[0], "01:00") || !strings.Contains(warnings[2], "missing") {
		t.Fatalf("warnings must retain the specific failure: %v", warnings)
	}

	// A different category is visible immediately, and its cache entry can
	// expire without a successful read.
	items := logger.refusals.Items()
	items[forecastRefusalKey("a", forecastCounterDecrease)] = cache.Item{
		Object: true, Expiration: time.Now().Add(-time.Second).UnixNano(),
	}
	logger.refusals = cache.NewFrom(time.Hour, 0, items)
	logger.failure("a", changedBucket)
	if len(warnings) != 4 || !strings.Contains(warnings[3], "02:00") {
		t.Fatalf("expired refusal must be logged again: %v", warnings)
	}

	logger.clear("a")
	logger.failure("a", first)
	logger.failure("a", missing)
	logger.failure("b", first)
	if len(warnings) != 6 {
		t.Fatalf("success must clear all categories only for that node: %v", warnings)
	}

	storeErr := errors.New("metric database unavailable")
	logger.failure("a", storeErr)
	logger.failure("a", fmt.Errorf("series query: %w", storeErr))
	if len(failures) != 2 || !strings.Contains(failures[1], storeErr.Error()) || len(warnings) != 6 {
		t.Fatalf("store failures must always remain errors: warnings=%v failures=%v", warnings, failures)
	}
}

func TestForecastRefusalLoggingIsConcurrent(t *testing.T) {
	var mu sync.Mutex
	warnings := 0
	logger := forecastFailureLogger{
		refusals: cache.New(time.Hour, 0),
		warn: func(string, error) {
			mu.Lock()
			defer mu.Unlock()
			warnings++
		},
		fail: func(string, error) { t.Error("unexpected non-input failure") },
	}
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			logger.failure("node", invalidForecastInput(forecastCounterDecrease, "decreased"))
		}()
	}
	wg.Wait()
	if warnings != 1 {
		t.Fatalf("concurrent duplicate warnings = %d, want 1", warnings)
	}
}
