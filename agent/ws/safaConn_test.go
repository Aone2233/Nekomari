package ws

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

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
