package api

import (
	"github.com/gin-gonic/gin"
	"net/http"
	"strings"
)

const MaxControlBody int64 = 1 << 20

// ControlBodyLimit leaves authenticated binary transfers to their own streaming
// limits. JSON control messages are small and must be bounded before decoding.
func ControlBodyLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		stream := strings.HasPrefix(path, "/api/admin/upload/") ||
			strings.HasPrefix(path, "/api/clients/transfer/") ||
			(strings.HasPrefix(path, "/api/admin/client/") && strings.HasSuffix(path, "/file/upload")) ||
			path == "/api/admin/update/favicon"
		if strings.HasPrefix(path, "/api/") && !stream && c.Request.Body != nil {
			if c.Request.ContentLength > MaxControlBody {
				c.AbortWithStatus(http.StatusRequestEntityTooLarge)
				return
			}
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxControlBody)
		}
		c.Next()
	}
}
