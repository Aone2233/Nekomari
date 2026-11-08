package ws

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestWriteJSONContextRestoresDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			kind, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if err := conn.WriteMessage(kind, data); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	raw, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	conn := NewSafeConn(raw)
	defer conn.Close()
	if err := conn.WriteJSONContext(context.Background(), "missing deadline"); err == nil {
		t.Fatal("accepted missing deadline")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := conn.WriteJSONContext(ctx, "first"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatal(err)
	}
	<-ctx.Done()
	if err := conn.WriteJSON("after deadline"); err != nil {
		t.Fatalf("old deadline broke normal writes: %v", err)
	}
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatal(err)
	}
}

func TestWriteJSONContextSkipsBusyWriter(t *testing.T) {
	conn := &SafeConn{}
	conn.mu.Lock()
	defer conn.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := conn.WriteJSONContext(ctx, "busy"); err == nil {
		t.Fatal("busy writer accepted")
	}
}

func TestWriteJSONContextWaitHonorsDeadlineWhenWriterBusy(t *testing.T) {
	conn := &SafeConn{}
	conn.mu.Lock()
	defer conn.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := conn.WriteJSONContextWait(ctx, "busy"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
}

func TestWriteJSONContextWaitAllowsWriterToFinish(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		kind, data, err := conn.ReadMessage()
		if err == nil {
			_ = conn.WriteMessage(kind, data)
		}
	}))
	defer server.Close()
	raw, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	conn := NewSafeConn(raw)
	defer conn.Close()
	conn.mu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- conn.WriteJSONContextWait(ctx, "control") }()
	time.Sleep(20 * time.Millisecond)
	select {
	case err := <-done:
		conn.mu.Unlock()
		t.Fatalf("write returned before writer released lock: %v", err)
	default:
	}
	conn.mu.Unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatal(err)
	}
}

func TestReadMessageLimitedCountsDecompressedBytes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{EnableCompression: true}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.WriteMessage(websocket.TextMessage, bytes.Repeat([]byte("a"), 1025))
	}))
	defer server.Close()
	dialer := websocket.Dialer{EnableCompression: true}
	conn, _, err := dialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, _, err := NewSafeConn(conn).ReadMessageLimited(1024); !errors.Is(err, errFrameTooLarge) {
		t.Fatalf("error = %v, want decompressed size limit", err)
	}
}

func TestReadMessageLimitedAcceptsBoundary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.WriteMessage(websocket.TextMessage, bytes.Repeat([]byte("x"), 1024))
	}))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, data, err := NewSafeConn(conn).ReadMessageLimited(1024)
	if err != nil || len(data) != 1024 {
		t.Fatalf("size=%d err=%v", len(data), err)
	}
}
