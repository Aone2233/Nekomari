package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/web/connection"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func TestWebSocketLifecycleWaitsForSocketAndHandler(t *testing.T) {
	lifecycle := &websocketLifecycle{conns: make(map[*connection.SafeConn]struct{})}
	started, release := make(chan struct{}), make(chan struct{})
	router := gin.New()
	router.Use(lifecycle.middleware())
	router.GET("/socket", func(c *gin.Context) {
		raw, err := (&websocket.Upgrader{}).Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			return
		}
		conn := connection.NewSafeConn(raw)
		if !lifecycle.track(conn) {
			_ = conn.Close()
			return
		}
		defer conn.Close()
		close(started)
		_, _, _ = conn.ReadMessage()
		<-release // Simulate handler cleanup that outlives the socket.
	})
	server := httptest.NewServer(router)
	defer server.Close()
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/socket"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	<-started
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	err = lifecycle.stop(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stop before handler returned = %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("shutdown did not abort the socket")
	}
	if late, response, err := websocket.DefaultDialer.Dial(url, nil); err == nil {
		late.Close()
		t.Fatal("shutdown accepted another websocket")
	} else if response == nil || response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("late websocket: response=%v err=%v", response, err)
	}
	close(release)
	ctx, cancel = context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := lifecycle.stop(ctx); err != nil {
		t.Fatalf("stop retry: %v", err)
	}
}

func TestWebSocketLifecycleWaitsForDetachedSocketCloseHook(t *testing.T) {
	lifecycle := &websocketLifecycle{conns: make(map[*connection.SafeConn]struct{})}
	started, release := make(chan struct{}), make(chan struct{})
	router := gin.New()
	router.Use(lifecycle.middleware())
	router.GET("/socket", func(c *gin.Context) {
		raw, err := (&websocket.Upgrader{}).Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			return
		}
		conn := connection.NewSafeConn(raw)
		conn.SetInterceptor(&connection.ConnInfo{}, blockingCloseInterceptor{started: started, release: release})
		if !lifecycle.track(conn) {
			_ = conn.Close()
		}
	})
	server := httptest.NewServer(router)
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/socket", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	err = lifecycle.stop(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stop during close hook = %v", err)
	}
	<-started
	close(release)
	ctx, cancel = context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := lifecycle.stop(ctx); err != nil {
		t.Fatalf("stop after close hook: %v", err)
	}
}

type blockingCloseInterceptor struct{ started, release chan struct{} }

func (blockingCloseInterceptor) OnConnect(*connection.ConnInfo) (bool, string) { return false, "" }
func (blockingCloseInterceptor) OnMessage(_ *connection.ConnInfo, kind int, data []byte) (int, []byte, bool) {
	return kind, data, false
}
func (blockingCloseInterceptor) OnSend(_ *connection.ConnInfo, kind int, data []byte) (int, []byte, bool) {
	return kind, data, false
}
func (i blockingCloseInterceptor) OnClose(*connection.ConnInfo) {
	close(i.started)
	<-i.release
}
