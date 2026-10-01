package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Aone2233/nekomari/agent/dnsresolver"
	"github.com/Aone2233/nekomari/agent/monitoring"
	v2 "github.com/Aone2233/nekomari/agent/protocol/v2"
	"github.com/Aone2233/nekomari/agent/terminal"
	"github.com/Aone2233/nekomari/agent/update"
	"github.com/Aone2233/nekomari/agent/utils"
	"github.com/Aone2233/nekomari/agent/ws"
	"github.com/gorilla/websocket"
)

var (
	v2AckMu       sync.Mutex
	v2AckEventIDs []string
	v2SeenEvents  = make(map[string]time.Time)
)

const (
	v2SeenEventTTL   = 10 * time.Minute
	v2SeenEventLimit = 4096
)

func EstablishWebSocketConnection() {
	var conn *ws.SafeConn
	defer func() {
		if conn != nil {
			conn.Close()
		}
	}()
	var err error
	interval := math.Max(1, flags.Interval)

	// Connection recovery must not wait for the (possibly much longer) report
	// interval. Poll the connection frequently and gate reports separately.
	dataTicker := time.NewTicker(time.Second)
	defer dataTicker.Stop()
	reportInterval := time.Duration(interval * float64(time.Second))
	nextReportAt := time.Now()

	heartbeatTicker := time.NewTicker(30 * time.Second)
	defer heartbeatTicker.Stop()

	var readDone <-chan struct{}

	for {
		select {
		case <-dataTicker.C:
			if conn == nil {
				log.Println("Attempting to connect to WebSocket...")
				retry := 0
				for retry <= flags.MaxRetries {
					if retry > 0 {
						log.Println("Retrying websocket connection, attempt:", retry)
					}
					websocketEndpoint := buildWebSocketEndpoint()
					conn, err = connectWebSocket(websocketEndpoint)
					if err == nil {
						log.Println("WebSocket connected using v2 protocol")
						wsConnectFailures.reset()
						done := make(chan struct{})
						readDone = done
						go handleWebSocketMessages(conn, done)
						break
					} else {
						log.Println("Failed to connect to WebSocket:", err)
					}
					retry++
					// 退避而不是固定间隔：连不上时每次尝试都注定失败，而固定间隔
					// 会让每个节点在整段故障期间持续敲、持续写日志。
					time.Sleep(reconnectBackoff(wsConnectFailures.fail()))
				}

				if retry > flags.MaxRetries {
					log.Println("Max retries reached.")
					conn, err = runPostFallback(buildWebSocketEndpoint(), interval)
					if err != nil {
						log.Println("POST fallback stopped:", err)
						return
					}
					log.Println("WebSocket recovered from POST fallback")
					done := make(chan struct{})
					readDone = done
					go handleWebSocketMessages(conn, done)
				}
			}
			if conn == nil || time.Now().Before(nextReportAt) {
				continue
			}
			nextReportAt = time.Now().Add(reportInterval)

			data := v2.BuildReportPayload(monitoring.GenerateReport())
			err = conn.WriteMessage(websocket.TextMessage, data)
			if err != nil {
				log.Println("Failed to send WebSocket message:", err)
				conn.Close()
				conn = nil // Mark connection as dead
				readDone = nil
				continue
			}
		case <-heartbeatTicker.C:
			if conn != nil {
				err := conn.WriteMessage(websocket.PingMessage, nil)
				if err != nil {
					log.Println("Failed to send heartbeat:", err)
					conn.Close()
					conn = nil // Mark connection as dead
					readDone = nil
				}
			}
		case <-readDone:
			log.Println("WebSocket disconnected")
			if conn != nil {
				conn.Close()
				conn = nil
			}
			readDone = nil
		}
	}
}

func buildWebSocketEndpoint() string {
	websocketEndpoint := v2RPCEndpoint()
	websocketEndpoint = "ws" + strings.TrimPrefix(websocketEndpoint, "http")
	if convertedEndpoint, err := utils.ConvertIDNToASCII(websocketEndpoint); err == nil {
		return convertedEndpoint
	} else {
		log.Printf("Warning: Failed to convert WebSocket IDN to ASCII: %v", err)
	}
	return websocketEndpoint
}

