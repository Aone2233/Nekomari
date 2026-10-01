package ipinfo

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/unlock"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
	"github.com/Aone2233/nekomari/web/api"
	"github.com/gin-gonic/gin"
)

// P1-5：/api/public/ip-info/v1/lookup?uuid= 是公开路由，但 uuid 指向的节点数据
// （解锁快照里的 egress_ip）只能按「存在 && 非 hidden（或管理员）」返回。规则本身与
// 数据层实现由 database/unlock 的用例在真实数据库上覆盖（LoadVisible/
// ClientHistoryReadable）；这里钉住 HTTP 契约：
//
//	(a) 隐藏节点 + 匿名 → 明确错误，响应里没有任何 IP；
//	(b) 不存在的 uuid → 与 (a) 完全相同的错误（否则就是隐藏节点存在性的判定器）；
//	(c) 非隐藏节点 → 行为不变，解锁快照照常返回；
//	(d) 管理员 + 隐藏节点 → 仍可读（与 public:* 的约定一致）；
//	(e) uuid 为空 → 仍然 200 且不返回任何节点数据（第三方主题的按地址查询就是这样调的）；
//	(f) 读取失败（数据库抖动）→ 仍然 200 且不带节点数据，不让附加信息拖垮整个接口。
//
// 为什么用可注入的解锁读取而不是真实数据库：本包既有测试刻意不建库
// （见 unlock_test.go 的说明），一旦这里建库，那些按 test-uuid 查询的用例会全部变成 404。
// 真实数据层规则在 database/unlock 里用真实库覆盖，两处合起来是完整的。
func newAccessHandler(t *testing.T, hidden map[string]bool, reports map[string]*UnlockData) *Handler {
	t.Helper()
	handler, _, _ := newTestHandler(t)
	handler.loadUnlock = func(uuid string, loggedIn bool) (*UnlockData, error) {
		if !unlock.ClientHistoryReadable(hidden, loggedIn, uuid) {
			return nil, unlock.ErrNotVisible
		}
		return reports[uuid], nil
	}
	return handler
}

func unlockPayload(egress string) *UnlockData {
	return &UnlockData{
		EgressIP:     egress,
		EgressRegion: "JP",
		ProbedAt:     "2026-10-01T12:00:00Z",
		Results:      []v2.UnlockItem{{ID: "netflix", Name: "Netflix", Kind: "media", Status: "ok", Basis: "probe"}},
	}
}

// TestLookupHiddenNodeIsNotReadableForGuests 是 P1-5 的直接回归：
// 修复前匿名的 /lookup?uuid=<hidden> 会返回节点探针上报的 egress_ip。
func TestLookupHiddenNodeIsNotReadableForGuests(t *testing.T) {
	const leakedEgress = "203.0.113.9"
	handler := newAccessHandler(t,
		map[string]bool{"hidden-node": true},
		map[string]*UnlockData{"hidden-node": unlockPayload(leakedEgress)},
	)
	router := newTestRouter(handler)

	status, message, body := lookupRaw(t, router, "hidden-node")
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want %d: %s", status, http.StatusNotFound, body)
	}
	if message != "unknown node: hidden-node" {
		t.Fatalf("message = %q, want the shared not-found message", message)
	}
	if strings.Contains(body, "egress_ip") || strings.Contains(body, leakedEgress) {
		t.Fatalf("the error response leaks the hidden node's egress address: %s", body)
	}
	if strings.Contains(body, "1.2.3.4") {
		t.Fatalf("the error response still carries the queried address: %s", body)
	}
}

// TestLookupUnknownNodeIsAnExplicitError：不存在的 uuid 必须有明确错误，而不是 200。
func TestLookupUnknownNodeIsAnExplicitError(t *testing.T) {
	handler := newAccessHandler(t,
		map[string]bool{"visible-node": false},
		map[string]*UnlockData{"visible-node": unlockPayload("203.0.113.9")},
	)
	router := newTestRouter(handler)

	status, message, body := lookupRaw(t, router, "never-existed")
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want %d: %s", status, http.StatusNotFound, body)
	}
	if message != "unknown node: never-existed" {
		t.Fatalf("message = %q, want it to name the requested node", message)
	}
	if strings.Contains(body, "1.2.3.4") || strings.Contains(body, "egress_ip") {
		t.Fatalf("the error response carries data: %s", body)
	}
}

