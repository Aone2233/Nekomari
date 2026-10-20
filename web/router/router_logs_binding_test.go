package router

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Aone2233/nekomari/cmd/flags"
	"github.com/Aone2233/nekomari/database/accounts"
	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/web/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// /api/admin/logs 的 HTTP 绑定必须把 msg_type 转发给 admin:getLogs。
//
// admin:getLogs 早就支持按 msg_type 精确过滤（admin.system.go 的 BindParams 里有 MsgType），
// 但路由表里的 WithQuery 只写了 limit/page。漏掉它时接口仍然 200、日志照常返回，只是筛选
// 被静默忽略 —— 前端「按类型筛选」看起来没生效，而服务端、网络面板、日志里都没有异常可看。
// 所以这里断言的是行为而不是路由字符串：带 msg_type 的请求必须真的少掉不匹配的行。
//
// WithQuery 的行为（web/rpc/jsonrpc/bridge.go）：只把**非空**的查询值合并进参数对象，
// 同名 body 字段会被查询参数覆盖；未列出的查询参数被忽略；值经 gin 的 c.Query 做 URL 解码。
// 下面几条子用例分别钉住「过滤生效」「limit/page 没被破坏」「未知/空值/URL 编码不改变语义」。
func TestAdminLogsBindingForwardsMessageType(t *testing.T) {
	// 内存库 + 临时工作目录：即使单独跑这一个用例（dbcore 尚未初始化），也不会在仓库里
	// 写下 data/komari.db。已经跑过的用例可能把 dbcore 初始化成内存库，GetDBInstance 复用它。
	flags.DatabaseType = flags.DatabaseTypeSQLite
	flags.DatabaseFile = "file:router-logs-binding?mode=memory&cache=shared"
	t.Chdir(t.TempDir())

	db := dbcore.GetDBInstance()
	user, err := accounts.CreateAccount("logsbinding"+strings.ReplaceAll(uuid.NewString(), "-", ""), "test-only-password")
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	session, err := accounts.CreateSession(user.UUID, 3600, "test", "127.0.0.1", "password")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	// 四个不同的 msg_type：两个 warn（够测分页）、一个 info（负例）、一个带空格（URL 编码）。
	seed := []models.Log{
		{UUID: user.UUID, Message: "router-logs-warn-a", MsgType: "warn"},
		{UUID: user.UUID, Message: "router-logs-warn-b", MsgType: "warn"},
		{UUID: user.UUID, Message: "router-logs-info", MsgType: "info"},
		{UUID: user.UUID, Message: "router-logs-spaced", MsgType: "warn level"},
	}
	for index := range seed {
		if err := db.Create(&seed[index]).Error; err != nil {
			t.Fatalf("seed log %d: %v", index, err)
		}
	}
	seededMessages := map[string]bool{
		"router-logs-warn-a": true,
		"router-logs-warn-b": true,
		"router-logs-info":   true,
		"router-logs-spaced": true,
	}

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(api.ControlBodyLimit(), api.IdentityMiddleware(), api.PrivateSiteMiddleware())
	Register(engine)
	server := httptest.NewServer(engine)
	t.Cleanup(server.Close)

	// fetch 以管理员会话请求 /api/admin/logs 并返回 data.logs / data.total。
	fetch := func(t *testing.T, query string) ([]map[string]any, int64) {
		t.Helper()
		request, err := http.NewRequest(http.MethodGet, server.URL+"/api/admin/logs"+query, nil)
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		request.AddCookie(&http.Cookie{Name: "session_token", Value: session})
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatalf("GET /api/admin/logs%s: %v", query, err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(response.Body)
			t.Fatalf("GET /api/admin/logs%s = %d: %s", query, response.StatusCode, body)
		}
		var envelope struct {
			Status string `json:"status"`
			Data   struct {
				Logs  []map[string]any `json:"logs"`
				Total int64            `json:"total"`
			} `json:"data"`
		}
		if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if envelope.Status != "success" {
			t.Fatalf("status = %q, want success", envelope.Status)
		}
		return envelope.Data.Logs, envelope.Data.Total
	}

	messages := func(rows []map[string]any) []string {
		out := make([]string, 0, len(rows))
		for _, row := range rows {
			message, _ := row["message"].(string)
			out = append(out, message)
		}
		return out
	}
	contains := func(values []string, want string) bool {
		for _, value := range values {
			if value == want {
				return true
			}
		}
		return false
	}

	t.Run("msg_type filters the returned rows", func(t *testing.T) {
		rows, total := fetch(t, "?limit=10&page=1&msg_type=warn")
		if total < 2 {
			t.Fatalf("total = %d, want at least the two seeded warn logs: the filter was probably dropped", total)
		}
		for _, row := range rows {
			if msgType, _ := row["msg_type"].(string); msgType != "warn" {
				t.Errorf("a row with msg_type %q was returned for msg_type=warn: %v", msgType, row)
			}
		}
		got := messages(rows)
		for _, want := range []string{"router-logs-warn-a", "router-logs-warn-b"} {
			if !contains(got, want) {
				t.Errorf("the seeded warn log %q is missing from %v", want, got)
			}
		}
		if contains(got, "router-logs-info") {
			t.Errorf("an info log survived a msg_type=warn request: %v", got)
		}
	})

	t.Run("limit and page still apply alongside msg_type", func(t *testing.T) {
		firstPage, total := fetch(t, "?limit=1&page=1&msg_type=warn")
		secondPage, secondTotal := fetch(t, "?limit=1&page=2&msg_type=warn")
		if total < 2 || secondTotal != total {
			t.Fatalf("total = %d and %d, want the same value >= 2", total, secondTotal)
		}
		if len(firstPage) != 1 || len(secondPage) != 1 {
			t.Fatalf("limit=1 returned %d and %d rows", len(firstPage), len(secondPage))
		}
		firstMessage, _ := firstPage[0]["message"].(string)
		secondMessage, _ := secondPage[0]["message"].(string)
		if firstMessage == secondMessage {
			t.Errorf("page 1 and page 2 both returned %q: page was ignored", firstMessage)
		}
		for _, message := range []string{firstMessage, secondMessage} {
			if !seededMessages[message] || !strings.HasPrefix(message, "router-logs-warn-") {
				t.Errorf("page returned %q, which is not one of the seeded warn logs", message)
			}
		}
	})

	t.Run("unknown, empty and encoded parameters keep their meaning", func(t *testing.T) {
		// 未列出的查询参数被 WithQuery 忽略，不能改变过滤结果。
		rows, _ := fetch(t, "?limit=10&page=1&msg_type=warn&not_a_parameter=1")
		for _, row := range rows {
			if msgType, _ := row["msg_type"].(string); msgType != "warn" {
				t.Errorf("an unknown query parameter changed the filter: row msg_type %q", msgType)
			}
		}

		// 空值不转发（WithQuery 只合并非空值）；即使转发，handler 也会 TrimSpace 成空串并
		// 当作「不过滤」。两种实现下结果都必须是不过滤，而不是把一切筛掉。
		unfiltered, _ := fetch(t, "?limit=10&page=1&msg_type=")
		if !contains(messages(unfiltered), "router-logs-info") {
			t.Error("msg_type= (empty) filtered the result down; an empty filter must mean no filter")
		}

		// URL 编码必须解码：%20 要变成空格，否则带空格的 msg_type 永远筛不到。
		spaced, _ := fetch(t, "?limit=10&page=1&msg_type=warn%20level")
		spacedMessages := messages(spaced)
		if !contains(spacedMessages, "router-logs-spaced") {
			t.Errorf("msg_type=warn%%20level did not return the spaced log: %v", spacedMessages)
		}
		for _, row := range spaced {
			if msgType, _ := row["msg_type"].(string); msgType != "warn level" {
				t.Errorf("a row with msg_type %q was returned for the encoded filter", msgType)
			}
		}
	})
}
