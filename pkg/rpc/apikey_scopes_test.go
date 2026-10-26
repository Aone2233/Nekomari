package rpc

import "testing"

func TestAPIKeyScopesNeverProvideAnAdministratorEscape(t *testing.T) {
	all := NewScopedAPIKeyPrincipal("key-id", APIKeyScopes())
	for _, method := range []string{"admin:createAPIKey", "admin:rotateAPIKey", "admin:revokeAPIKey", "admin:listAPIKeys",
		"admin:dbQuery", "admin:dbExec", "admin:getSettings", "admin:editSettings", "admin:getSessions", "admin:deleteAllSessions",
		"admin:exportConfigSecrets", "admin:importConfig", "admin:getPluginConfiguration", "admin:setPluginEnabled",
		"client:report", "admin:newUnreviewedMethod", "plugin:privileged",
		"admin:rotateClientToken", "admin:queryPanelMetrics", "admin:getRequestMetrics", "admin:getFleetFreshness",
		"admin:listTaskHistory", "admin:readTaskHistoryText", "public:exportMetrics", "public:getSlaReportPage"} {
		if CheckPrincipal(all, method) || APIKeySensitiveGrant(all, method) {
			t.Errorf("all scopes allowed privileged escape %s", method)
		}
	}
	for method, scope := range apiKeyMethodScopes {
		if !CheckPrincipal(NewScopedAPIKeyPrincipal("key-id", []string{scope}), method) {
			t.Errorf("required scope %s failed for %s", scope, method)
		}
		for _, other := range APIKeyScopes() {
			if other != scope && CheckPrincipal(NewScopedAPIKeyPrincipal("key-id", []string{other}), method) {
				t.Errorf("scope %s leaked into %s", other, method)
			}
		}
	}
	for _, method := range []string{"admin:exec", "admin:rollAgentUpdate", "admin:getClientToken"} {
		if !APIKeySensitiveGrant(all, method) || APIKeySensitiveGrant(NewAPIKeyPrincipal(), method) {
			t.Errorf("sensitive grant policy failed for %s", method)
		}
	}
	if NewScopedAPIKeyPrincipal("key-id", []string{ScopeTasksExec}).CanReadPrivateNodes() {
		t.Fatal("execution grant also disclosed private nodes")
	}
	Allow("scope-test:guestPlugin", RoleGuest)
	if CheckPrincipal(all, "scope-test:guestPlugin") || CheckPrincipal(all, "public:guestPlugin") {
		t.Fatal("plugin methods inherit key access without review")
	}
	for _, route := range []string{"/api/admin/download/backup", "/api/admin/settings/", "/api/admin/2fa/generate", "/api/admin/update/user",
		"/api/admin/client/:uuid/terminal", "/api/admin/client/:uuid/file/download", "/api/admin/upload/merge", "/api/admin/plugin/:short/*filepath",
		"/api/admin/database-recovery/status", "/plugin/custom", "/api/admin/api-keys"} {
		if APIKeyHTTPAllowed(all, "GET", route) || APIKeyHTTPAllowed(all, "POST", route) {
			t.Errorf("all scopes allowed unreviewed route %s", route)
		}
	}
}
