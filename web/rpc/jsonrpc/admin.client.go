package jsonrpc

import (
	"context"

	"github.com/Aone2233/nekomari/database/auditlog"
	"github.com/Aone2233/nekomari/database/clients"
	"github.com/Aone2233/nekomari/database/records"
	"github.com/Aone2233/nekomari/internal/metricstore"
	"github.com/Aone2233/nekomari/pkg/rpc"
	agent_runtime "github.com/Aone2233/nekomari/web/agent"
)

// admin.client.go
// client 资源的 RPC2 方法（admin 命名空间）。承载原 web/api/admin/client.go 的业务逻辑，
// 包含审计日志与运行时副作用。传统 REST handler 经 CallFromGin 转调这些方法。

func init() {
	RegisterWithGroupAndMeta("addClient", rpc.RoleAdmin, adminAddClient, &rpc.MethodMeta{
		Name:    "admin:addClient",
		Summary: "Create a new client",
		Params: []rpc.ParamMeta{
			{Name: "name", Type: "string", Required: false, Description: "Optional client name"},
		},
		Returns: "{ uuid: string, token?: string }; API keys read credentials separately with admin:getClientToken",
	})
	RegisterWithGroupAndMeta("editClient", rpc.RoleAdmin, adminEditClient, &rpc.MethodMeta{
		Name:    "admin:editClient",
		Summary: "Edit a client (partial update)",
		Params: []rpc.ParamMeta{
			{Name: "uuid", Type: "string", Required: true, Description: "Client UUID"},
		},
		Returns: "null",
	})
	RegisterWithGroupAndMeta("removeClient", rpc.RoleAdmin, adminRemoveClient, &rpc.MethodMeta{
		Name:    "admin:removeClient",
		Summary: "Delete a client",
		Params: []rpc.ParamMeta{
			{Name: "uuid", Type: "string", Required: true, Description: "Client UUID"},
		},
		Returns: "null",
	})
	RegisterWithGroupAndMeta("getClient", rpc.RoleAdmin, adminGetClient, &rpc.MethodMeta{
		Name:    "admin:getClient",
		Summary: "Get a client by UUID",
		Params: []rpc.ParamMeta{
			{Name: "uuid", Type: "string", Required: true, Description: "Client UUID"},
		},
		Returns: "Client",
	})
	RegisterWithGroupAndMeta("listClients", rpc.RoleAdmin, adminListClients, &rpc.MethodMeta{
		Name:    "admin:listClients",
		Summary: "List all clients (basic info)",
		Returns: "Client[]",
	})
	RegisterWithGroupAndMeta("getClientToken", rpc.RoleAdmin, adminGetClientToken, &rpc.MethodMeta{
		Name:    "admin:getClientToken",
		Summary: "Get a client's token by UUID",
		Params: []rpc.ParamMeta{
			{Name: "uuid", Type: "string", Required: true, Description: "Client UUID"},
		},
		Returns: "{ token: string }",
	})
	// Sessions require step-up; keys need the explicit agents:token:read grant.
	rpc.MarkSensitive("admin:getClientToken")
	RegisterWithGroupAndMeta("clearRecords", rpc.RoleAdmin, adminClearRecords, &rpc.MethodMeta{
		Name:    "admin:clearRecords",
		Summary: "Delete all load records",
		Returns: "null",
	})
}

// auditActorAPIKey is the fallback label for old internal unscoped callers.
const auditActorAPIKey = "api_key"

// apiKeyPlaceholderUUID 是 IdentityMiddleware 为 API key 写进旧兼容字段的占位 UUID。
const apiKeyPlaceholderUUID = "00000000-0000-0000-0000-000000000000"

// auditActor 从上下文提取审计用的 actor 标识与来源 IP。
//
// Sessions use their user UUID; keys use their non-secret key UUID.
func auditActor(ctx context.Context) (uuid, ip string) {
	meta := rpc.MetaFromContext(ctx)
	if meta == nil {
		return "", ""
	}
	ip = meta.RemoteIP
	if meta.Principal != nil && meta.Principal.IsAPIKey {
		if meta.Principal.APIKeyID != "" {
			return meta.Principal.APIKeyID, ip
		}
		return auditActorAPIKey, ip
	}
	uuid = meta.UserUUID
	if uuid == apiKeyPlaceholderUUID {
		// 有的调用点仍从 HTTP 兼容字段取 UUID；占位值不是用户。
		uuid = auditActorAPIKey
	}
	return uuid, ip
}

