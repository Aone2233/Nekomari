package ipinfo

import (
	"net/http"
	"testing"
)

// TestIpInfoLookupWithoutUnlockReport 固定住「还没探测过」的表达方式。
//
// 没有探测记录时必须是：能力位为 false，且整个 unlock 字段不出现。面板据此显示
// 「等待探测」。如果这里改成输出一个空结果或把能力位设成 true，面板就会把「还没测」
// 显示成「已解锁」—— 这正是这次要避免的那类静默错误。
//
// 测试环境不建数据库，所以这条路径同时也是「数据库不可用时接口仍然完整」的回归。
func TestIpInfoLookupWithoutUnlockReport(t *testing.T) {
	handler, _, _ := newTestHandler(t)
	router := newTestRouter(handler)

	recorder := doRequest(t, router, http.MethodGet, "/api/public/ip-info/v1/lookup?uuid=client-a&ip=1.2.3.4", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("lookup returned %d: %s", recorder.Code, recorder.Body.String())
	}
	_, data, _ := decodeSuccess(t, recorder)

	capabilities := asObject(t, data, "capabilities")
	if asBool(t, capabilities, "media_unlock") || asBool(t, capabilities, "ai_unlock") {
		t.Error("unlock capabilities must be false when no probe has reported yet")
	}
	if _, present := data["unlock"]; present {
		t.Error("the unlock field must be absent, not an empty object, when there is no report")
	}
}
