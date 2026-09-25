package jsonrpc

import (
	"context"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/database/tasks"
	"github.com/Aone2233/nekomari/pkg/rpc"
	"github.com/Aone2233/nekomari/utils"
	logger "github.com/Aone2233/nekomari/utils/log"
)

// admin.ping.go
// 延迟监测任务（ping task）的 RPC2 方法（admin 命名空间）。

func init() {
	RegisterWithGroupAndMeta("addPingTask", rpc.RoleAdmin, adminAddPingTask, &rpc.MethodMeta{
		Name:    "admin:addPingTask",
		Summary: "Create a ping task",
		Returns: "{ task_id: uint }",
	})
	RegisterWithGroupAndMeta("deletePingTask", rpc.RoleAdmin, adminDeletePingTask, &rpc.MethodMeta{
		Name:    "admin:deletePingTask",
		Summary: "Delete ping tasks by ids",
		Returns: "null",
	})
	RegisterWithGroupAndMeta("editPingTask", rpc.RoleAdmin, adminEditPingTask, &rpc.MethodMeta{
		Name:    "admin:editPingTask",
		Summary: "Edit ping tasks",
		Returns: "null",
	})
	RegisterWithGroupAndMeta("getAllPingTasks", rpc.RoleAdmin, adminGetAllPingTasks, &rpc.MethodMeta{
		Name:    "admin:getAllPingTasks",
		Summary: "List all ping tasks",
		Returns: "PingTask[]",
	})
	RegisterWithGroupAndMeta("orderPingTask", rpc.RoleAdmin, adminOrderPingTask, &rpc.MethodMeta{
		Name:    "admin:orderPingTask",
		Summary: "Reorder ping tasks (map of id->weight)",
		Returns: "null",
	})
}

func adminAddPingTask(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		Clients   []string `json:"clients"`
		DefaultOn bool     `json:"default_on"`
		Name      string   `json:"name"`
		Target    string   `json:"target"`
		TaskType  string   `json:"type"`
		Interval  int      `json:"interval"`
		Reference string   `json:"reference"`
	}
	req.BindParams(&params)
	if params.Name == "" || params.Target == "" || params.TaskType == "" || params.Interval == 0 {
		return nil, rpc.MakeError(rpc.InvalidParams, "name, target, type and interval are required", nil)
	}
	if !params.DefaultOn && len(params.Clients) == 0 {
		return nil, rpc.MakeError(rpc.InvalidParams, "clients is required when default_on is false", nil)
	}
	// A hostname target with probes that can land on different address families
	// reports two different paths as one number; refuse it at creation. See
	// utils.ValidatePingTaskTargetFamily for the rule and the evidence.
	if err := utils.ValidatePingTaskTargetFamily(params.Target, params.DefaultOn, params.Clients); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, err.Error(), nil)
	}
	taskID, err := tasks.AddPingTask(params.Clients, params.DefaultOn, params.Name, params.Target, params.TaskType, params.Interval, params.Reference)
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, err.Error(), nil)
	}
	return map[string]any{"task_id": taskID}, nil
}

func adminDeletePingTask(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		ID []uint `json:"id"`
	}
	req.BindParams(&params)
	if len(params.ID) == 0 {
		return nil, rpc.MakeError(rpc.InvalidParams, "id is required", nil)
	}
	if err := tasks.DeletePingTask(params.ID); err != nil {
		return nil, rpc.MakeError(rpc.InternalError, err.Error(), nil)
	}
	return nil, nil
}

func adminEditPingTask(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		Tasks []*models.PingTask `json:"tasks"`
	}
	req.BindParams(&params)
	if len(params.Tasks) == 0 {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid request data", nil)
	}
	for _, task := range params.Tasks {
		if task == nil {
			return nil, rpc.MakeError(rpc.InvalidParams, "Invalid request data", nil)
		}
		// An edit is another way to create the mixture, so the same rule applies.
		if err := utils.ValidatePingTaskTargetFamily(task.Target, task.DefaultOn, task.Clients); err != nil {
			return nil, rpc.MakeError(rpc.InvalidParams, err.Error(), nil)
		}
	}
	if err := tasks.EditPingTask(params.Tasks); err != nil {
		return nil, rpc.MakeError(rpc.InternalError, err.Error(), nil)
	}
	return nil, nil
}

func adminGetAllPingTasks(_ context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	list, err := tasks.GetAllPingTasks()
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, err.Error(), nil)
	}

	// 附带"哪些节点会被地址族过滤掉"，供管理界面标注。
	//
	// 为什么由服务端算：/api/nodes 是公开接口，刻意不暴露节点的 ipv4/ipv6 —— 那属于
	// 基础设施信息。而"这个节点探不到这个目标"是调度器的既有判断，放在这里算可以保证
	// 界面显示的和实际下发的一致；如果在浏览器里重算一份，两边迟早会漂移。
	skipped, err := utils.PingTasksFamilySkips(list)
	if err != nil {
		// 标注失败不该让整个任务列表请求失败，返回不带标注的结果即可。
		logger.Errorf("jsonrpc", "failed to compute address-family skips: %v", err)
		return list, nil
	}

	type taskWithSkips struct {
		models.PingTask
		SkippedClients []string `json:"skipped_clients"`
	}
	out := make([]taskWithSkips, 0, len(list))
	for _, t := range list {
		out = append(out, taskWithSkips{
			PingTask:       t,
			SkippedClients: skipped[t.Id],
		})
	}
	return out, nil
}

func adminOrderPingTask(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	// 参数为 { idStr: weight } 映射。
	order := map[uint]int{}
	var raw map[string]int
	if err := req.BindParams(&raw); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid or missing request body: "+err.Error(), nil)
	}
	for idStr, weight := range raw {
		id, err := parseUintKey(idStr)
		if err != nil {
			return nil, rpc.MakeError(rpc.InvalidParams, "Invalid task id: "+idStr, nil)
		}
		order[id] = weight
	}
	if err := tasks.UpdatePingTaskOrder(order); err != nil {
		return nil, rpc.MakeError(rpc.InternalError, err.Error(), nil)
	}
	return nil, nil
}
