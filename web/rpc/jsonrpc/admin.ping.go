package jsonrpc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/database/tasks"
	"github.com/Aone2233/nekomari/pkg/rpc"
	"github.com/Aone2233/nekomari/utils"
	logger "github.com/Aone2233/nekomari/utils/log"
	agentRuntime "github.com/Aone2233/nekomari/web/agent"
)

var pinnedDiagnosticSlots = make(chan struct{}, 8)

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
	RegisterWithGroupAndMeta("getPinnedPingDiagnostic", rpc.RoleAdmin, adminGetPinnedPingDiagnostic, &rpc.MethodMeta{
		Name:    "admin:getPinnedPingDiagnostic",
		Summary: "Inspect a current family-pinned task assignment",
		Returns: "PinnedDiagnosticSnapshot",
	})
	RegisterWithGroupAndMeta("getPingScheduleState", rpc.RoleAdmin, adminGetPingScheduleState, &rpc.MethodMeta{
		Name: "admin:getPingScheduleState", Summary: "Inspect whether the last ping schedule reload failed",
		Returns: "{ reload_pending: bool }",
	})
}

func adminGetPingScheduleState(_ context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	return map[string]bool{"reload_pending": tasks.PingScheduleReloadPending()}, nil
}

func adminGetPinnedPingDiagnostic(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		TaskID uint   `json:"task_id"`
		Client string `json:"client"`
	}
	if err := req.BindParams(&params); err != nil || params.TaskID == 0 || params.Client == "" || len(params.Client) > 64 {
		return nil, rpc.MakeError(rpc.InvalidParams, "task_id and client are required", nil)
	}
	// Keep even authenticated inspection from accumulating unbounded waiters
	// on task and credential guards during reconnects or database contention.
	select {
	case pinnedDiagnosticSlots <- struct{}{}:
		defer func() { <-pinnedDiagnosticSlots }()
	default:
		return nil, rpc.MakeError(rpc.Unavailable, "pinned diagnostic busy; retry later", nil)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	snapshot, err := agentRuntime.SnapshotPinnedDiagnostic(bounded, params.TaskID, params.Client)
	if errors.Is(err, agentRuntime.ErrPinnedDiagnosticNotFound) {
		return nil, rpc.MakeError(rpc.NotFound, "pinned task assignment not found", nil)
	}
	if err != nil {
		return nil, rpc.MakeError(rpc.Unavailable, "pinned diagnostic unavailable; retry later", nil)
	}
	return snapshot, nil
}

func adminAddPingTask(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	return adminAddPingTaskWith(ctx, req, tasks.AddPingTaskPinned)
}

// pingTaskOptions validates the additive v1.8 probe fields. Omitted attempts
// default to 3 for a newly created task; existing tasks keep their stored value
// because edits never call this helper with nil fields.
func pingTaskOptions(attempts, spacing, timeout *int, httpMode, expectedStatus, ipPreference string) (tasks.PingTaskOptions, error) {
	opts := tasks.PingTaskOptions{ProbeAttempts: 3, ProbeSpacingMS: 200}
	if attempts != nil {
		opts.ProbeAttempts = *attempts
	}
	if opts.ProbeAttempts < 1 || opts.ProbeAttempts > 10 {
		return opts, errors.New("probe_attempts must be between 1 and 10")
	}
	if spacing != nil {
		opts.ProbeSpacingMS = *spacing
	}
	if opts.ProbeSpacingMS < 50 || opts.ProbeSpacingMS > 1000 {
		return opts, errors.New("probe_spacing_ms must be between 50 and 1000")
	}
	if timeout != nil {
		opts.ProbeTimeoutMS = *timeout
	}
	if opts.ProbeTimeoutMS != 0 && (opts.ProbeTimeoutMS < 100 || opts.ProbeTimeoutMS > 30000) {
		return opts, errors.New("probe_timeout_ms must be 0 or between 100 and 30000")
	}
	switch httpMode {
	case "", "legacy", "status_aware":
		opts.HTTPMode = httpMode
	default:
		return opts, errors.New("http_mode must be empty, legacy, or status_aware")
	}
	if len(expectedStatus) > 128 {
		return opts, errors.New("http_expected_status is too long")
	}
	opts.HTTPExpectedStatus = expectedStatus
	switch ipPreference {
	case "", "auto", "4", "6":
		opts.IPVersionPreference = ipPreference
	default:
		return opts, errors.New("ip_version_preference must be empty, auto, 4, or 6")
	}
	return opts, nil
}

