package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRetryMetricStoreConnectionStopsAfterRecovery(t *testing.T) {
	wantErr := errors.New("temporary connection failure")
	attempts := 0
	err := retryMetricStoreConnection(3, 0, func() error {
		attempts++
		if attempts < 3 {
			return wantErr
		}
		return nil
	})
	if err != nil {
		t.Fatalf("retry returned error after recovery: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

func TestGuideNoRouteRejectsUnknownAPIAndRedirectsPages(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(noStoreAPIResponses())
	router.NoRoute(guideNoRoute("/install", "Not found in install mode", nil))

	apiResponse := httptest.NewRecorder()
	router.ServeHTTP(apiResponse, httptest.NewRequest(http.MethodGet, "/api/unknown", nil))
	if apiResponse.Code != http.StatusNotFound {
		t.Fatalf("API status = %d, want %d", apiResponse.Code, http.StatusNotFound)
	}
	if got := apiResponse.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("API cache control = %q, want no-store", got)
	}

	pageResponse := httptest.NewRecorder()
	router.ServeHTTP(pageResponse, httptest.NewRequest(http.MethodGet, "/stale-guide", nil))
	if pageResponse.Code != http.StatusTemporaryRedirect {
		t.Fatalf("page status = %d, want %d", pageResponse.Code, http.StatusTemporaryRedirect)
	}
	if got := pageResponse.Header().Get("Location"); got != "/install" {
		t.Fatalf("redirect location = %q, want /install", got)
	}
}

func TestRetryMetricStoreConnectionReturnsLastError(t *testing.T) {
	wantErr := errors.New("connection unavailable")
	attempts := 0
	err := retryMetricStoreConnection(3, 0, func() error {
		attempts++
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

func TestShutdownReturnsCleanupError(t *testing.T) {
	wantErr := errors.New("queued reports were not flushed")
	app := New(Options{})
	app.addCleanup("metric-report-batcher", func(_ context.Context) error {
		return wantErr
	})

	err := app.Shutdown()
	if !errors.Is(err, wantErr) {
		t.Fatalf("shutdown error = %v, want %v", err, wantErr)
	}
}

func TestCriticalCleanupFailureStopsDependentTeardown(t *testing.T) {
	cause := errors.New("drain failed")
	var calls []string
	app := New(Options{})
	app.addCleanup("lock", func(context.Context) error { calls = append(calls, "lock"); return nil })
	app.addCriticalCleanup("store", func(context.Context) error { calls = append(calls, "store"); return nil })
	app.addCriticalCleanup("queue", func(context.Context) error { calls = append(calls, "queue"); return cause })
	app.addCleanup("ordinary", func(context.Context) error { calls = append(calls, "ordinary"); return errors.New("ordinary failure") })
	err := app.runCleanups(t.Context())
	if !errors.Is(err, cause) || !reflect.DeepEqual(calls, []string{"ordinary", "queue"}) {
		t.Fatalf("calls = %v, error = %v", calls, err)
	}
}

type testGuideWorker struct{ quiesce func(context.Context) error }

func (w testGuideWorker) Quiesce(ctx context.Context) error { return w.quiesce(ctx) }

func TestGuideWorkerFailureRetainsFenceForRetry(t *testing.T) {
	app := New(Options{})
	calls := 0
	app.guideWorker = testGuideWorker{quiesce: func(context.Context) error {
		calls++
		if calls == 1 {
			return context.DeadlineExceeded
		}
		return nil
	}}
	if err := app.quiesceGuide(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first guide drain = %v, want deadline", err)
	}
	if app.guideWorker == nil {
		t.Fatal("failed guide drain dropped its worker fence")
	}
	if err := app.quiesceGuide(context.Background()); err != nil {
		t.Fatalf("retry guide drain: %v", err)
	}
	if app.guideWorker != nil || calls != 2 {
		t.Fatalf("worker = %v, calls = %d", app.guideWorker, calls)
	}
}

func TestCleanHandoffRefusesEvenNoncriticalCleanupFailure(t *testing.T) {
	app := New(Options{})
	handoff := false
	app.addCleanHandoff("handoff", func(context.Context) error { handoff = true; return nil })
	want := errors.New("provider did not close")
	app.addCleanup("provider", func(context.Context) error { return want })
	if err := app.runCleanups(context.Background()); !errors.Is(err, want) {
		t.Fatalf("cleanup error = %v, want %v", err, want)
	}
	if handoff {
		t.Fatal("published handoff despite cleanup failure")
	}
}

func TestGuideDrainFailureCannotBeReclassifiedOnLaterShutdown(t *testing.T) {
	const child = "NEKOMARI_GUIDE_DRAIN_FAILURE_TEST"
	if os.Getenv(child) == "" {
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestGuideDrainFailureCannotBeReclassifiedOnLaterShutdown$")
		cmd.Env = append(os.Environ(), child+"=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("guide failure subprocess: %v\n%s", err, out)
		}
		return
	}
	cause := errors.New("previous guide drain failure")
	app := New(Options{})
	app.failHandoff(cause)
	var resourceClosed, handedOff bool
	app.addCleanHandoff("handoff", func(context.Context) error { handedOff = true; return nil })
	app.addCleanup("resource", func(context.Context) error { resourceClosed = true; return nil })
	if err := app.Shutdown(); !errors.Is(err, cause) || !resourceClosed || handedOff {
		t.Fatalf("shutdown = %v, resourceClosed = %v, handedOff = %v", err, resourceClosed, handedOff)
	}
	if err := app.Shutdown(); !errors.Is(err, cause) || handedOff {
		t.Fatalf("retry = %v, handedOff = %v", err, handedOff)
	}
}
