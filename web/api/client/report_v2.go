package client

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	logger "github.com/Aone2233/nekomari/utils/log"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Aone2233/nekomari/database/clients"
	"github.com/Aone2233/nekomari/database/tasks"
	"github.com/Aone2233/nekomari/database/unlock"
	"github.com/Aone2233/nekomari/internal/metricstore"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
	"github.com/Aone2233/nekomari/utils/notifier"
	agent_runtime "github.com/Aone2233/nekomari/web/agent"
	"github.com/Aone2233/nekomari/web/api"
	"github.com/Aone2233/nekomari/web/connection"
	"github.com/Aone2233/nekomari/web/filemanager"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func readMaybeCompressedBody(r *http.Request, limit int64) ([]byte, error) {
	defer r.Body.Close()
	if strings.EqualFold(r.Header.Get("Content-Encoding"), "gzip") {
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			return nil, err
		}
		defer zr.Close()
		return readLimitedReport(zr, limit)
	}
	return readLimitedReport(r.Body, limit)
}

func readLimitedReport(r io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("report exceeds body limit")
	}
	return body, err
}

// agentMessageWithinControlLimit classifies a decoded Agent message by size. Only
// filesystem metadata (a directory listing) may use the wider Agent bound; every
// other message, reports included, is held to the browser control-body limit.
func agentMessageWithinControlLimit(method string, size int64) bool {
	if method == v2.MethodAgentFileResult {
		return true
	}
	return size <= api.MaxControlBody
}

// handleV2RPC dispatches one decoded JSON-RPC request.
//
// req is a v2.RawRequest rather than a v2.Request so each method's typed params
// are unmarshalled straight from the bytes that arrived. Decoding the params as
// an untyped map first (which is what v2.Request.Params would do here) parses
// the whole report body into map[string]any and then re-marshals it to reach the
// typed struct: two extra passes over every report, on the hottest ingest path
// the panel has.
func handleV2RPC(uuid string, req v2.RawRequest, allowWait bool) v2.Response {
	return handleV2RPCOperation(context.Background(), uuid, req, allowWait, func(_ context.Context, fn func() error) error { return fn() })
}

type clientOperation func(context.Context, func() error) error

