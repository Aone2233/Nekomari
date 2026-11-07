package api

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"github.com/Aone2233/nekomari/web/connection"
	"github.com/gin-gonic/gin"
)

// A hijacked connection is not included in http.Server.Shutdown. Track both
// its request handler and the socket: terminal handlers can return while their
// connections continue to be used by separate forwarding goroutines.
type websocketLifecycle struct {
	mu       sync.Mutex
	stopping bool
	handlers int
	conns    map[*connection.SafeConn]struct{}
	drained  chan struct{}
}

var panelWebSockets = &websocketLifecycle{conns: make(map[*connection.SafeConn]struct{})}

var errWebSocketShuttingDown = errors.New("websocket server is shutting down")

func (l *websocketLifecycle) beginHandler() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.stopping {
		return false
	}
	l.handlers++
	return true
}

func (l *websocketLifecycle) endHandler() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.handlers--
	l.signalDrained()
}

// Called while l.mu is held. Stop may be retried after a deadline.
func (l *websocketLifecycle) signalDrained() {
	if l.stopping && l.handlers == 0 && len(l.conns) == 0 && l.drained != nil {
		close(l.drained)
		l.drained = nil
	}
}

func (l *websocketLifecycle) track(conn *connection.SafeConn) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.stopping {
		return false
	}
	conn.SetOnClose(func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		delete(l.conns, conn)
		l.signalDrained()
	})
	l.conns[conn] = struct{}{}
	return true
}

func (l *websocketLifecycle) middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !IsWebSocketUpgrade(c) {
			c.Next()
			return
		}
		if !l.beginHandler() {
			c.AbortWithStatus(http.StatusServiceUnavailable)
			return
		}
		defer l.endHandler()
		c.Next()
	}
}

func (l *websocketLifecycle) stop(ctx context.Context) error {
	if ctx == nil {
		return errors.New("websocket shutdown requires a context")
	}
	l.mu.Lock()
	var conns []*connection.SafeConn
	if !l.stopping {
		l.stopping = true
		if l.handlers != 0 || len(l.conns) != 0 {
			l.drained = make(chan struct{})
		}
		for conn := range l.conns {
			conns = append(conns, conn)
		}
	}
	drained := l.drained
	l.mu.Unlock()
	// Abort first without taking a write lock; a blocked hook or writer cannot
	// prevent the remaining connections from receiving the shutdown signal.
	for _, conn := range conns {
		conn.Abort()
	}
	for _, conn := range conns {
		go conn.Close()
	}
	if drained == nil {
		return nil
	}
	select {
	case <-drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// TrackWebSocketHandlers must wrap routes using UpgradeSafeConn. The lifecycle
// is terminal and belongs to the process, not an individual HTTP server.
func TrackWebSocketHandlers() gin.HandlerFunc { return panelWebSockets.middleware() }

func QuiesceWebSockets(ctx context.Context) error { return panelWebSockets.stop(ctx) }