// A nil callback disables family-pinned creation for isolated callers.
func adminAddPingTaskWith(ctx context.Context, req *rpc.JsonRpcRequest, addPinned func(context.Context, *models.PingTask) error) (any, *rpc.JsonRpcError) {
	var params struct {
		Clients             []string `json:"clients"`
		DefaultOn           bool     `json:"default_on"`
		Name                string   `json:"name"`
		Target              string   `json:"target"`
		TaskType            string   `json:"type"`
		Interval            int      `json:"interval"`
		Reference           string   `json:"reference"`
		TargetFamily        string   `json:"target_family"`
		ProbeAttempts       *int     `json:"probe_attempts"`
		ProbeSpacingMS      *int     `json:"probe_spacing_ms"`
		ProbeTimeoutMS      *int     `json:"probe_timeout_ms"`
		HTTPMode            string   `json:"http_mode"`
		HTTPExpectedStatus  string   `json:"http_expected_status"`
		IPVersionPreference string   `json:"ip_version_preference"`
	}
	if err := req.BindParams(&params); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid request data", nil)
	}
	if params.Name == "" || params.Target == "" || params.TaskType == "" || params.Interval == 0 {
		return nil, rpc.MakeError(rpc.InvalidParams, "name, target, type and interval are required", nil)
	}
	if !params.DefaultOn && len(params.Clients) == 0 {
		return nil, rpc.MakeError(rpc.InvalidParams, "clients is required when default_on is false", nil)
	}
	opts, err := pingTaskOptions(params.ProbeAttempts, params.ProbeSpacingMS, params.ProbeTimeoutMS,
		params.HTTPMode, params.HTTPExpectedStatus, params.IPVersionPreference)
	if err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, err.Error(), nil)
	}
	if params.TargetFamily != "" {
		if params.ProbeAttempts != nil || params.ProbeSpacingMS != nil || params.ProbeTimeoutMS != nil ||
			params.HTTPMode != "" || params.HTTPExpectedStatus != "" || params.IPVersionPreference != "" {
			return nil, rpc.MakeError(rpc.InvalidParams, "family-pinned tasks do not support advanced probe options", nil)
		}
		if addPinned == nil {
			return nil, rpc.MakeError(rpc.InvalidParams, "family-pinned tasks are not available yet", nil)
		}
		if err := utils.ValidatePinnedPingTarget(params.Target, params.TaskType, params.TargetFamily); err != nil {
			return nil, rpc.MakeError(rpc.InvalidParams, err.Error(), nil)
		}
		task := models.PingTask{Name: params.Name, Target: params.Target, Type: params.TaskType,
			TargetFamily: params.TargetFamily, Reference: params.Reference, DefaultOn: params.DefaultOn,
			Clients: models.StringArray(params.Clients), Interval: params.Interval,
		}
		if err := addPinned(ctx, &task); err != nil {
			var pending *tasks.ScheduleReloadPendingError
			if errors.As(err, &pending) {
				return nil, rpc.MakeCommittedError(rpc.Unavailable, fmt.Sprintf("Task %d was saved, but its schedule did not reload. Do not create it again; refresh the task list and contact the administrator.", pending.TaskID), "ping_create_schedule_pending", pending.TaskID)
			}
			var validation *tasks.PingTaskValidationError
			if errors.As(err, &validation) {
				return nil, rpc.MakeError(rpc.InvalidParams, validation.Error(), nil)
			}
			return nil, rpc.MakeError(rpc.InternalError, err.Error(), nil)
		}
		return map[string]any{"task_id": task.Id}, nil
	}
	// The legacy hostname/family rule is checked inside the task-creation
	// transaction, rather than against an earlier RPC-layer snapshot.
	taskID, err := tasks.AddPingTaskWithOptions(params.Clients, params.DefaultOn, params.Name, params.Target, params.TaskType, params.Interval, params.Reference, opts)
	if err != nil {
		var validation *tasks.PingTaskValidationError
		if errors.As(err, &validation) {
			return nil, rpc.MakeError(rpc.InvalidParams, validation.Error(), nil)
		}
		var pending *tasks.ScheduleReloadPendingError
		if errors.As(err, &pending) {
			return nil, rpc.MakeCommittedError(rpc.Unavailable, fmt.Sprintf("Task %d was saved, but its schedule did not reload. Do not create it again; refresh the task list and contact the administrator.", pending.TaskID), "ping_create_schedule_pending", pending.TaskID)
		}
		return nil, rpc.MakeError(rpc.InternalError, err.Error(), nil)
	}
	return map[string]any{"task_id": taskID}, nil
}