// TestHiddenAndUnknownNodesAnswerTheSameWay：隐藏节点与不存在的 uuid 必须给出同一个
// 形状的响应 —— 同样的状态码、同样的消息模板（只有回显的 uuid 不同）、都不带任何数据。
// 这是这个接口不泄露「哪些 uuid 是隐藏节点」的全部依据。
func TestHiddenAndUnknownNodesAnswerTheSameWay(t *testing.T) {
	handler := newAccessHandler(t,
		map[string]bool{"hidden-node": true},
		map[string]*UnlockData{"hidden-node": unlockPayload("203.0.113.9")},
	)
	router := newTestRouter(handler)

	hiddenStatus, hiddenMessage, _ := lookupRaw(t, router, "hidden-node")
	unknownStatus, unknownMessage, _ := lookupRaw(t, router, "never-existed")
	if hiddenStatus != unknownStatus {
		t.Fatalf("status differs: hidden=%d unknown=%d", hiddenStatus, unknownStatus)
	}
	if strings.TrimSuffix(hiddenMessage, "hidden-node") != strings.TrimSuffix(unknownMessage, "never-existed") {
		t.Fatalf("messages differ beyond the echoed uuid: hidden=%q unknown=%q", hiddenMessage, unknownMessage)
	}
}

// TestLookupVisibleNodeKeepsItsUnlockPayload：非隐藏节点的既有可用行为不变。
func TestLookupVisibleNodeKeepsItsUnlockPayload(t *testing.T) {
	const egress = "203.0.113.20"
	handler := newAccessHandler(t,
		map[string]bool{"visible-node": false},
		map[string]*UnlockData{"visible-node": unlockPayload(egress)},
	)
	router := newTestRouter(handler)

	recorder := doRequest(t, router, http.MethodGet,
		"/api/public/ip-info/v1/lookup?uuid=visible-node&ip=1.2.3.4", nil)
	_, data, _ := decodeSuccess(t, recorder)

	if got := asString(t, data, "uuid"); got != "visible-node" {
		t.Fatalf("uuid = %q, want the requested one echoed back", got)
	}
	payload := asObject(t, data, "unlock")
	if got := asString(t, payload, "egress_ip"); got != egress {
		t.Fatalf("unlock.egress_ip = %q, want %q", got, egress)
	}
	capabilities := asObject(t, data, "capabilities")
	if !asBool(t, capabilities, "media_unlock") || !asBool(t, capabilities, "ai_unlock") {
		t.Fatalf("capabilities lost the unlock flags: %v", capabilities)
	}
}

// TestLookupHiddenNodeIsReadableForAdmins：管理员仍能读隐藏节点的快照，
// 与 clientHistoryReadable / public:* 的约定一致。
func TestLookupHiddenNodeIsReadableForAdmins(t *testing.T) {
	const egress = "203.0.113.30"
	handler := newAccessHandler(t,
		map[string]bool{"hidden-node": true},
		map[string]*UnlockData{"hidden-node": unlockPayload(egress)},
	)

	adminRouter := gin.New()
	adminRouter.Use(func(c *gin.Context) { c.Set("role", api.RoleAdmin); c.Next() })
	adminRouter.GET("/api/public/ip-info/v1/lookup", handler.Lookup)

	recorder := doRequest(t, adminRouter, http.MethodGet,
		"/api/public/ip-info/v1/lookup?uuid=hidden-node&ip=1.2.3.4", nil)
	_, data, _ := decodeSuccess(t, recorder)
	if got := asString(t, asObject(t, data, "unlock"), "egress_ip"); got != egress {
		t.Fatalf("admin unlock.egress_ip = %q, want %q", got, egress)
	}
}

