package ws

import (
	"context"
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

// WriteJSONContext does not queue behind a busy writer. Cancellation closes
// the socket to interrupt an in-progress network write.
func (sc *SafeConn) WriteJSONContext(ctx context.Context, v interface{}) error {
	if ctx == nil {
		return errors.New("nil write context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return errors.New("websocket context write requires a deadline")
	}
	if !sc.mu.TryLock() {
		return errors.New("websocket writer busy")
	}
	if sc.conn == nil {
		sc.mu.Unlock()
		return errors.New("nil websocket")
	}
	done := make(chan error, 1)
	go func() {
		defer sc.mu.Unlock()
		if err := ctx.Err(); err != nil {
			done <- err
			return
		}
		if err := sc.conn.SetWriteDeadline(deadline); err != nil {
			sc.Abort()
			done <- err
			return
		}
		err := sc.conn.WriteJSON(v)
		if restoreErr := sc.conn.SetWriteDeadline(time.Time{}); err == nil {
			err = restoreErr
		}
		if err != nil {
			sc.Abort()
		}
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		sc.Abort()
		return ctx.Err()
	}
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
