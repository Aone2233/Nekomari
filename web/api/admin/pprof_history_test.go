package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestPprofHistoryRejectsUnboundedAndDuplicateWindows(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, query := range []string{"?hours=0", "?hours=168", "?hours=1&hours=24", "?hours=", "?hours=1.0"} {
		response := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(response)
		ctx.Request = httptest.NewRequest(http.MethodGet, "/api/admin/pprof/history"+query, nil)
		pprofHistoryHandler(ctx)
		if response.Code != http.StatusBadRequest {
			t.Errorf("%s returned %d", query, response.Code)
		}
	}
}
