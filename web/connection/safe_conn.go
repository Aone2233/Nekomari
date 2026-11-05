package connection

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ConnInfo describes one WebSocket connection passed to interceptor
// callbacks. ID matches the owning SafeConn.
type ConnInfo struct {
	ID         int64
	Path       string
	RemoteIP   string
	UserAgent  string
	ClientUUID string
}

// FrameInterceptor is the optional hook chain around WebSocket connections
// and frames. The provider is wired once at startup by internal/server (the
// plugin manager); a nil interceptor leaves every connection untouched.
type FrameInterceptor interface {
	// OnConnect runs right after the upgrade. A denied connection is closed
	// by the caller; plugins bear the consequences of rejecting endpoints
	// such as /api/rpc2.
	OnConnect(info *ConnInfo) (deny bool, reason string)
	// OnMessage inspects one inbound frame; OnSend one outbound frame. They
	// return the possibly replaced payload and whether the frame is dropped.
	OnMessage(info *ConnInfo, frameType int, data []byte) (newType int, newData []byte, drop bool)
	OnSend(info *ConnInfo, frameType int, data []byte) (newType int, newData []byte, drop bool)
	// OnClose notifies that the connection ended.
	OnClose(info *ConnInfo)
}

// frameInterceptor is the process-wide WebSocket hook provider.
var frameInterceptor FrameInterceptor

// SetFrameInterceptor wires the plugin hook chain into every WebSocket
// connection. It must be called once at startup, before connections arrive.
func SetFrameInterceptor(i FrameInterceptor) { frameInterceptor = i }

// Interceptor returns the process-wide frame interceptor, or nil when no
// provider is wired.
func Interceptor() FrameInterceptor { return frameInterceptor }

// errTooManyDroppedFrames fails the read loop after a plugin dropped too many
// frames in a row, so a connection on a fully filtered endpoint cannot spin
// forever.
var errTooManyDroppedFrames = errors.New("too many frames dropped by plugin hooks")

// maxConsecutiveDroppedFrames bounds the internal re-read loop.
const maxConsecutiveDroppedFrames = 16

// SafeConn wraps a websocket connection with a mutex so concurrent writes
// cannot interleave frames, and funnels every frame through the optional
// plugin interceptor when one is attached.
type SafeConn struct {
	conn          *websocket.Conn
	mu            sync.Mutex
	writeDeadline time.Time
	ID            int64

	interceptor FrameInterceptor
	info        *ConnInfo
}

func NewSafeConn(conn *websocket.Conn) *SafeConn {
	return &SafeConn{
		conn: conn,
		mu:   sync.Mutex{},
		ID:   time.Now().UnixNano(),
	}
}

func (sc *SafeConn) SetReadLimit(limit int64) { sc.conn.SetReadLimit(limit) }
func (sc *SafeConn) SetWriteDeadline(t time.Time) error {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if err := sc.conn.SetWriteDeadline(t); err != nil {
		return err
	}
	sc.writeDeadline = t
	return nil
}

// SetInterceptor attaches the hook provider and the connection identity. It
// must be called before any frame is read or written.
func (sc *SafeConn) SetInterceptor(info *ConnInfo, interceptor FrameInterceptor) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.info = info
	sc.interceptor = interceptor
}

// readFrame reads one frame and runs the wsMessage hook chain. Frames
// dropped by hooks are transparently skipped, so callers only ever see
// processed frames.
func (sc *SafeConn) readFrame() (int, []byte, error) {
	for dropped := 0; ; {
		messageType, data, err := sc.conn.ReadMessage()
		if err != nil {
			return 0, nil, err
		}
		sc.mu.Lock()
		interceptor, info := sc.interceptor, sc.info
		sc.mu.Unlock()
		if interceptor == nil || info == nil {
			return messageType, data, nil
		}
		newType, newData, drop := interceptor.OnMessage(info, messageType, data)
		if !drop {
			return newType, newData, nil
		}
		if dropped++; dropped >= maxConsecutiveDroppedFrames {
			return 0, nil, errTooManyDroppedFrames
		}
	}
}