func handleV2RPCOperation(ctx context.Context, uuid string, req v2.RawRequest, allowWait bool, operation clientOperation) v2.Response {
	admit := func(fn func() error) error { return operation(ctx, fn) }
	failure := func(message string, err error) v2.Response {
		if errors.Is(err, api.ErrClientCredentialRetired) {
			return v2.Error(req.ID, -32001, "invalid token", nil)
		}
		return v2.Error(req.ID, -32000, message, err.Error())
	}
	if req.JSONRPC != v2.Version {
		return v2.Error(req.ID, -32600, "invalid jsonrpc version", nil)
	}
	switch req.Method {
	case v2.MethodAgentReport:
		var params v2.ReportParams
		if err := req.DecodeParams(&params); err != nil {
			return v2.Error(req.ID, -32602, "invalid report params", err.Error())
		}
		var events []v2.Event
		if err := admit(func() error {
			if err := ingestReportContext(ctx, uuid, params.Report, allowWait); err != nil {
				return err
			}
			events = agent_runtime.TakeV2Events(uuid, params.AckEventIDs, 8)
			return nil
		}); err != nil {
			return failure("failed to save report", err)
		}
		return v2.Success(req.ID, gin.H{
			"status": "success",
			"events": events,
		})
	case v2.MethodAgentBasicInfo:
		var params v2.BasicInfoParams
		if err := req.DecodeParams(&params); err != nil {
			return v2.Error(req.ID, -32602, "invalid basic info params", err.Error())
		}
		if params.Info == nil {
			params.Info = map[string]any{}
		}
		params.Info["uuid"] = uuid
		appendClientRegionFromGeoIP(params.Info)
		if err := admit(func() error { return clients.SaveClientInfoContext(ctx, params.Info) }); err != nil {
			return failure("failed to save basic info", err)
		}
		return v2.Success(req.ID, gin.H{"status": "success"})
	case v2.MethodAgentUnlock:
		var params v2.UnlockParams
		if err := req.DecodeParams(&params); err != nil {
			return v2.Error(req.ID, -32602, "invalid unlock params", err.Error())
		}
		if err := admit(func() error { return unlock.SaveContext(ctx, uuid, params) }); err != nil {
			return failure("failed to save unlock report", err)
		}
		return v2.Success(req.ID, gin.H{"status": "success"})
	case v2.MethodAgentPingResult:
		var params v2.PingResultParams
		if err := req.DecodeParams(&params); err != nil {
			return v2.Error(req.ID, -32602, "invalid ping result params", err.Error())
		}
		if err := admit(func() error {
			return metricstore.WritePingRecord(ctx, newPingRecord(uuid, params.TaskID, params.PingType, params.Role, params.Family, params.Value, params.ValueUS, time.Now().UTC()))
		}); err != nil {
			return failure("failed to save ping result", err)
		}
		return v2.Success(req.ID, gin.H{"status": "success"})
	case v2.MethodAgentTaskResult:
		var params v2.TaskResultParams
		if err := req.DecodeParams(&params); err != nil {
			return v2.Error(req.ID, -32602, "invalid task result params", err.Error())
		}
		finishedAt := params.FinishedAt
		if finishedAt.IsZero() {
			finishedAt = time.Now().UTC()
		}
		if err := admit(func() error {
			return tasks.SaveTaskResultContext(ctx, params.TaskID, uuid, params.Result, params.ExitCode, finishedAt)
		}); err != nil {
			return failure("failed to save task result", err)
		}
		return v2.Success(req.ID, gin.H{"status": "success"})
	case v2.MethodAgentPull:
		var params v2.PullParams
		if err := req.DecodeParams(&params); err != nil {
			return v2.Error(req.ID, -32602, "invalid pull params", err.Error())
		}
		var wait agent_runtime.V2EventWait
		if err := admit(func() error {
			if allowWait {
				refreshPostPresence(uuid)
			}
			agent_runtime.MarkV2Client(uuid)
			wait = agent_runtime.PrepareV2EventWait(uuid, params.AckEventIDs)
			return nil
		}); err != nil {
			return failure("failed to pull events", err)
		}
		timeout := 0 * time.Second
		if allowWait {
			timeout = 25 * time.Second
		}
		events := wait.Wait(ctx, timeout)
		if err := admit(func() error { return nil }); err != nil {
			return failure("failed to pull events", err)
		}
		return v2.Success(req.ID, gin.H{"events": events})
	case v2.MethodAgentFileResult:
		var params v2.FileResult
		if err := req.DecodeParams(&params); err != nil {
			return v2.Error(req.ID, -32602, "invalid file result params", err.Error())
		}
		params.UUID = uuid
		resolved := false
		if err := admit(func() error { resolved = filemanager.Resolve(params); return nil }); err != nil {
			return failure("failed to save file result", err)
		}
		if !resolved {
			return v2.Error(req.ID, -32004, "unknown or expired file operation", nil)
		}
		return v2.Success(req.ID, gin.H{"status": "success"})
	default:
		return v2.Error(req.ID, -32601, "method not found", req.Method)
	}
}

func UploadV2RPC(c *gin.Context) {
	uuid, ok := clientUUIDFromContext(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, v2.Error(nil, -32001, "invalid token", nil))
		return
	}
	operation := api.ClientOperation(c, uuid)
	bytesBody, err := readMaybeCompressedBody(c.Request, api.MaxAgentControlBody)
	if err != nil {
		c.JSON(http.StatusBadRequest, v2.Error(nil, -32700, "invalid compressed body", err.Error()))
		return
	}
	var req v2.RawRequest
	if err := json.Unmarshal(bytesBody, &req); err != nil {
		c.JSON(http.StatusBadRequest, v2.Error(nil, -32700, "parse error", err.Error()))
		return
	}
	// Only filesystem metadata may use the wider Agent bound. Everything else —
	// reports above all — keeps the 1 MiB control-body limit, so widening the
	// channel for directory listings does not widen it for decoded reports.
	if !agentMessageWithinControlLimit(req.Method, int64(len(bytesBody))) {
		c.JSON(http.StatusRequestEntityTooLarge, v2.Error(req.ID, -32600, "control message exceeds body limit", nil))
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	resp := handleV2RPCOperation(ctx, uuid, req, true, operation)
	if err := operation(ctx, func() error { return nil }); err != nil {
		resp = v2.Error(req.ID, -32001, "invalid token", nil)
	}
	status := http.StatusOK
	if resp.Error != nil {
		status = http.StatusBadRequest
		if resp.Error.Code == -32001 {
			status = http.StatusUnauthorized
		}
	}
	c.JSON(status, resp)
}