func runPostFallback(websocketEndpoint string, interval float64) (*ws.SafeConn, error) {
	log.Println("Entering v2 POST fallback mode")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runV2PullLoop(ctx)

	reportTicker := time.NewTicker(time.Duration(interval * float64(time.Second)))
	defer reportTicker.Stop()
	// 重连用计时器而不是固定 ticker：间隔随连续失败次数增长。
	reconnectTimer := time.NewTimer(reconnectBackoff(wsConnectFailures.current() + 1))
	defer reconnectTimer.Stop()

	// 报告失败也限流：面板不可达时它每 interval 秒失败一次，不做限流会变成
	// 每 5 秒一行日志 —— 那本身就是第二场事故。
	postFailed := false

	for {
		select {
		case <-reportTicker.C:
			reportID := fmt.Sprintf("report-%d", time.Now().UnixNano())
			ackIDs := snapshotV2AckEventIDs()
			resp, err := postV2Request(v2.BuildReportRequest(reportID, monitoring.GenerateReport(), ackIDs))
			if err != nil {
				if !postFailed {
					postFailed = true
					log.Println("Failed to POST v2 report (repeats suppressed until it recovers):", err)
				}
				continue
			}
			if postFailed {
				postFailed = false
				log.Println("v2 POST fallback is reporting again")
			}
			clearV2AckEventIDs(ackIDs)
			processV2ResponseEvents(resp)
		case <-reconnectTimer.C:
			conn, err := connectWebSocket(websocketEndpoint)
			if err == nil {
				wsConnectFailures.reset()
				return conn, nil
			}
			log.Println("POST fallback WebSocket recovery failed:", err)
			reconnectTimer.Reset(reconnectBackoff(wsConnectFailures.fail()))
		}
	}
}

func runV2PullLoop(ctx context.Context) {
	// 拉取失败也限流：面板不可达时这个循环每几秒失败一次，不做限流就是每几秒
	// 一行日志。与报告路径同理。
	pullFailed := false
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		pullID := fmt.Sprintf("pull-%d", time.Now().UnixNano())
		ackIDs := snapshotV2AckEventIDs()
		payload := v2.NewRequest(pullID, v2.MethodAgentPull, map[string]interface{}{
			"capabilities":  []string{"exec", "ping", "message", "event", "terminal", "file"},
			"ack_event_ids": ackIDs,
		})
		resp, err := postV2RequestContext(ctx, payload)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if !pullFailed {
				pullFailed = true
				log.Println("Failed to POST v2 pull (repeats suppressed until it recovers):", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(reconnectBackoff(v2PullFailures.fail())):
			}
			continue
		}
		if pullFailed {
			pullFailed = false
			log.Println("v2 pull is working again")
		}
		clearV2AckEventIDs(ackIDs)
		processV2ResponseEvents(resp)
	}
}

func postV2Request(payload []byte) (*v2.Response, error) {
	return postV2RequestContext(context.Background(), payload)
}

func postV2RequestContext(ctx context.Context, payload []byte) (*v2.Response, error) {
	endpoint := v2RPCEndpoint()
	body := payload
	compressed := false
	if !flags.DisableCompression {
		if gz, err := gzipBytes(payload); err == nil {
			body = gz
			compressed = true
		}
	}
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	setV2Auth(req)
	if compressed {
		req.Header.Set("Content-Encoding", "gzip")
	}
	client := dnsresolver.GetHTTPClientWithPreference(35*time.Second, flags.PreferIPVersion)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	bytesBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &httpStatusError{StatusCode: resp.StatusCode, Status: resp.Status, Body: string(bytesBody)}
	}
	rpcResp, err := parseV2Response(bytesBody)
	if err != nil {
		return nil, err
	}
	return rpcResp, nil
}

func processV2ResponseEvents(resp *v2.Response) {
	if resp == nil || resp.Result == nil {
		return
	}
	var result v2.EventResult
	if err := v2.BindResult(resp.Result, &result); err != nil {
		log.Println("Failed to bind v2 event result:", err)
		return
	}
	for _, event := range result.Events {
		if processV2Event(nil, event.Method, event.Params, event.ID) {
			addV2AckEventID(event.ID)
		}
	}
}

