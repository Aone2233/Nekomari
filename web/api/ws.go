package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/Aone2233/nekomari/database/clients"
	"github.com/Aone2233/nekomari/internal/config"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
	agent_runtime "github.com/Aone2233/nekomari/web/agent"
	"github.com/gin-gonic/gin"
)

var statusConnections = make(chan struct{}, 128)

func GetClients(c *gin.Context) {
	select {
	case statusConnections <- struct{}{}:
		defer func() { <-statusConnections }()
	default:
		c.AbortWithStatus(http.StatusTooManyRequests)
		return
	}
	// 升级到ws
	if !IsWebSocketUpgrade(c) {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "Require WebSocket upgrade"})
		return
	}
	// Upgrade the HTTP connection to a WebSocket connection
	conn, err := UpgradeSafeConn(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "Failed to upgrade to WebSocket." + err.Error()})
		return
	}
	defer conn.Close()
	conn.SetReadLimit(4096)

	// 请求
	for {
		_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		var resp struct {
			Online []string             `json:"online"` // 已建立连接的客户端uuid列表
			Data   map[string]v2.Report `json:"data"`   // 最后上报的数据
		}

		resp.Online = []string{}
		resp.Data = map[string]v2.Report{}

		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		message := string(data)
		isLogin := IdentifyPrincipal(c).HasRole(RoleAdmin)
		private, configErr := config.GetAs[bool](config.PrivateSiteKey, false)
		if configErr != nil || (!isLogin && private && !hasTempAccess(c)) {
			return
		}
		hiddenMap, err := clients.HiddenClients()
		if err != nil {
			return
		}
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))

		uuID := ""
		if message != "get" { // 非请求全部内容
			if strings.HasPrefix(message, "get ") {
				uuID = strings.TrimSpace(strings.TrimPrefix(message, "get "))
			} else {
				conn.WriteJSON(gin.H{"status": "error", "error": "Invalid message"})
				continue
			}
		}

		// 在线客户端uuid列表（WebSocket 与非 WebSocket）
		for _, key := range agent_runtime.GetAllOnlineUUIDs() {
			if !isLogin && hiddenMap[key] {
				continue
			}
			if uuID != "" && key != uuID {
				continue
			}
			resp.Online = append(resp.Online, key)
		}

		//过往节点数据信息
		for key, report := range agent_runtime.GetLatestReport() {
			if !isLogin && hiddenMap[key] {
				continue
			}
			if uuID != "" && key != uuID {
				continue
			}

			report.UUID = "" // 不暴露 uuid
			if report.CPU.Usage == 0 {
				report.CPU.Usage = 0.01
			}
			resp.Data[key] = *report
		}

		err = conn.WriteJSON(gin.H{"status": "success", "data": resp})
		if err != nil {
			return
		}
	}
}