func WebSocketV2RPC(c *gin.Context) {
	uuid, ok := clientUUIDFromContext(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, v2.Error(nil, -32001, "invalid token", nil))
		return
	}
	operation := api.ClientOperation(c, uuid)
	if !api.IsWebSocketUpgrade(c) {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "Require WebSocket upgrade"})
		return
	}
	conn, err := api.UpgradeSafeConn(c, api.EnableWebSocketCompression)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "Failed to upgrade to WebSocket." + err.Error()})
		return
	}
	defer conn.Close()
	// The Agent's own channel: it carries filesystem metadata as well as reports,
	// so it uses the Agent bound rather than the browser control-body limit.
	conn.SetReadLimit(api.MaxAgentControlBody)

	setupCtx, setupCancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	var oldConn *connection.SafeConn
	err = operation(setupCtx, func() error {
		oldConn = agent_runtime.GetConnectedClient(uuid)
		agent_runtime.SetConnectedClients(uuid, conn)
		agent_runtime.MarkV2Client(uuid)
		return nil
	})
	setupCancel()
	if err != nil {
		return
	}
	if oldConn != nil {
		oldConn.Abort()
	}
	currentOperation := func(ctx context.Context, fn func() error) error {
		return operation(ctx, func() error {
			if agent_runtime.GetConnectedClient(uuid) != conn {
				return api.ErrClientCredentialRetired
			}
			return fn()
		})
	}
	// The agent only holds the rollout target in memory, so a node that restarted would otherwise resume
	// tracking the newest release and silently undo its part of a rollout; symmetrically, a node that was
	// offline when its pin was cleared has to be told the target is gone. The panel persists both facts per
	// node, so this is where a connecting (or reconnecting) node re-learns them. A failure is logged and the
	// connection continues: the next dispatch re-sends the target anyway, and refusing the connection would
	// turn a display problem into a monitoring outage.
	if err := resendPersistedAgentUpdateTargetOperation(c.Request.Context(), currentOperation, conn, uuid, clients.AgentUpdateState, clients.SetTargetVersionClearPending); err != nil {
		if errors.Is(err, api.ErrClientCredentialRetired) {
			return
		}
		logger.Errorf("client-api", "failed to reconcile the persisted agent update state for client %s: %v", uuid, err)
	}
	go notifierOnline(uuid, conn.ID)
	defer func() {
		agent_runtime.DeleteClientConditionally(uuid, conn)
		notifierOffline(uuid, conn.ID)
	}()
	if !pushQueuedV2Events(c.Request.Context(), currentOperation, conn, uuid) {
		return
	}

	for {
		conn.SetReadDeadline(time.Now().Add(readWait))
		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				logger.Errorf("client-api", "Client %s v2 connection error: %v", uuid, err)
			}
			return
		}
		message = bytes.TrimSpace(message)
		ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
		if err := currentOperation(ctx, func() error { return nil }); err != nil {
			cancel()
			return
		}
		var req v2.RawRequest
		if err := json.Unmarshal(message, &req); err != nil {
			cancel()
			conn.WriteJSON(v2.Error(nil, -32700, "parse error", err.Error()))
			continue
		}
		// The same rule as the POST path: only filesystem metadata may use the wider
		// Agent bound. The read limit above is per connection, so without this check
		// the socket would accept an 8 MiB report that POST refuses.
		if !agentMessageWithinControlLimit(req.Method, int64(len(message))) {
			cancel()
			conn.WriteJSON(v2.Error(req.ID, -32600, "control message exceeds body limit", nil))
			continue
		}
		resp := handleV2RPCOperation(ctx, uuid, req, false, currentOperation)
		err = currentOperation(ctx, func() error { return nil })
		cancel()
		if err != nil {
			return
		}
		if req.ID != nil {
			if err := conn.WriteJSON(resp); err != nil {
				logger.Errorf("client-api", "failed to write v2 rpc response: %v", err)
				return
			}
		}
	}
}

// agentUpdateTargetWriter is the part of a v2 connection the rollout re-send needs. Taking the interface
// rather than *connection.SafeConn keeps the behaviour testable without a live WebSocket.
type agentUpdateTargetWriter interface {
	WriteJSON(v any) error
}

