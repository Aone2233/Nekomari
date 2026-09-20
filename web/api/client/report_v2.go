package client

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	logger "github.com/Aone2233/nekomari/utils/log"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Aone2233/nekomari/database/clients"
	"github.com/Aone2233/nekomari/database/tasks"
	"github.com/Aone2233/nekomari/database/unlock"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
	"github.com/Aone2233/nekomari/utils/notifier"
	agent_runtime "github.com/Aone2233/nekomari/web/agent"
	"github.com/Aone2233/nekomari/web/api"
	"github.com/Aone2233/nekomari/web/connection"
	"github.com/Aone2233/nekomari/web/filemanager"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func readMaybeCompressedBody(r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	if strings.EqualFold(r.Header.Get("Content-Encoding"), "gzip") {
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			return nil, err
		}
		defer zr.Close()
		return readLimitedReport(zr)
	}
	return readLimitedReport(r.Body)
}

func readLimitedReport(r io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, api.MaxControlBody+1))
	if int64(len(body)) > api.MaxControlBody {
		return nil, fmt.Errorf("report exceeds body limit")
	}
	return body, err
}

func bindV2Params[T any](raw any, target *T) error {
	b, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, target)
}

func handleV2RPC(uuid string, req v2.Request, allowWait bool) v2.Response {
	if req.JSONRPC != v2.Version {
		return v2.Error(req.ID, -32600, "invalid jsonrpc version", nil)
	}
	switch req.Method {
	case v2.MethodAgentReport:
		var params v2.ReportParams
		if err := bindV2Params(req.Params, &params); err != nil {
			return v2.Error(req.ID, -32602, "invalid report params", err.Error())
		}
		if err := ingestReport(uuid, params.Report, true); err != nil {
			return v2.Error(req.ID, -32000, "failed to save report", err.Error())
		}
		return v2.Success(req.ID, gin.H{
			"status": "success",
			"events": agent_runtime.TakeV2Events(uuid, params.AckEventIDs, 8),
		})
	case v2.MethodAgentBasicInfo:
		var params v2.BasicInfoParams
		if err := bindV2Params(req.Params, &params); err != nil {
			return v2.Error(req.ID, -32602, "invalid basic info params", err.Error())
		}
		if err := ingestBasicInfo(uuid, params.Info, ""); err != nil {
			return v2.Error(req.ID, -32000, "failed to save basic info", err.Error())
		}
		return v2.Success(req.ID, gin.H{"status": "success"})
	case v2.MethodAgentUnlock:
		var params v2.UnlockParams
		if err := bindV2Params(req.Params, &params); err != nil {
			return v2.Error(req.ID, -32602, "invalid unlock params", err.Error())
		}
		if err := unlock.Save(uuid, params); err != nil {
			return v2.Error(req.ID, -32000, "failed to save unlock report", err.Error())
		}
		return v2.Success(req.ID, gin.H{"status": "success"})
	case v2.MethodAgentPingResult:
		var params v2.PingResultParams
		if err := bindV2Params(req.Params, &params); err != nil {
			return v2.Error(req.ID, -32602, "invalid ping result params", err.Error())
		}
		if err := ingestPingResult(uuid, params.TaskID, params.PingType, params.Role, params.Value); err != nil {
			return v2.Error(req.ID, -32000, "failed to save ping result", err.Error())
		}
		return v2.Success(req.ID, gin.H{"status": "success"})
	case v2.MethodAgentTaskResult:
		var params v2.TaskResultParams
		if err := bindV2Params(req.Params, &params); err != nil {
			return v2.Error(req.ID, -32602, "invalid task result params", err.Error())
		}
		finishedAt := params.FinishedAt
		if finishedAt.IsZero() {
			finishedAt = time.Now().UTC()
		}
		if err := tasks.SaveTaskResult(params.TaskID, uuid, params.Result, params.ExitCode, finishedAt); err != nil {
			return v2.Error(req.ID, -32000, "failed to save task result", err.Error())
		}
		return v2.Success(req.ID, gin.H{"status": "success"})
	case v2.MethodAgentPull:
		var params v2.PullParams
		if err := bindV2Params(req.Params, &params); err != nil {
			return v2.Error(req.ID, -32602, "invalid pull params", err.Error())
		}
		refreshPostPresence(uuid)
		agent_runtime.MarkV2Client(uuid)
		timeout := 0 * time.Second
		if allowWait {
			timeout = 25 * time.Second
		}
		return v2.Success(req.ID, gin.H{
			"events": agent_runtime.WaitV2Events(uuid, params.AckEventIDs, timeout),
		})
	case v2.MethodAgentFileResult:
		var params v2.FileResult
		if err := bindV2Params(req.Params, &params); err != nil {
			return v2.Error(req.ID, -32602, "invalid file result params", err.Error())
		}
		params.UUID = uuid
		if !filemanager.Resolve(params) {
			return v2.Error(req.ID, -32004, "unknown or expired file operation", nil)
		}
		return v2.Success(req.ID, gin.H{"status": "success"})
	default:
		return v2.Error(req.ID, -32601, "method not found", req.Method)
	}
}

