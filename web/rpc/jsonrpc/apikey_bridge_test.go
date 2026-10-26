package jsonrpc

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Aone2233/nekomari/pkg/rpc"
	"github.com/gin-gonic/gin"
)

func TestLocalizedAPIKeyBridgePreservesLegacyErrors(t *testing.T) {
	for _, render := range []renderKind{renderStandard, renderFlat, renderRaw} {
		for _, localized := range []bool{false, true} {
			writer := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(writer)
			jerr := rpc.MakeError(rpc.InvalidParams, "legacy description", "private detail")
			if localized {
				jerr = rpc.MakeLocalizedError(rpc.InvalidParams, jerr.Message, "api_key_name_invalid")
			}
			renderResponse(c, &bindConfig{render: render}, jerr.Response())
			var body map[string]any
			if json.Unmarshal(writer.Body.Bytes(), &body) != nil || writer.Code != http.StatusBadRequest || body["status"] != "error" || body["message"] != "legacy description" {
				t.Fatalf("REST error contract changed: %d %s", writer.Code, writer.Body)
			}
			_, present := body["error_key"]
			if present != localized || body["data"] != nil || (localized && body["error_key"] != "api_key_name_invalid") {
				t.Fatal("legacy data leaked or localized metadata was lost")
			}
		}
	}
}
