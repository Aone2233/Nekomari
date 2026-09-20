package api

import (
	"github.com/gin-gonic/gin"
	"net/http"
	"strings"
)

// MaxControlBody bounds browser JSON control messages. They are small and must be
// bounded before decoding.
const MaxControlBody int64 = 1 << 20

// MaxAgentControlBody bounds the Agent v2 control channel. That channel is not
// only reports: it also carries filesystem metadata, and a directory listing for a
// large directory is legitimately bigger than a browser control body. At 1 MiB the
// panel answered 413, the Agent treated the 4xx as final, and the operator saw a
// file-operation timeout instead of the directory. The bound stays finite so a
// buggy or compromised Agent still cannot stream an unbounded body into memory;
// reports keep the tighter MaxControlBody (see UploadV2RPC).
const MaxAgentControlBody int64 = 8 << 20

// agentControlPath is the Agent's JSON-RPC entry point, over both HTTP POST and
// WebSocket.
const agentControlPath = "/api/clients/v2/rpc"

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
			limit := MaxControlBody
			if path == agentControlPath {
				limit = MaxAgentControlBody
			}
			if c.Request.ContentLength > limit {
				c.AbortWithStatus(http.StatusRequestEntityTooLarge)
				return
			}
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		}
		c.Next()
	}
}