// TestLookupEmptyUUIDStaysResolvableWithoutNodeData：uuid 为空是契约内的调用方式
// （docs/IP-INFO-API.md 记录的第三方主题按地址查询就是 uuid=），必须保持 200，
// 而且不能因此去解析任何节点。
func TestLookupEmptyUUIDStaysResolvableWithoutNodeData(t *testing.T) {
	handler, _, _ := newTestHandler(t)
	handler.loadUnlock = func(uuid string, loggedIn bool) (*UnlockData, error) {
		t.Fatalf("an empty uuid must not resolve a node (uuid=%q loggedIn=%v)", uuid, loggedIn)
		return nil, nil
	}
	router := newTestRouter(handler)

	recorder := doRequest(t, router, http.MethodGet, "/api/public/ip-info/v1/lookup?ip=1.2.3.4", nil)
	_, data, _ := decodeSuccess(t, recorder)
	if got := asString(t, data, "uuid"); got != "" {
		t.Fatalf("uuid = %q, want empty", got)
	}
	if _, present := data["unlock"]; present {
		t.Fatalf("an empty uuid produced node data: %s", recorder.Body.String())
	}
}

// TestLookupDegradesWhenTheUnlockReadFails：读取失败（数据库抖动）不能让整个 IP 信息
// 接口失败 —— 解锁只是附加信息；但既然无法确认可见性，就不能返回任何节点数据。
func TestLookupDegradesWhenTheUnlockReadFails(t *testing.T) {
	handler, _, _ := newTestHandler(t)
	handler.loadUnlock = func(string, bool) (*UnlockData, error) {
		return nil, errors.New("database is unavailable")
	}
	router := newTestRouter(handler)

	recorder := doRequest(t, router, http.MethodGet,
		"/api/public/ip-info/v1/lookup?uuid=some-node&ip=1.2.3.4", nil)
	_, data, _ := decodeSuccess(t, recorder)
	if _, present := data["unlock"]; present {
		t.Fatalf("a failed read still produced node data: %s", recorder.Body.String())
	}
	capabilities := asObject(t, data, "capabilities")
	if asBool(t, capabilities, "media_unlock") || asBool(t, capabilities, "ai_unlock") {
		t.Fatalf("unverified capabilities = %v, want false", capabilities)
	}
}

// TestLookupWithoutADatabaseServesNoNodeData：生产实现（未注入）在数据库未就绪时
// 同样不返回节点数据、也不报错。这条同时是既有用例「本包不建库仍然完整可用」的回归。
func TestLookupWithoutADatabaseServesNoNodeData(t *testing.T) {
	handler, _, _ := newTestHandler(t)
	router := newTestRouter(handler)

	recorder := doRequest(t, router, http.MethodGet,
		"/api/public/ip-info/v1/lookup?uuid=client-a&ip=1.2.3.4", nil)
	_, data, _ := decodeSuccess(t, recorder)
	if _, present := data["unlock"]; present {
		t.Fatalf("no database, yet node data appeared: %s", recorder.Body.String())
	}
}

// TestVisibleUnlockDataNeverInventsAProbe：没有探测结果的快照必须转成 nil，
// 否则面板会把「还没探测」显示成「未解锁」。
func TestVisibleUnlockDataNeverInventsAProbe(t *testing.T) {
	if got := visibleUnlockData(nil); got != nil {
		t.Fatalf("nil report = %+v, want nil", got)
	}
	if got := visibleUnlockData(&v2.UnlockParams{EgressIP: "203.0.113.9"}); got != nil {
		t.Fatalf("report without results = %+v, want nil", got)
	}

	probedAt := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	got := visibleUnlockData(&v2.UnlockParams{
		EgressIP:     "203.0.113.9",
		EgressRegion: "JP",
		ProbedAt:     probedAt,
		Results:      []v2.UnlockItem{{ID: "netflix", Name: "Netflix", Kind: "media", Status: "ok", Basis: "probe"}},
	})
	if got == nil {
		t.Fatal("a probed report was dropped")
	}
	if got.EgressIP != "203.0.113.9" || got.EgressRegion != "JP" ||
		got.ProbedAt != "2026-10-01T12:00:00Z" || len(got.Results) != 1 {
		t.Fatalf("translated payload = %+v", got)
	}
}

// lookupRaw 发一次匿名查询，返回状态码、错误消息（成功时为空）与响应体原文。
func lookupRaw(t *testing.T, router *gin.Engine, uuid string) (int, string, string) {
	t.Helper()
	recorder := doRequest(t, router, http.MethodGet,
		"/api/public/ip-info/v1/lookup?uuid="+uuid+"&ip=1.2.3.4", nil)
	message := ""
	if recorder.Code >= 400 {
		message = decodeError(t, recorder)
	}
	return recorder.Code, message, recorder.Body.String()
}
