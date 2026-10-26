package rpc

import "strings"

const (
	ScopeNodesRead      = "nodes:read"
	ScopeNodesWrite     = "nodes:write"
	ScopeLogsRead       = "logs:read"
	ScopeTasksRead      = "tasks:read"
	ScopeTasksExec      = "tasks:exec"
	ScopeAgentUpdate    = "agents:update"
	ScopeAgentTokenRead = "agents:token:read"
	ScopeDiagnostics    = "diagnostics:read"
)

func APIKeyScopes() []string {
	return []string{ScopeNodesRead, ScopeNodesWrite, ScopeLogsRead, ScopeTasksRead,
		ScopeTasksExec, ScopeAgentUpdate, ScopeAgentTokenRead, ScopeDiagnostics}
}

// Deliberately exact, not prefix- or role-based. New/plugin methods are denied
// until reviewed. SQL, settings, sessions, key management and arbitrary uploads
// are never available to keys: each could manufacture a broader credential.
var apiKeyMethodScopes = map[string]string{
	"admin:getClient":                ScopeNodesRead,
	"admin:listClients":              ScopeNodesRead,
	"admin:getAllPingTasks":          ScopeNodesRead,
	"admin:listMaintenanceWindows":   ScopeNodesRead,
	"admin:listOfflineNotifications": ScopeNodesRead,
	"admin:getAllLoadNotifications":  ScopeNodesRead,
	"admin:getTrafficForecast":       ScopeNodesRead,
	"admin:agentVersions":            ScopeNodesRead,
	"admin:getDatabaseSize":          ScopeNodesRead,
	"admin:addClient":                ScopeNodesWrite,
	"admin:editClient":               ScopeNodesWrite,
	"admin:removeClient":             ScopeNodesWrite,
	"admin:orderClients":             ScopeNodesWrite,
	"admin:bulkEditClients":          ScopeNodesWrite,
	"admin:getLogs":                  ScopeLogsRead,
	"admin:getTasks":                 ScopeTasksRead,
	"admin:getTaskById":              ScopeTasksRead,
	"admin:getTaskResultsByTaskId":   ScopeTasksRead,
	"admin:getSpecificTaskResult":    ScopeTasksRead,
	"admin:getTasksByClientId":       ScopeTasksRead,
	"admin:exec":                     ScopeTasksExec,
	"admin:rollAgentUpdate":          ScopeAgentUpdate,
	"admin:getClientToken":           ScopeAgentTokenRead,
	"admin:getUploadStats":           ScopeDiagnostics,
}

func apiKeyAllows(p *Principal, method string) bool {
	if scope := apiKeyMethodScopes[method]; scope != "" {
		return p.HasScope(scope)
	}
	if !strings.Contains(method, ":") && !strings.HasPrefix(method, "rpc.") {
		method = "common:" + method
	}
	// Public plugin methods do not inherit this allowance.
	switch method {
	case "common:getNodes", "common:getNodesLatestStatus", "common:getMe", "common:getPublicInfo",
		"common:getVersion", "common:getNodeRecentStatus", "common:getRecords",
		"public:getMe", "public:getNodesInformation", "public:getPublicSettings", "public:getVersion",
		"public:getClientRecentRecords", "public:getRecordsByUUID", "public:getPingRecords", "public:getPublicPingTasks",
		"public:getNodeInsights", "public:getSlaReport", "public:listMetricDefinitions", "public:queryMetrics",
		"public:getPingMetricStats", "rpc.ping", "rpc.version", "rpc.methods", "rpc.help":
		return RequiredRole(method) == RoleGuest
	default:
		return false
	}
}

// APIKeySensitiveGrant is the only noninteractive alternative to session 2FA.
func APIKeySensitiveGrant(p *Principal, method string) bool {
	if p == nil || p.Type != PrincipalAPIKey {
		return false
	}
	switch method {
	case "admin:exec", "admin:rollAgentUpdate", "admin:getClientToken":
		return CheckPrincipal(p, method)
	default:
		return false
	}
}

// APIKeyRouteMethod covers only reviewed REST bridges.
// Other direct/unknown handlers (recovery, terminal, files, plugins) fail closed.
func APIKeyRouteMethod(verb, route string) string {
	return apiKeyRoutes[verb+" "+route]
}

func APIKeyHTTPAllowed(p *Principal, verb, route string) bool {
	if method := APIKeyRouteMethod(verb, route); method != "" {
		return CheckPrincipal(p, method)
	}
	if route == "/api/rpc2" {
		return verb == "GET" || verb == "POST"
	}
	if verb != "GET" {
		return false
	}
	switch route {
	case "/ping", "/api/me", "/api/nodes", "/api/public", "/api/version", "/api/recent/:uuid",
		"/api/records/load", "/api/records/ping", "/api/task/ping", "/api/clients":
		return true
	default:
		return false
	}
}

var apiKeyRoutes = map[string]string{
	"GET /api/admin/database/size":              "admin:getDatabaseSize",
	"POST /api/admin/client/add":                "admin:addClient",
	"GET /api/admin/client/list":                "admin:listClients",
	"GET /api/admin/client/:uuid":               "admin:getClient",
	"POST /api/admin/client/:uuid/edit":         "admin:editClient",
	"POST /api/admin/client/:uuid/remove":       "admin:removeClient",
	"POST /api/admin/client/order":              "admin:orderClients",
	"GET /api/admin/client/:uuid/token":         "admin:getClientToken",
	"GET /api/admin/logs":                       "admin:getLogs",
	"GET /api/admin/ping/":                      "admin:getAllPingTasks",
	"GET /api/admin/notification/offline":       "admin:listOfflineNotifications",
	"GET /api/admin/notification/load/":         "admin:getAllLoadNotifications",
	"GET /api/admin/task/all":                   "admin:getTasks",
	"POST /api/admin/task/exec":                 "admin:exec",
	"GET /api/admin/task/:task_id":              "admin:getTaskById",
	"GET /api/admin/task/:task_id/result":       "admin:getTaskResultsByTaskId",
	"GET /api/admin/task/:task_id/result/:uuid": "admin:getSpecificTaskResult",
	"GET /api/admin/task/client/:uuid":          "admin:getTasksByClientId",
}
