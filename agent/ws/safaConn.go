package ws

import (
	"errors"
	"io"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var errFrameTooLarge = errors.New("websocket frame exceeds read limit")

type SafeConn struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func NewSafeConn(conn *websocket.Conn) *SafeConn {
	return &SafeConn{
		conn: conn,
		mu:   sync.Mutex{},
	}
}

func (sc *SafeConn) WriteMessage(messageType int, data []byte) error {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return sc.conn.WriteMessage(messageType, data)
}

func (sc *SafeConn) WriteJSON(v interface{}) error {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return sc.conn.WriteJSON(v)
}

func (sc *SafeConn) Close() error {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return sc.conn.Close()
}

// Abort interrupts a stalled write without waiting on the writer mutex.
func (sc *SafeConn) Abort() {
	_ = sc.conn.Close()
}
func (sc *SafeConn) ReadMessage() (int, []byte, error) {
	// sc.mu.Lock()
	// defer sc.mu.Unlock()
	return sc.conn.ReadMessage()
}

// ReadMessageLimited bounds the decoded frame even when compression is enabled.
func (sc *SafeConn) ReadMessageLimited(limit int64) (int, []byte, error) {
	messageType, reader, err := sc.conn.NextReader()
	if err != nil {
		return 0, nil, err
	}
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return 0, nil, err
	}
	if int64(len(data)) > limit {
		return 0, nil, errFrameTooLarge
	}
	return messageType, data, nil
}
func (sc *SafeConn) ReadJSON(v interface{}) error {
	// sc.mu.Lock()
	// defer sc.mu.Unlock()
	return sc.conn.ReadJSON(v)
}
func (sc *SafeConn) SetReadDeadline(t time.Time) error {
	// sc.mu.Lock()
	// defer sc.mu.Unlock()
	return sc.conn.SetReadDeadline(t)
}
func (sc *SafeConn) GetConn() *websocket.Conn {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return sc.conn
}