// resendPersistedAgentUpdateTarget reconciles one connecting node with the rollout state the panel has
// persisted for it. Two states need a message:
//
//   - a pinned target, because a node that restarted mid-rollout would otherwise resume tracking the newest
//     release and silently undo its part of the rollout;
//   - a clear that the node never received, because it was offline when the operator removed the pin. The
//     agent keeps its target in memory only, so an empty target alone does not stop it: it needs to be told.
//
// The normal state — no pin and nothing pending — sends nothing: the agent's default is already "follow the
// newest release", so a clear event on every connect would be pure noise.
//
// A delivered clear is acknowledged by clearing the pending marker, so the debt is paid exactly once. A
// failed write leaves the marker in place and the next connect retries it. Lookup and write errors are
// returned for the caller to log; the connection itself survives them.
//
// resendPersistedAgentUpdateTarget 让一个刚连上的节点与其在面板侧持久化的 rollout 状态对齐。只有两种
// 状态需要发消息：
//
//   - 已钉的目标（节点重启后会跟随最新发布，从而静默回退 rollout）；
//   - 节点从未收到的清除（清除时节点离线；agent 只把目标存在内存里，仅把面板记录置空并不会让它停下）。
//
// 常态（无钉版本、无待下发清除）不发送任何消息：agent 默认就跟随最新发布，每次连接都发清除事件只是噪音。
//
// 清除真正送达后会把 pending 标记清掉，因此这笔债只结算一次；写入失败则保留标记，下次连接重试。查询与
// 写入错误都返回给调用方记录日志，连接本身不受影响。
func resendPersistedAgentUpdateTarget(
	conn agentUpdateTargetWriter,
	uuid string,
	lookup func(string) (string, bool, error),
	clearPending func(string, bool) error,
) error {
	return resendPersistedAgentUpdateTargetOperation(context.Background(), unguardedClientOperation, conn, uuid, lookup, clearPending)
}

func unguardedClientOperation(_ context.Context, fn func() error) error { return fn() }

func resendPersistedAgentUpdateTargetOperation(
	ctx context.Context, operation clientOperation, conn agentUpdateTargetWriter, uuid string,
	lookup func(string) (string, bool, error), clearPending func(string, bool) error,
) error {
	var stored string
	var pendingClear bool
	err := operation(ctx, func() error {
		var err error
		stored, pendingClear, err = lookup(uuid)
		return err
	})
	if err != nil {
		return fmt.Errorf("read the persisted rollout state: %w", err)
	}
	target := strings.TrimSpace(stored)
	if target == "" && !pendingClear {
		return nil
	}
	if err := conn.WriteJSON(v2.Request{
		JSONRPC: v2.Version,
		Method:  v2.MethodAgentUpdate,
		Params:  v2.UpdateParams{Target: target},
	}); err != nil {
		return fmt.Errorf("write the agent update request: %w", err)
	}
	if !pendingClear {
		return nil
	}
	// The node has now been told; the debt is paid. Failing to record that is
	// reported but harmless: the next connect would send the clear again.
	if err := operation(ctx, func() error { return clearPending(uuid, false) }); err != nil {
		return fmt.Errorf("clear the pending rollout reset: %w", err)
	}
	return nil
}

func pushQueuedV2Events(ctx context.Context, operation clientOperation, conn *connection.SafeConn, uuid string) bool {
	var events []v2.Event
	if err := operation(ctx, func() error {
		events = agent_runtime.TakeV2Events(uuid, nil, 0)
		return nil
	}); err != nil {
		return false
	}
	if len(events) == 0 {
		return true
	}
	ackIDs := make([]string, 0, len(events))
	ack := func() error {
		return operation(ctx, func() error { agent_runtime.AckV2Events(uuid, ackIDs); return nil })
	}
	for _, event := range events {
		payload := v2.Request{JSONRPC: v2.Version, Method: event.Method, Params: event.Params}
		if err := conn.WriteJSON(payload); err != nil {
			_ = ack()
			logger.Errorf("client-api", "failed to push queued v2 event %s to client %s: %v", event.ID, uuid, err)
			return false
		}
		ackIDs = append(ackIDs, event.ID)
	}
	return ack() == nil
}

func clientUUIDFromContext(c *gin.Context) (string, bool) {
	if v, ok := c.Get("client_uuid"); ok {
		if uuid, ok := v.(string); ok && uuid != "" {
			return uuid, true
		}
	}
	token := c.Query("token")
	if token == "" {
		return "", false
	}
	uuid, err := clients.GetClientUUIDByToken(token)
	return uuid, err == nil && uuid != ""
}

func notifierOnline(uuid string, connID int64) {
	go func() {
		defer func() { _ = recover() }()
		notifier.OnlineNotification(uuid, connID)
	}()
}

func notifierOffline(uuid string, connID int64) {
	defer func() { _ = recover() }()
	notifier.OfflineNotification(uuid, connID)
}
