package admin

import (
	"bytes"
	"strings"
	"testing"
)

// A preview that exceeds the size limit must still return the part that fits, with a notice that says so.
//
// ## The defect
//
// `pprofPreviewBuffer` carried a comment saying it exists to "keep automatic browser previews bounded", and
// its `Write` returned an error when the limit was reached. That error travelled up through `collectPprof`
// and the handler turned it into **HTTP 413 with no body at all** — so the panel showed
//
//     profile text preview is too large; download the pprof file instead
//
// and nothing else. The intent was a bounded preview; the behaviour was no preview.
//
// It is not a rare edge either. Measured on a running panel, `heap` and `allocs` are ~70 KB as binary
// profiles and over the 1 MiB limit as `debug=1` text, so the two profiles an operator most wants to look at
// were the two that could not be viewed. The text form is a debug dump of the serialised protobuf, and
// truncating it mid-sample-list loses the largest entries — which is why the notice has to be explicit and
// why the binary download remains the answer for real analysis.
//
// So: truncation is the *designed* behaviour and must not be an error. What must be visible is that the
// content is incomplete.

func TestPreviewBufferKeepsThePartThatFits(t *testing.T) {
	buffer := &pprofPreviewBuffer{limit: 10}

	written, err := buffer.Write([]byte("0123456789abcdef"))
	if err != nil {
		t.Fatalf("writing more than the limit returned an error: %v\n"+
			"The buffer's purpose is to bound the preview; exceeding the bound is expected, not a failure.", err)
	}
	// The full length is reported **on purpose**, and this assertion exists so it is not "corrected" later. A
	// short write would be treated as `io.ErrShortWrite` by the profile writer's own `WriteTo` and abort the
	// collection at the first chunk past the limit, discarding everything after it. What was dropped is
	// recorded in `Truncated` instead.
	if written != 16 {
		t.Errorf("Write reported %d bytes accepted, want the full 16: a short write makes the collector stop "+
			"rather than continue into a bounded buffer, which is what truncation is for", written)
	}
	if got := buffer.String(); got != "0123456789" {
		t.Errorf("buffer holds %q, want the first 10 bytes", got)
	}
	if !buffer.Truncated() {
		t.Error("Truncated() is false after dropping 6 bytes, so the caller cannot say the preview is partial")
	}
}

// A preview that fits is not marked truncated, or every preview would carry a warning.
func TestPreviewBufferKnowsWhenItFits(t *testing.T) {
	buffer := &pprofPreviewBuffer{limit: 10}
	if _, err := buffer.Write([]byte("short")); err != nil {
		t.Fatalf("a write within the limit failed: %v", err)
	}
	if buffer.Truncated() {
		t.Error("Truncated() is true for content that fits")
	}
}

// Exactly filling the limit is not truncation. The boundary is where an off-by-one would hide: content of
// exactly `limit` bytes fits, and only the next byte is dropped.
func TestPreviewBufferBoundary(t *testing.T) {
	for _, tc := range []struct {
		name        string
		size        int
		wantTrunc   bool
		wantContent int
	}{
		{"one below", 9, false, 9},
		{"exactly", 10, false, 10},
		{"one above", 11, true, 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			buffer := &pprofPreviewBuffer{limit: 10}
			if _, err := buffer.Write(bytes.Repeat([]byte("x"), tc.size)); err != nil {
				t.Fatalf("write of %d bytes failed: %v", tc.size, err)
			}
			if got := buffer.Len(); got != tc.wantContent {
				t.Errorf("stored %d bytes, want %d", got, tc.wantContent)
			}
			if buffer.Truncated() != tc.wantTrunc {
				t.Errorf("Truncated() = %v, want %v", buffer.Truncated(), tc.wantTrunc)
			}
		})
	}
}

// Truncation written in several chunks is still detected, because the profile writer does not emit one large
// buffer — `WriteTo` streams, and a chunk boundary is where a naive check would miss.
func TestPreviewBufferDetectsTruncationAcrossWrites(t *testing.T) {
	buffer := &pprofPreviewBuffer{limit: 6}
	for _, chunk := range []string{"ab", "cd", "ef", "gh", "ij"} {
		if _, err := buffer.Write([]byte(chunk)); err != nil {
			t.Fatalf("chunk %q failed: %v", chunk, err)
		}
	}
	if got := buffer.String(); got != "abcdef" {
		t.Errorf("buffer holds %q, want abcdef", got)
	}
	if !buffer.Truncated() {
		t.Error("Truncated() is false although four bytes were dropped across later chunks")
	}
}

// A zero limit means "no limit", which is how the binary download path reuses this type. Treating zero as
// "truncate everything" would produce empty downloads.
func TestPreviewBufferWithNoLimitKeepsEverything(t *testing.T) {
	buffer := &pprofPreviewBuffer{}
	payload := bytes.Repeat([]byte("y"), 4096)
	if _, err := buffer.Write(payload); err != nil {
		t.Fatalf("unlimited write failed: %v", err)
	}
	if buffer.Len() != len(payload) {
		t.Errorf("stored %d of %d bytes; a zero limit must not truncate", buffer.Len(), len(payload))
	}
	if buffer.Truncated() {
		t.Error("Truncated() is true with no limit set")
	}
}

// The notice is what makes a partial preview honest, so it must say both things: that this is not everything,
// and what to do instead.
func TestTruncationNoticeExplainsItself(t *testing.T) {
	notice := pprofPreviewTruncationNotice(1 << 20)

	for _, want := range []string{"truncat", "download"} {
		if !strings.Contains(strings.ToLower(notice), want) {
			t.Errorf("the notice does not mention %q: %q", want, notice)
		}
	}
	// The size is stated so an operator can tell "just over" from "far over" before deciding to look at it.
	if !strings.Contains(notice, "1 MiB") && !strings.Contains(notice, "1048576") {
		t.Errorf("the notice does not state the limit: %q", notice)
	}
	// It is appended to plain text served as UTF-8, so it must not assume a markup renderer.
	if strings.Contains(notice, "<") || strings.Contains(notice, "&nbsp;") {
		t.Errorf("the notice contains markup but the response is text/plain: %q", notice)
	}
}

// Truncation is a condition the response reports, not a failure it returns.
//
// The type of the record is what this pins: `Truncated()` is the only signal, so a future change that
// reintroduces a returned error would have to delete this test and read the comment above that explains why.
func TestTruncationIsAConditionNotAFailure(t *testing.T) {
	buffer := &pprofPreviewBuffer{limit: 4}
	if _, err := buffer.Write([]byte("0123456789")); err != nil {
		t.Fatalf("exceeding the limit returned an error: %v", err)
	}
	if !buffer.Truncated() {
		t.Fatal("the buffer did not record truncation, so the handler has nothing to report")
	}
	// The content that fits is still there — that is the whole point of reporting rather than failing.
	if got := buffer.String(); got != "0123" {
		t.Errorf("buffer holds %q, want the first 4 bytes", got)
	}
}
