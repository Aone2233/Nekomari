package terminal

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/Aone2233/nekomari/web/api"
	"github.com/gin-gonic/gin"
)

func EstablishConnection(c *gin.Context) {
	session_id := c.Query("id")
	TerminalSessionsMutex.Lock()
	session, exists := TerminalSessions[session_id]
	authenticatedUUID, _ := c.Get("client_uuid")
	authenticatedString, hasAuthenticatedUUID := authenticatedUUID.(string)
	if !exists || session == nil || session.Browser == nil ||
		(hasAuthenticatedUUID && authenticatedString != "" && authenticatedString != session.UUID) {
		TerminalSessionsMutex.Unlock()
		c.JSON(404, gin.H{"status": "error", "error": "Session not found"})
		return
	}
	uuid := session.UUID
	TerminalSessionsMutex.Unlock()
	operation := api.ClientOperation(c, uuid)
	// Upgrade the connection to WebSocket
	if !api.IsWebSocketUpgrade(c) {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "Require WebSocket upgrade"})
		return
	}
	conn, err := api.UpgradeSafeConn(c)
	if err != nil {
		return
	}
	conn.SetCloseHandler(func(code int, text string) error {
		suspendSession(session_id, nil, conn)
		return nil
	})
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	if err := operation(ctx, func() error {
		if _, ok := attachAgent(session_id, session, conn); !ok {
			return errors.New("terminal session retired")
		}
		return nil
	}); err != nil {
		conn.Close()
		return
	}
	maybeStartForwarding(session_id)
}
