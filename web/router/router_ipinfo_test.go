package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestIpInfoRoutesRegistered 校验四条 IP 信息路由注册在预期的方法与路径上。
//
// 这里只断言路由表与「不需要上游就能判定」的响应，避免测试依赖真实网络。
func TestIpInfoRoutesRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	Register(engine)

	want := []string{
		"GET /api/public/ip-info/v1/status",
		"GET /api/public/ip-info/v1/lookup",
		"GET /api/public/ip-info/v1/latency",
		"POST /api/admin/ip-info/v1/refresh",
	}
	registered := make(map[string]bool, len(engine.Routes()))
	for _, route := range engine.Routes() {
		registered[route.Method+" "+route.Path] = true
	}
	for _, key := range want {
		if !registered[key] {
			t.Errorf("route %q is not registered", key)
		}
	}

	// /status 不依赖任何上游，可以直接打通。
	recorder := serve(t, engine, http.MethodGet, "/api/public/ip-info/v1/status", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status route returned %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"ok":true`) {
		t.Errorf("status body = %s, want ok:true", recorder.Body.String())
	}

	// lookup 是公开路由：私有地址在参数校验阶段就被拒绝，不会打到上游。
	recorder = serve(t, engine, http.MethodGet, "/api/public/ip-info/v1/lookup?ip=127.0.0.1", nil)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("lookup route returned %d, want 400: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"error"`) {
		t.Errorf("lookup body = %s, want an error envelope", recorder.Body.String())
	}
}

// TestIpInfoAdminRefreshRequiresAdmin 校验刷新接口受 /api/admin 的 RequireRole 保护。
func TestIpInfoAdminRefreshRequiresAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	Register(engine)

	body := strings.NewReader(`{"uuid":"u","ip":"1.2.3.4","force":true}`)
	recorder := serve(t, engine, http.MethodPost, "/api/admin/ip-info/v1/refresh", body)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated refresh returned %d, want 401: %s", recorder.Code, recorder.Body.String())
	}
}

func serve(t *testing.T, engine *gin.Engine, method, target string, body *strings.Reader) *httptest.ResponseRecorder {
	t.Helper()
	var request *http.Request
	if body == nil {
		request = httptest.NewRequest(method, target, nil)
	} else {
		request = httptest.NewRequest(method, target, body)
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	return recorder
}