// writeFrame runs the wsSend hook chain then writes one frame. A dropped
// frame is silently skipped; a nil interceptor writes through immediately.
func (sc *SafeConn) writeFrame(messageType int, data []byte) error {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sc.interceptor == nil || sc.info == nil {
		return sc.conn.WriteMessage(messageType, data)
	}
	newType, newData, drop := sc.interceptor.OnSend(sc.info, messageType, data)
	if drop {
		return nil
	}
	return sc.conn.WriteMessage(newType, newData)
}

func (sc *SafeConn) WriteMessage(messageType int, data []byte) error {
	return sc.writeFrame(messageType, data)
}

// WriteJSON encodes v then sends it as one text frame through the wsSend
// hook chain.
func (sc *SafeConn) WriteJSON(v interface{}) error {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(v); err != nil {
		return err
	}
	return sc.writeFrame(websocket.TextMessage, buf.Bytes())
}

// A stuck interceptor cannot be interrupted by closing a socket. Bound the
// number of workers it can strand; saturation skips pinned work rather than
// accumulating goroutines.
var contextWriteSlots = make(chan struct{}, 64)

var errContextWriterBusy = errors.New("websocket context writer unavailable")

// WriteJSONContext requires a deadline and skips a busy writer rather than
// waiting behind it. Cancellation aborts the socket even if an interceptor
// stalls; any stranded worker counts against contextWriteSlots. A successful
// write restores the deadline previously set by the caller.
func (sc *SafeConn) WriteJSONContext(ctx context.Context, v interface{}) error {
	if ctx == nil {
		return errors.New("nil write context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, ok := ctx.Deadline(); !ok {
		return errors.New("websocket context write requires a deadline")
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(v); err != nil {
		return err
	}
	if !sc.mu.TryLock() {
		return errContextWriterBusy
	}
	if sc.conn == nil {
		sc.mu.Unlock()
		return errContextWriterBusy
	}
	select {
	case contextWriteSlots <- struct{}{}:
	default:
		sc.mu.Unlock()
		return errContextWriterBusy
	}
	done := make(chan error, 1)
	go func() {
		defer func() { <-contextWriteSlots }()
		err := func() (err error) {
			defer sc.mu.Unlock()
			defer func() {
				if err != nil {
					sc.Abort()
				}
			}()
			if err = ctx.Err(); err != nil {
				return err
			}
			if deadline, ok := ctx.Deadline(); ok {
				previous := sc.writeDeadline
				if !previous.IsZero() && previous.Before(deadline) {
					deadline = previous
				}
				if err = sc.conn.SetWriteDeadline(deadline); err != nil {
					return err
				}
				defer func() {
					if restoreErr := sc.conn.SetWriteDeadline(previous); err == nil {
						err = restoreErr
					}
				}()
			}
			kind, data := websocket.TextMessage, buf.Bytes()
			if sc.interceptor != nil && sc.info != nil {
				var drop bool
				kind, data, drop = sc.interceptor.OnSend(sc.info, kind, data)
				if drop {
					return errors.New("websocket frame dropped by interceptor")
				}
			}
			if err = ctx.Err(); err != nil {
				return err
			}
			return sc.conn.WriteMessage(kind, data)
		}()
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
	interceptor, info := sc.interceptor, sc.info
	sc.interceptor = nil // run OnClose exactly once
	err := sc.conn.Close()
	sc.mu.Unlock()
	if interceptor != nil && info != nil {
		interceptor.OnClose(info)
	}
	return err
}

// Abort interrupts blocked I/O without waiting for the writer/interceptor
// mutex. The owning handler still calls Close for normal lifecycle cleanup.
func (sc *SafeConn) Abort() {
	if sc.conn != nil {
		_ = sc.conn.Close()
	}
}

func (sc *SafeConn) ReadMessage() (int, []byte, error) {
	return sc.readFrame()
}

func (sc *SafeConn) ReadJSON(v interface{}) error {
	_, data, err := sc.readFrame()
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func (sc *SafeConn) SetReadDeadline(t time.Time) error {
	return sc.conn.SetReadDeadline(t)
}

func (sc *SafeConn) GetConn() *websocket.Conn {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return sc.conn
}

// SetCloseHandler forwards to the underlying connection (used by the
// terminal sessions to clean up on close).
func (sc *SafeConn) SetCloseHandler(h func(code int, text string) error) {
	sc.conn.SetCloseHandler(h)
}