func adminDeletePingTask(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	return adminDeletePingTaskWith(req, tasks.DeletePingTask)
}

func adminDeletePingTaskWith(req *rpc.JsonRpcRequest, deleteTasks func([]uint) error) (any, *rpc.JsonRpcError) {
	var params struct {
		ID []uint `json:"id"`
	}
	if err := req.BindParams(&params); err != nil || len(params.ID) == 0 {
		return nil, rpc.MakeError(rpc.InvalidParams, "id is required", nil)
	}
	if err := deleteTasks(params.ID); err != nil {
		var pending *tasks.PingDeletionPendingError
		if errors.As(err, &pending) {
			return nil, rpc.MakeCommittedError(rpc.Unavailable, "Tasks were deleted, but history cleanup or schedule reload is pending. Do not delete them again; refresh the task list and contact the administrator.", "ping_delete_cleanup_pending", 0)
		}
		return nil, rpc.MakeError(rpc.InternalError, err.Error(), nil)
	}
	return nil, nil
}

func adminEditPingTask(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	return adminEditPingTaskWith(req, true, tasks.EditPingTask)
}

func adminEditPingTaskWith(req *rpc.JsonRpcRequest, allowPinned bool, editTasks func([]tasks.PingTaskEdit) error) (any, *rpc.JsonRpcError) {
	type pingTaskEdit struct {
		models.PingTask
		Reference           *string `json:"reference"`
		Family              *string `json:"target_family"`
		ProbeAttempts       *int    `json:"probe_attempts"`
		ProbeSpacingMS      *int    `json:"probe_spacing_ms"`
		ProbeTimeoutMS      *int    `json:"probe_timeout_ms"`
		HTTPMode            *string `json:"http_mode"`
		HTTPExpectedStatus  *string `json:"http_expected_status"`
		IPVersionPreference *string `json:"ip_version_preference"`
	}
	var params struct {
		Tasks []*pingTaskEdit `json:"tasks"`
	}
	if err := req.BindParams(&params); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid request data", nil)
	}
	if len(params.Tasks) == 0 {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid request data", nil)
	}
	edits := make([]tasks.PingTaskEdit, 0, len(params.Tasks))
	for _, task := range params.Tasks {
		if task == nil {
			return nil, rpc.MakeError(rpc.InvalidParams, "Invalid request data", nil)
		}
		if !allowPinned && task.Family != nil && *task.Family != "" {
			return nil, rpc.MakeError(rpc.InvalidParams, "family-pinned tasks are not available yet", nil)
		}
		provided := task.ProbeAttempts != nil || task.ProbeSpacingMS != nil || task.ProbeTimeoutMS != nil || task.HTTPMode != nil || task.HTTPExpectedStatus != nil || task.IPVersionPreference != nil
		if task.ProbeAttempts != nil {
			task.PingTask.ProbeAttempts = *task.ProbeAttempts
		}
		if task.ProbeSpacingMS != nil {
			task.PingTask.ProbeSpacingMS = *task.ProbeSpacingMS
		}
		if task.ProbeTimeoutMS != nil {
			task.PingTask.ProbeTimeoutMS = *task.ProbeTimeoutMS
		}
		if task.HTTPMode != nil {
			task.PingTask.HTTPMode = *task.HTTPMode
		}
		if task.HTTPExpectedStatus != nil {
			task.PingTask.HTTPExpectedStatus = *task.HTTPExpectedStatus
		}
		if task.IPVersionPreference != nil {
			task.PingTask.IPVersionPreference = *task.IPVersionPreference
		}
		edits = append(edits, tasks.PingTaskEdit{Task: &task.PingTask, Reference: task.Reference, Family: task.Family, AdvancedOptionsProvided: provided})
	}
	if err := editTasks(edits); err != nil {
		var pending *tasks.ScheduleReloadPendingError
		if errors.As(err, &pending) {
			return nil, rpc.MakeCommittedError(rpc.Unavailable, "Changes were saved, but the schedule did not reload. Refresh the task list and contact the administrator.", "ping_edit_schedule_pending", 0)
		}
		var validation *tasks.PingTaskValidationError
		if errors.As(err, &validation) {
			return nil, rpc.MakeError(rpc.InvalidParams, validation.Error(), nil)
		}
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
