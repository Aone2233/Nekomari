package client

import (
	"bytes"
	"compress/gzip"
	"github.com/Aone2233/nekomari/web/api"
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
	if _, err := readMaybeCompressedBody(req); err == nil {
		t.Fatal("gzip expansion bypassed limit")
	}
	req = httptest.NewRequest("POST", "/rpc", bytes.NewReader(payload))
	if _, err := readMaybeCompressedBody(req); err == nil {
		t.Fatal("oversized plain report accepted")
	}
}
