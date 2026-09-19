package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestClientIPIgnoresForwardedHeaderFromUntrustedPeer 固定住这次加固的核心：转发头
// 只有在直连来源是可信代理时才被采信。
//
// Gin 默认信任【所有】代理，那样任何能直连面板的人都能用 X-Forwarded-For 伪造
// ClientIP —— 会话记录、审计日志与访客限流都依赖它。
func TestClientIPIgnoresForwardedHeaderFromUntrustedPeer(t *testing.T) {
	engine := gin.New()
	configureTrustedProxies(engine)
	engine.GET("/whoami", func(c *gin.Context) { c.String(http.StatusOK, c.ClientIP()) })

	cases := []struct {
		name       string
		remoteAddr string
		xff        string
		want       string
	}{
		{"untrusted peer: header ignored", "203.0.113.9:40000", "1.2.3.4", "203.0.113.9"},
		{"loopback peer: header trusted", "127.0.0.1:40000", "1.2.3.4", "1.2.3.4"},
		{"private peer: header trusted", "10.1.2.3:40000", "5.6.7.8", "5.6.7.8"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/whoami", nil)
			req.RemoteAddr = tc.remoteAddr
			req.Header.Set("X-Forwarded-For", tc.xff)
			rec := httptest.NewRecorder()

			engine.ServeHTTP(rec, req)

			if got := rec.Body.String(); got != tc.want {
				t.Errorf("ClientIP() = %q, want %q", got, tc.want)
			}
		})
	}
}
