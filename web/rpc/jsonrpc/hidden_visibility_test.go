package jsonrpc

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/dbcache"
	"github.com/Aone2233/nekomari/pkg/rpc"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
	agent_runtime "github.com/Aone2233/nekomari/web/agent"
)

// 这一组用例守住 P1-9 的整条链：匿名调用者不得从任何 guest 可达的接口里
// 看出「某个 uuid 是隐藏节点」，也不得拿到隐藏节点的 uuid。
//
// 链路由三处叠成（public:getPublicPingTasks 先给出 uuid，
// common:getNodeRecentStatus / common:getRecords 再逐个确认），所以三处都要钉。

func guestContext() context.Context {
	return rpc.NewContextWithMeta(context.Background(), &rpc.ContextMeta{Principal: rpc.NewAnonymousPrincipal()})
}

func adminContext() context.Context {
	return rpc.NewContextWithMeta(context.Background(), &rpc.ContextMeta{Principal: rpc.PrincipalFromRole(rpc.RoleAdmin)})
}

// seedVisibilityClients 建一个可见节点和一个隐藏节点。uuid 由调用方给出，
// 因为测试二进制共用一个内存库，重复的 uuid 会互相干扰。
func seedVisibilityClients(t *testing.T, visibleUUID, hiddenUUID string) {
	t.Helper()
	db := dbcore.GetDBInstance()
	for _, client := range []models.Client{
		{UUID: visibleUUID, Name: visibleUUID, Token: "test-only-token-" + visibleUUID},
		{UUID: hiddenUUID, Name: hiddenUUID, Token: "test-only-token-" + hiddenUUID, Hidden: true},
	} {
		if err := db.Create(&client).Error; err != nil {
			t.Fatalf("create client %s: %v", client.UUID, err)
		}
	}
	// 可见性缓存按 dbcache 的 revision 失效，测试里没有注册 revision watcher。
	dbcache.InvalidateAll()
}

func mustMarshalJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal %#v: %v", value, err)
	}
	return string(encoded)
}

// TestVisibleClientUUIDsFiltersHiddenAndUnknown 是这条链的最小单元：一个客户端的
// uuid 只有在「存在且（不隐藏或调用者是管理员）」时才允许出现在返回给调用者的列表里。
func TestVisibleClientUUIDsFiltersHiddenAndUnknown(t *testing.T) {
	hidden := map[string]bool{
		"visible-node": false,
		"hidden-node":  true,
	}
	stored := []string{"visible-node", "hidden-node", "deleted-node", ""}

	guest := visibleClientUUIDs(stored, hidden, false)
	if len(guest) != 1 || guest[0] != "visible-node" {
		t.Fatalf("guest client list = %v, want only the visible, existing node", guest)
	}

	admin := visibleClientUUIDs(stored, hidden, true)
	if len(admin) != len(stored) {
		t.Fatalf("admin client list = %v, want the stored list verbatim", admin)
	}
	for i := range stored {
		if admin[i] != stored[i] {
			t.Fatalf("admin client list = %v, want %v", admin, stored)
		}
	}
}