func snapshotV2AckEventIDs() []string {
	v2AckMu.Lock()
	defer v2AckMu.Unlock()
	return append([]string{}, v2AckEventIDs...)
}

func clearV2AckEventIDs(sent []string) {
	if len(sent) == 0 {
		return
	}
	sentSet := make(map[string]struct{}, len(sent))
	for _, id := range sent {
		sentSet[id] = struct{}{}
	}
	v2AckMu.Lock()
	defer v2AckMu.Unlock()
	remaining := v2AckEventIDs[:0]
	for _, id := range v2AckEventIDs {
		if _, ok := sentSet[id]; !ok {
			remaining = append(remaining, id)
		}
	}
	v2AckEventIDs = remaining
}

func addV2AckEventID(id string) {
	if id == "" {
		return
	}
	v2AckMu.Lock()
	defer v2AckMu.Unlock()
	v2AckEventIDs = append(v2AckEventIDs, id)
}

func markV2EventSeen(id string) bool {
	if id == "" {
		return true
	}
	v2AckMu.Lock()
	defer v2AckMu.Unlock()
	now := time.Now()
	for eventID, seenAt := range v2SeenEvents {
		if now.Sub(seenAt) > v2SeenEventTTL {
			delete(v2SeenEvents, eventID)
		}
	}
	if _, ok := v2SeenEvents[id]; ok {
		return false
	}
	if len(v2SeenEvents) >= v2SeenEventLimit {
		var oldestID string
		var oldest time.Time
		for eventID, seenAt := range v2SeenEvents {
			if oldestID == "" || seenAt.Before(oldest) {
				oldestID, oldest = eventID, seenAt
			}
		}
		if oldestID != "" {
			delete(v2SeenEvents, oldestID)
		}
	}
	v2SeenEvents[id] = now
	return true
}

func connectWebSocket(websocketEndpoint string) (*ws.SafeConn, error) {
	dialer := newWSDialer()

	conn, resp, err := dialer.Dial(websocketEndpoint, v2RPCAuthHeaders())
	if err != nil {
		if resp != nil && resp.StatusCode != 101 {
			return nil, &httpStatusError{StatusCode: resp.StatusCode, Status: resp.Status}
		}
		return nil, err
	}

	return ws.NewSafeConn(conn), nil
}

func handleWebSocketMessages(conn *ws.SafeConn, done chan<- struct{}) {
	defer close(done)
	for {
		_, message_raw, err := conn.ReadMessage()
		if err != nil {
			log.Println("WebSocket read error:", err)
			return
		}
		var message v2.Request
		err = json.Unmarshal(message_raw, &message)
		if err != nil {
			log.Println("Bad ws message:", err)
			continue
		}
		if message.JSONRPC != v2.Version {
			log.Printf("Bad v2 ws message version %q", message.JSONRPC)
			continue
		}
		processV2Event(conn, message.Method, message.Params, "")
	}
}