func UploadV2RPC(c *gin.Context) {
	bytesBody, err := readMaybeCompressedBody(c.Request)
	if err != nil {
		c.JSON(http.StatusBadRequest, v2.Error(nil, -32700, "invalid compressed body", err.Error()))
		return
	}
	var req v2.Request
	if err := json.Unmarshal(bytesBody, &req); err != nil {
		c.JSON(http.StatusBadRequest, v2.Error(nil, -32700, "parse error", err.Error()))
		return
	}
	uuid, ok := clientUUIDFromContext(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, v2.Error(req.ID, -32001, "invalid token", nil))
		return
	}
	resp := handleV2RPC(uuid, req, true)
	status := http.StatusOK
	if resp.Error != nil {
		status = http.StatusBadRequest
	}
	c.JSON(status, resp)
}

func WebSocketV2RPC(c *gin.Context) {
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
	conn.SetReadLimit(api.MaxControlBody)

	uuid, ok := clientUUIDFromContext(c)
	if !ok {
		conn.WriteJSON(v2.Error(nil, -32001, "invalid token", nil))
		return
	}
	if oldConn, exists := agent_runtime.GetConnectedClients()[uuid]; exists {
		go oldConn.Close()
	}
	agent_runtime.SetConnectedClients(uuid, conn)
	agent_runtime.MarkV2Client(uuid)
	go notifierOnline(uuid, conn.ID)
	defer func() {
		agent_runtime.DeleteClientConditionally(uuid, conn)
		notifierOffline(uuid, conn.ID)
	}()
	if !pushQueuedV2Events(conn, uuid) {
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
		var req v2.Request
		if err := json.Unmarshal(message, &req); err != nil {
			conn.WriteJSON(v2.Error(nil, -32700, "parse error", err.Error()))
			continue
		}
		resp := handleV2RPC(uuid, req, false)
		if req.ID != nil {
			if err := conn.WriteJSON(resp); err != nil {
				logger.Errorf("client-api", "failed to write v2 rpc response: %v", err)
				return
			}
		}
	}
}

func pushQueuedV2Events(conn *connection.SafeConn, uuid string) bool {
	events := agent_runtime.TakeV2Events(uuid, nil, 0)
	if len(events) == 0 {
		return true
	}
	ackIDs := make([]string, 0, len(events))
	for _, event := range events {
		payload := v2.Request{JSONRPC: v2.Version, Method: event.Method, Params: event.Params}
		if err := conn.WriteJSON(payload); err != nil {
			agent_runtime.AckV2Events(uuid, ackIDs)
			logger.Errorf("client-api", "failed to push queued v2 event %s to client %s: %v", event.ID, uuid, err)
			return false
		}
		ackIDs = append(ackIDs, event.ID)
	}
	agent_runtime.AckV2Events(uuid, ackIDs)
	return true
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
