package client

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
	"github.com/Aone2233/nekomari/web/api"
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReportLimitsApplyAfterDecompression(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), int(api.MaxControlBody)+1)
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	_, _ = writer.Write(payload)
	_ = writer.Close()
	req := httptest.NewRequest("POST", "/rpc", &compressed)
	req.Header.Set("Content-Encoding", "gzip")
	if _, err := readMaybeCompressedBody(req, api.MaxControlBody); err == nil {
		t.Fatal("gzip expansion bypassed limit")
	}
	req = httptest.NewRequest("POST", "/rpc", bytes.NewReader(payload))
	if _, err := readMaybeCompressedBody(req, api.MaxControlBody); err == nil {
		t.Fatal("oversized plain report accepted")
	}
}

// The endpoint, not just the helper: a directory listing above the browser
// control-body limit is accepted, and a report of the same size is refused. This
// is the property that widening the Agent channel must not give up.
func TestUploadV2RPCSizeLimitDependsOnMethod(t *testing.T) {
	gin.SetMode(gin.TestMode)
	if api.MaxAgentControlBody <= api.MaxControlBody {
		t.Fatal("Agent bound leaves no room for a directory listing")
	}
	oversized := string(bytes.Repeat([]byte("x"), int(api.MaxControlBody)+1024))

	upload := func(method string, params map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		body, err := json.Marshal(v2.Request{JSONRPC: v2.Version, Method: method, Params: params})
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/api/clients/v2/rpc", bytes.NewReader(body))
		c.Set("client_uuid", "test-client")
		UploadV2RPC(c)
		return w
	}

	report := upload(v2.MethodAgentReport, map[string]any{"report": oversized})
	if report.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized report status %d, want %d", report.Code, http.StatusRequestEntityTooLarge)
	}

	// The filesystem result is not refused for its size; it fails later because the
	// request id is not pending, which is what proves it got past the size check.
	listing := upload(v2.MethodAgentFileResult, map[string]any{
		"uuid": "test-client", "request_id": "not-pending", "ok": true, "result": oversized,
	})
	if listing.Code == http.StatusRequestEntityTooLarge {
		t.Fatal("directory listing refused by the control-body limit")
	}
}