func processV2Event(conn *ws.SafeConn, method string, params interface{}, eventID string) bool {
	if !markV2EventSeen(eventID) {
		return true
	}
	switch method {
	case v2.MethodAgentExec:
		var p struct {
			TaskID  string `json:"task_id"`
			Command string `json:"command"`
		}
		if err := v2.BindParams(params, &p); err == nil {
			go NewTask(p.TaskID, p.Command)
			return true
		} else {
			log.Printf("bad v2 exec params: %v", err)
		}
	case v2.MethodAgentPing:
		var p struct {
			TaskID    uint   `json:"ping_task_id"`
			Type      string `json:"ping_type"`
			Target    string `json:"ping_target"`
			Reference string `json:"ping_reference"`
		}
		if err := v2.BindParams(params, &p); err == nil {
			go NewPingTask(conn, p.TaskID, p.Type, p.Target, p.Reference)
			return true
		} else {
			log.Printf("bad v2 ping params: %v", err)
		}
	case v2.MethodAgentTerminal:
		var p struct {
			RequestID string `json:"request_id"`
		}
		if err := v2.BindParams(params, &p); err == nil {
			go establishTerminalConnection(p.RequestID, flags.Endpoint)
			return true
		} else {
			log.Printf("bad v2 terminal params: %v", err)
		}
	case v2.MethodAgentUpdate:
		var p v2.UpdateParams
		bindErr := v2.BindParams(params, &p)
		if bindErr == nil {
			// Recorded first, then acted on, so that a failed install still leaves the node pinned to what the
			// panel asked for. The alternative — recording only on success — would make a node whose install
			// failed drift back to tracking releases, which is the opposite of what an operator who set a
			// target wants and would be invisible from the panel.
			update.SetRequestedVersion(p.Target)
			if p.Target == "" {
				log.Println("Panel cleared the target version; this node will track releases again.")
				return true
			}
			go func(target string) {
				if update.TargetSatisfied(target) {
					log.Printf("Already running the panel's target version %s.", target)
					return
				}
				if err := update.CheckAndUpdateTo(target); err != nil {
					// Logged, not swallowed: the panel can see the version that is still running, and this is
					// what says why.
					log.Println("Panel-requested update failed:", err)
				}
			}(p.Target)
			return true
		}
		log.Printf("bad v2 update params: %v", bindErr)
	case v2.MethodAgentMessage, v2.MethodAgentEvent:
		log.Printf("received v2 %s: %+v", method, params)
		return true
	case v2.MethodAgentFile:
		var operation v2.FileOperation
		if err := v2.BindParams(params, &operation); err == nil {
			go handleFileOperation(operation)
			return true
		} else {
			log.Printf("bad v2 file params: %v", err)
		}
	default:
		log.Printf("unknown v2 event method %s", method)
	}
	return false
}

// connectWebSocket attempts to establish a WebSocket connection and upload basic info

// terminalEndpoint 返回终端握手端点，**不带任何凭据**。
//
// 与 v2RPCEndpoint 同一原则：URL 的查询串会被 nginx / Cloudflare 的访问日志完整
// 记录，也会在请求出错时被原样打进各节点自己的 journal。终端握手此前是
// `/api/clients/terminal?token=<token>&id=<id>`，也就是 T5 把 RPC 链路改走请求头
// 之后，agent 侧残留的最后一个"把凭据放进 URL"的调用点。服务端该路由在
// RequireRole 组里、由 IdentityMiddleware 识别身份，所以凭据完全可以走请求头
// （见 v2RPCAuthHeaders）。`id` 是会话标识而不是凭据，保留在查询串里；对它做转义，
// 避免请求 ID 中的字符拼出额外的查询参数。
func terminalEndpoint(id, endpoint string) string {
	uri := strings.TrimSuffix(endpoint, "/") + "/api/clients/terminal?id=" + url.QueryEscape(id)
	uri = "ws" + strings.TrimPrefix(uri, "http")

	// 转换中文域名为 ASCII 兼容编码
	if convertedEndpoint, err := utils.ConvertIDNToASCII(uri); err == nil {
		uri = convertedEndpoint
	} else {
		log.Printf("Warning: Failed to convert Terminal WebSocket IDN to ASCII: %v", err)
	}

	return uri
}

// establishTerminalConnection 建立终端连接并使用terminal包处理终端操作
func establishTerminalConnection(id, endpoint string) {
	// 使用与主 WS 相同的拨号策略；凭据在握手请求头里，不在 URL 里。
	dialer := newWSDialer()

	conn, _, err := dialer.Dial(terminalEndpoint(id, endpoint), v2RPCAuthHeaders())
	if err != nil {
		log.Println("Failed to establish terminal connection:", err)
		return
	}

	// 启动终端
	terminal.StartTerminal(conn, id)
	if conn != nil {
		conn.Close()
	}
}

// newWSDialer 构造统一的 WebSocket 拨号器（自定义解析、IPv4/IPv6 动态排序、可选 TLS 忽略）
func newWSDialer() *websocket.Dialer {
	d := &websocket.Dialer{
		HandshakeTimeout:  15 * time.Second,
		NetDialContext:    dnsresolver.GetDialContextWithPreference(15*time.Second, flags.PreferIPVersion),
		Proxy:             http.ProxyFromEnvironment,
		EnableCompression: !flags.DisableCompression,
	}
	if flags.IgnoreUnsafeCert {
		d.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return d
}
