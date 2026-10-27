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
		Returns: "{ uuid: string, token: string }",
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
	// 读取 Agent 凭据属于敏感操作：token 是 agent 的长期 bearer 凭据，拿到它等于可以冒充
	// 该节点上报数据。普通会话因此需要敏感 2FA；API key 仍按既有设计豁免（见
	// dispatchWithSensitive → VerifySensitive2FACore(..., isAPIKey)），本轮不改变它的
	// 权限/scope。标记在这里而不是 transport 里：名单属于方法自己的声明，敏感名单用例
	// （sensitive_stepup_test.go）会把这个新增项一起审阅。
	rpc.MarkSensitive("admin:getClientToken")
	RegisterWithGroupAndMeta("clearRecords", rpc.RoleAdmin, adminClearRecords, &rpc.MethodMeta{
		Name:    "admin:clearRecords",
		Summary: "Delete all load records",
		Returns: "null",
	})
}

// auditActorAPIKey 是审计日志里 API key 调用的 actor 标识。
//
// API key 没有用户身份：HTTP 兼容字段里是占位 UUID（全零），RPC meta 的 UserUUID 是空串。
// 两者都不适合直接落进日志 —— 空串看起来像「漏记」，全零 UUID 看起来像某个具体账号
// （实际只是占位），排查时会把人引向错误方向。统一记成 "api_key"，明确「这是凭据调用，
// 不是某个用户操作」。
const auditActorAPIKey = "api_key"

// apiKeyPlaceholderUUID 是 IdentityMiddleware 为 API key 写进旧兼容字段的占位 UUID。
const apiKeyPlaceholderUUID = "00000000-0000-0000-0000-000000000000"

// auditActor 从上下文提取审计用的 actor 标识与来源 IP。
//
// 普通会话返回真实用户 UUID；API key 返回 "api_key"（见上面的注释）。没有 meta 时返回空值，
// 与改动前一致。
func auditActor(ctx context.Context) (uuid, ip string) {
	meta := rpc.MetaFromContext(ctx)
	if meta == nil {
		return "", ""
	}
	ip = meta.RemoteIP
	if meta.Principal != nil && meta.Principal.IsAPIKey {
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
	if params.Name != "" {
		actor, ip := auditActor(ctx)
		auditlog.Log(ip, actor, "create client:"+uuid, "info")
	}
	return map[string]any{"uuid": uuid, "token": token}, nil
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
	agent_runtime.DeleteV2EventQueue(params.UUID)
	return nil, nil
}

func adminGetClient(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
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
	return result, nil
}

func adminListClients(_ context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	cls, err := clients.GetAllClientBasicInfo()
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, err.Error(), nil)
	}
	return cls, nil
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
	// 凭据读取得留痕：敏感门回答「谁有资格读」，审计回答「谁真的读过」。actor 对 API key
	// 是 "api_key"（见 auditActor），不会伪装成某个用户。
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