func adminAddClient(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		Name string `json:"name"`
	}
	req.BindParams(&params)

	var (
		uuid, token string
		err         error
	)
	if params.Name == "" {
		uuid, token, err = clients.CreateClient()
	} else {
		uuid, token, err = clients.CreateClientWithName(params.Name)
	}
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, err.Error(), nil)
	}
	actor, ip := auditActor(ctx)
	auditlog.Log(ip, actor, "create client:"+uuid, "info")
	result := map[string]any{"uuid": uuid}
	if meta := rpc.MetaFromContext(ctx); meta == nil || meta.Principal == nil || meta.Principal.Type != rpc.PrincipalAPIKey {
		result["token"] = token
	}
	return result, nil
}

func adminEditClient(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var update map[string]interface{}
	if err := req.BindParams(&update); err != nil || update == nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid params", nil)
	}
	uuid, _ := update["uuid"].(string)
	if uuid == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid or missing UUID", nil)
	}
	if jerr := validateAPIKeyClientUpdate(ctx, update); jerr != nil {
		return nil, jerr
	}
	if err := clients.SaveClient(update); err != nil {
		return nil, rpc.MakeError(rpc.InternalError, err.Error(), nil)
	}
	actor, ip := auditActor(ctx)
	auditlog.Log(ip, actor, "edit client:"+uuid, "info")
	return nil, nil
}

func adminRemoveClient(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		UUID string `json:"uuid"`
	}
	req.BindParams(&params)
	if params.UUID == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid or missing UUID", nil)
	}
	if err := clients.DeleteClient(params.UUID); err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to delete client"+err.Error(), nil)
	}
	metricstore.DeleteEntityAsync(params.UUID)
	actor, ip := auditActor(ctx)
	auditlog.Log(ip, actor, "delete client:"+params.UUID, "warn")
	agent_runtime.DeleteConnectedClients(params.UUID)
	agent_runtime.DeleteLatestReport(params.UUID)
	return nil, nil
}

func adminGetClient(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		UUID string `json:"uuid"`
	}
	req.BindParams(&params)
	if params.UUID == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid or missing UUID", nil)
	}
	result, err := clients.GetClientByUUID(params.UUID)
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, err.Error(), nil)
	}
	if meta := rpc.MetaFromContext(ctx); meta != nil && meta.Principal != nil && meta.Principal.Type == rpc.PrincipalAPIKey {
		result.Token = ""
	}
	return result, nil
}

func adminListClients(ctx context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	cls, err := clients.GetAllClientBasicInfo()
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, err.Error(), nil)
	}
	if meta := rpc.MetaFromContext(ctx); meta != nil && meta.Principal != nil && meta.Principal.Type == rpc.PrincipalAPIKey {
		for i := range cls {
			cls[i].Token = ""
		}
	}
	return cls, nil
}

func validateAPIKeyClientUpdate(ctx context.Context, update map[string]any) *rpc.JsonRpcError {
	meta := rpc.MetaFromContext(ctx)
	if meta == nil || meta.Principal == nil || meta.Principal.Type != rpc.PrincipalAPIKey {
		return nil
	}
	for key := range update {
		switch key {
		case "uuid", "name", "group", "tags", "weight", "price", "billing_cycle", "currency", "expired_at",
			"hidden", "traffic_limit", "traffic_limit_type", "auto_renewal", "public_remark", "remark", "region":
		default:
			return rpc.MakeLocalizedError(rpc.PermissionDenied, "nodes:write cannot change credentials, agent versions or report fields", "nodes_write_restricted")
		}
	}
	return nil
}

func adminGetClientToken(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		UUID string `json:"uuid"`
	}
	req.BindParams(&params)
	if params.UUID == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid or missing UUID", nil)
	}
	token, err := clients.GetClientTokenByUUID(params.UUID)
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, err.Error(), nil)
	}
	// Audit the key UUID (or user UUID), never the credential.
	actor, ip := auditActor(ctx)
	auditlog.Log(ip, actor, "read client token:"+params.UUID, "warn")
	return map[string]any{"token": token}, nil
}

func adminClearRecords(ctx context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	if err := records.DeleteAll(); err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to delete Record"+err.Error(), nil)
	}
	actor, ip := auditActor(ctx)
	auditlog.Log(ip, actor, "clear records", "warn")
	return nil, nil
}
