package plugin

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// The hook buffer holds one response in memory so a response hook can rewrite
// it. It must stay small enough that a single hooked response cannot pin tens of
// megabytes (the previous 32 MiB cap, plus bodyBytes()'s copy, allowed ~64 MiB
// per request). 2 MiB covers the panel's own JSON and HTML pages; larger
// responses are forwarded unmodified by bufferedResponseWriter.Write.
func TestHookBufferLimitStaysSmall(t *testing.T) {
	if maxHookBufferBytes != 2<<20 {
		t.Fatalf("maxHookBufferBytes = %d, want 2 MiB (%d)", maxHookBufferBytes, 2<<20)
	}
}

// TestBufferedResponseWriterPassesThroughOversizedBody drives the writer past
// its limit and asserts every byte reached the client: passthrough abandons
// rewriting, it does not truncate the response.
func TestBufferedResponseWriterPassesThroughOversizedBody(t *testing.T) {
	first := bytes.Repeat([]byte("a"), maxHookBufferBytes/2)
	// The second write alone exceeds the remaining budget, so the writer must
	// flush what it holds and forward this write unchanged.
	second := bytes.Repeat([]byte("b"), maxHookBufferBytes)

	rec := httptest.NewRecorder()
	writer := newBufferedResponseWriter(rec)
	if _, err := writer.Write(first); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if writer.passedThrough() {
		t.Fatal("writer switched to passthrough before the limit was exceeded")
	}
	if _, err := writer.Write(second); err != nil {
		t.Fatalf("second write: %v", err)
	}
	if !writer.passedThrough() {
		t.Fatal("writer did not switch to passthrough after exceeding the limit")
	}
	want := append(append([]byte(nil), first...), second...)
	if got := rec.Body.Bytes(); !bytes.Equal(got, want) {
		t.Fatalf("client body = %d bytes, want %d bytes (content differs)", len(got), len(want))
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

// TestOversizedResponseIsNotTruncatedByResponseHook is the end-to-end version:
// a plugin registers a response hook, the handler writes more than the cap, and
// the client must receive the original bytes plus the original headers, because
// the hook can no longer rewrite a response that switched to passthrough.
func TestOversizedResponseIsNotTruncatedByResponseHook(t *testing.T) {
	withTempDataDir(t)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	Init(engine)

	zipPath := writePluginZip(t, map[string]string{
		"komari-plugin.json": `{"name":"Big","short":"big","version":"1.0.0","permissions":{"timeout":5,"allowHooks":true}}`,
		"script.js": `const server = require("server");
function load() {
  server.hook("response", (req, res) => {
    res.headers["x-hooked"] = "1";
    res.body = "rewritten";
  });
}
`,
	})
	if _, err := InstallZip(zipPath); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled("big", true, true); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = SetEnabled("big", false, false) }()

	payload := bytes.Repeat([]byte("0123456789abcdef"), (2<<20)/16+64)
	engine.GET("/big", func(c *gin.Context) {
		c.Data(http.StatusOK, "application/octet-stream", payload)
	})

	rec := httptest.NewRecorder()
	WrapHandler(engine).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/big", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Body.Bytes(); !bytes.Equal(got, payload) {
		t.Fatalf("client body = %d bytes, want the %d-byte payload unchanged", len(got), len(payload))
	}
	if got := rec.Header().Get("x-hooked"); got != "" {
		t.Fatalf("oversized response was rewritten by the response hook: x-hooked=%q", got)
	}
	if !strings.Contains(GetLogs("big"), "sent to the client unmodified") {
		t.Fatalf("expected the passthrough to be logged; logs = %q", GetLogs("big"))
	}
}

// recordingHookTimer is a fake hookTimer that records Stop calls, so the
// early-completion path can be asserted without depending on Go's timer
// internals.
type recordingHookTimer struct {
	ch    chan time.Time
	stops int
}

func (t *recordingHookTimer) C() <-chan time.Time { return t.ch }
func (t *recordingHookTimer) Stop()               { t.stops++ }

func TestWaitHookTurnStopsTimerWhenTheCallbackFinishesFirst(t *testing.T) {
	done := make(chan struct{})
	close(done)
	timer := &recordingHookTimer{ch: make(chan time.Time)}

	var requested time.Duration
	previous := newHookTimer
	newHookTimer = func(d time.Duration) hookTimer {
		requested = d
		return timer
	}
	t.Cleanup(func() { newHookTimer = previous })

	const budget = 30 * time.Second
	if timedOut := waitHookTurn(done, budget, newHookTimer); timedOut {
		t.Fatal("waitHookTurn reported a timeout for an already-finished callback")
	}
	if timer.stops != 1 {
		t.Fatalf("timer stopped %d times, want exactly 1 (a fast hook must not leave a timer to fire later)", timer.stops)
	}
	if requested != budget {
		t.Fatalf("timer created with %s, want %s", requested, budget)
	}
}

func TestWaitHookTurnReportsTimeout(t *testing.T) {
	done := make(chan struct{})
	defer close(done)
	start := time.Now()
	if timedOut := waitHookTurn(done, 5*time.Millisecond, newHookTimer); !timedOut {
		t.Fatal("waitHookTurn did not report the timeout")
	}
	if elapsed := time.Since(start); elapsed < 5*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("timeout returned after %s, want about 5ms", elapsed)
	}
}