// TestHiddenNodeRecentStatusIsIndistinguishableFromUnknown 是链路的第二环。
// 隐藏节点在内存里【确实有】上报，所以这条用例同时证明了两件事：回答与不存在的
// uuid 逐字相同，并且没有把那份上报内容一起返回。
func TestHiddenNodeRecentStatusIsIndistinguishableFromUnknown(t *testing.T) {
	const visibleUUID = "vis-status-node"
	const hiddenUUID = "hid-status-node"
	seedVisibilityClients(t, visibleUUID, hiddenUUID)

	agent_runtime.RecordReport(v2.Report{
		UUID:      hiddenUUID,
		UpdatedAt: time.Now().UTC(),
		CPU:       v2.CPUReport{Usage: 42},
	})
	t.Cleanup(func() { agent_runtime.DeleteLatestReport(hiddenUUID) })

	request := func(uuid string) *rpc.JsonRpcRequest {
		return &rpc.JsonRpcRequest{
			Version: rpc.RPC_VERSION,
			Method:  "common:getNodeRecentStatus",
			Params:  map[string]any{"uuid": uuid},
		}
	}

	guest := guestContext()
	hiddenResp, jerr := getNodeRecentStatus(guest, request(hiddenUUID))
	if jerr != nil {
		t.Fatalf("guest + hidden uuid returned an error: %+v", jerr)
	}
	unknownResp, jerr := getNodeRecentStatus(guest, request("no-such-node-at-all"))
	if jerr != nil {
		t.Fatalf("guest + unknown uuid returned an error: %+v", jerr)
	}
	if hidden, unknown := mustMarshalJSON(t, hiddenResp), mustMarshalJSON(t, unknownResp); hidden != unknown {
		t.Fatalf("hidden and unknown must answer identically:\n hidden  = %s\n unknown = %s", hidden, unknown)
	}
	if body := mustMarshalJSON(t, hiddenResp); strings.Contains(body, "42") {
		t.Fatalf("the hidden node's report leaked through the anonymous path: %s", body)
	}

	// 反向对照：管理员仍然看得到，否则「一律返回空」也能让上面的断言通过。
	adminResp, jerr := getNodeRecentStatus(adminContext(), request(hiddenUUID))
	if jerr != nil {
		t.Fatalf("admin + hidden uuid returned an error: %+v", jerr)
	}
	if body := mustMarshalJSON(t, adminResp); !strings.Contains(body, "42") {
		t.Fatalf("admin must still read the hidden node's report, got %s", body)
	}
}

// TestHiddenNodeRecordsAreIndistinguishableFromUnknown 是链路的第三环。
//
// 这条用例只断言「两个回答相同」，不断言它们具体是什么：本测试二进制里 metric
// store 没有启用，所以两条路径都会得到同一个 "metric store not enabled" 错误；
// 换成启用 store 的环境，两条路径都会得到同一个空结果。两种情况都满足断言，而
// 改动前的代码对 hidden 提前返回 InvalidParams("UUID not found")、对不存在的
// uuid 走另一条路 —— 两者不同，用例会失败。
func TestHiddenNodeRecordsAreIndistinguishableFromUnknown(t *testing.T) {
	const visibleUUID = "vis-records-node"
	const hiddenUUID = "hid-records-node"
	seedVisibilityClients(t, visibleUUID, hiddenUUID)

	request := func(uuid string) *rpc.JsonRpcRequest {
		return &rpc.JsonRpcRequest{
			Version: rpc.RPC_VERSION,
			Method:  "common:getRecords",
			Params:  map[string]any{"type": "load", "uuid": uuid, "hours": 1},
		}
	}

	guest := guestContext()
	hiddenValue, hiddenErr := getRecords(guest, request(hiddenUUID))
	unknownValue, unknownErr := getRecords(guest, request("no-such-node-at-all"))

	if (hiddenErr == nil) != (unknownErr == nil) {
		t.Fatalf("hidden returned err=%+v, unknown returned err=%+v; the difference is the oracle", hiddenErr, unknownErr)
	}
	if hiddenErr != nil {
		if hiddenErr.Code != unknownErr.Code || hiddenErr.Message != unknownErr.Message {
			t.Fatalf("hidden error %+v differs from unknown error %+v", hiddenErr, unknownErr)
		}
		return
	}
	if hidden, unknown := mustMarshalJSON(t, hiddenValue), mustMarshalJSON(t, unknownValue); hidden != unknown {
		t.Fatalf("hidden and unknown must answer identically:\n hidden  = %s\n unknown = %s", hidden, unknown)
	}
}

// TestPublicPingTasksHideHiddenClients 是链路的第一环，也是唯一直接把 uuid
// 交出去的那一处。
func TestPublicPingTasksHideHiddenClients(t *testing.T) {
	const visibleUUID = "vis-task-node"
	const hiddenUUID = "hid-task-node"
	seedVisibilityClients(t, visibleUUID, hiddenUUID)

	db := dbcore.GetDBInstance()
	task := models.PingTask{
		Name:     "visibility-fixture-task",
		Clients:  models.StringArray{visibleUUID, hiddenUUID},
		Type:     "icmp",
		Target:   "198.51.100.7",
		Interval: 60,
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("create ping task: %v", err)
	}

	find := func(value any) []string {
		t.Helper()
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal tasks: %v", err)
		}
		var tasks []struct {
			Id      uint     `json:"id"`
			Clients []string `json:"clients"`
		}
		if err := json.Unmarshal(encoded, &tasks); err != nil {
			t.Fatalf("unmarshal tasks: %v", err)
		}
		for _, candidate := range tasks {
			if candidate.Id == task.Id {
				return candidate.Clients
			}
		}
		t.Fatalf("task %d missing from the public payload: %s", task.Id, encoded)
		return nil
	}

	guestValue, jerr := publicGetPublicPingTasks(guestContext(), &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION,
		Method:  "public:getPublicPingTasks",
	})
	if jerr != nil {
		t.Fatalf("guest call failed: %+v", jerr)
	}
	if got := find(guestValue); len(got) != 1 || got[0] != visibleUUID {
		t.Fatalf("guest task clients = %v, want only %q", got, visibleUUID)
	}

	adminValue, jerr := publicGetPublicPingTasks(adminContext(), &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION,
		Method:  "public:getPublicPingTasks",
	})
	if jerr != nil {
		t.Fatalf("admin call failed: %+v", jerr)
	}
	if got := find(adminValue); len(got) != 2 {
		t.Fatalf("admin task clients = %v, want both nodes", got)
	}
}

// guestReachableAllowlist 是当前【经过审阅】的 guest 可达方法名单。
//
// 它存在的理由是 Allow("common:*", RoleGuest)：一个不带命名空间的新 RPC 会
// 自动对匿名调用者开放，而这件事没有任何测试会注意到。新增方法时这个用例会失败，
// 迫使作者在名单里显式加一行 —— 与 dispatch_test.go 里私有站点白名单的做法一致。
//
// 往名单里加方法之前先问：匿名调用者拿得到它返回的东西吗？
var guestReachableAllowlist = map[string]bool{
	"common:getMe":                  true,
	"common:getNodeRecentStatus":    true,
	"common:getPublicInfo":          true,
	"common:getRecords":             true,
	"common:getVersion":             true,
	"public:getClientRecentRecords": true,
	"public:getMe":                  true,
	"public:getNodesInformation":    true,
	"public:getPingMetricStats":     true,
	"public:getPingRecords":         true,
	"public:getPublicPingTasks":     true,
	"public:getPublicSettings":      true,
	"public:getRecordsByUUID":       true,
	"public:getSlaReport":           true,
	"public:getVersion":             true,
	"public:listMetricDefinitions":  true,
	"public:queryMetrics":           true,
	"rpc.help":                      true,
	"rpc.methods":                   true,
	"rpc.ping":                      true,
	"rpc.version":                   true,

	// 匿名可见但经过审阅的另外三个 —— 本用例第一次运行时把它们暴露出来的：
	//
	//   - common:getNodes 是主题拿到节点列表（含地址）的唯一路径，公开面板需要它；
	//     非管理员分支会剔除 hidden 节点并清空 Token/Remark/Version，请求隐藏 uuid 时
	//     与请求不存在的 uuid 返回同一条 "Node not found"。
	//   - common:getNodesLatestStatus 同样剔除 hidden；请求隐藏 uuid 时走的也是
	//     同一条 "Node not found"。
	//   - public:recordVisitorEvent 是登录页渲染路径上的匿名写入（访问审计），
	//     私有站点白名单也依赖它（见 dispatch_test.go）。
	"common:getNodes":             true,
	"common:getNodesLatestStatus": true,
	"public:recordVisitorEvent":   true,
}

func TestGuestReachableMethodsAreAnExplicitList(t *testing.T) {
	reachable := map[string]bool{}
	for _, method := range rpc.ListMethods() {
		if rpc.RequiredRole(method) == rpc.RoleGuest {
			reachable[method] = true
		}
	}

	var unexpected []string
	for method := range reachable {
		if !guestReachableAllowlist[method] {
			unexpected = append(unexpected, method)
		}
	}
	var missing []string
	for method := range guestReachableAllowlist {
		if !reachable[method] {
			missing = append(missing, method)
		}
	}

	if len(unexpected) > 0 {
		t.Errorf("these methods became reachable by anonymous callers without being reviewed: %v\n"+
			"If that is intended, add them to guestReachableAllowlist with a reason.", unexpected)
	}
	if len(missing) > 0 {
		t.Errorf("guestReachableAllowlist names methods that are no longer guest-reachable: %v\n"+
			"Remove them so the list keeps meaning something.", missing)
	}
}
