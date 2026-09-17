package jsonrpc

import (
	"context"
	"fmt"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/database/notification"
	"github.com/Aone2233/nekomari/pkg/rpc"
	"github.com/Aone2233/nekomari/utils/messageSender"
	"github.com/Aone2233/nekomari/utils/notifier"
	"gorm.io/gorm/clause"
)

// admin.notification.go
// 通知相关 RPC2 方法（admin 命名空间）：负载告警、离线通知。

func init() {
	// load notifications
	reg("addLoadNotification", adminAddLoadNotification, "Create a load notification")
	reg("deleteLoadNotification", adminDeleteLoadNotification, "Delete load notifications by ids")
	reg("editLoadNotification", adminEditLoadNotification, "Edit load notifications")
	reg("getAllLoadNotifications", adminGetAllLoadNotifications, "List all load notifications")
	// offline notifications
	reg("listOfflineNotifications", adminListOfflineNotifications, "List offline notifications")
	reg("editOfflineNotification", adminEditOfflineNotification, "Edit offline notifications")
	reg("enableOfflineNotification", adminEnableOfflineNotification, "Enable offline notifications for clients")
	reg("disableOfflineNotification", adminDisableOfflineNotification, "Disable offline notifications for clients")
	// send notification
	reg("sendNotification", adminSendNotification, "Send a notification")
}

// adminSendNotification 发送一条通知。仅供外部（插件/脚本）通过 RPC 调用，
// 内部通知逻辑（notifier/renewal/session 等）直接调用 messageSender.SendNotification。
func adminSendNotification(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		Event models.EventMessage `json:"event"`
	}
	if err := req.BindParams(&params); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid request data: "+err.Error(), nil)
	}
	if fmt.Sprint(params.Event.Event) == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "event is required", nil)
	}
	if err := messageSender.SendNotification(params.Event); err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to send notification: "+err.Error(), nil)
	}
	return nil, nil
}

// reg 是 admin 命名空间方法的注册便捷封装。
func reg(name string, h rpc.Handler, summary string) {
	RegisterWithGroupAndMeta(name, rpc.RoleAdmin, h, &rpc.MethodMeta{Name: "admin:" + name, Summary: summary})
}

func adminAddLoadNotification(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		Clients      []string `json:"clients"`
		Name         string   `json:"name"`
		Metric       string   `json:"metric"`
		Threshold    float32  `json:"threshold"`
		Ratio        float32  `json:"ratio"`
		Interval     int      `json:"interval"`
		Mode         string   `json:"mode"`
		BaselineDays int      `json:"baseline_days"`
		Multiplier   float32  `json:"multiplier"`
		// Tasks 仅 ping 指标需要：要盯的延迟监测任务 id 列表。
		Tasks []string `json:"tasks"`
	}
	req.BindParams(&params)
	if params.Mode == "" {
		params.Mode = models.LoadThresholdModeFixed
	}
	if params.Mode != models.LoadThresholdModeFixed && params.Mode != models.LoadThresholdModeBaseline {
		return nil, rpc.MakeError(rpc.InvalidParams, "mode must be 'fixed' or 'baseline'", nil)
	}
	// ping 指标必须有目标任务，否则规则无从求值。
	if notifier.IsPingAlertMetric(params.Metric) && len(params.Tasks) == 0 {
		return nil, rpc.MakeError(rpc.InvalidParams, "tasks are required for ping metrics", nil)
	}
	// 基线模式下 threshold 是【下限】，允许为 0；固定模式下必须给出阈值。
	thresholdRequired := params.Mode == models.LoadThresholdModeFixed
	pingMode := notifier.IsPingAlertMetric(params.Metric)

	// ping 指标的「对象」是任务，服务器由任务自身推导，因此不要求选客户端；
	// 主机指标反过来，必须选客户端。Ratio 只被主机指标使用。
	if params.Metric == "" || params.Interval == 0 || (thresholdRequired && params.Threshold == 0) {
		return nil, rpc.MakeError(rpc.InvalidParams, "metric and interval are required (threshold is required in fixed mode)", nil)
	}
	if !pingMode && len(params.Clients) == 0 {
		return nil, rpc.MakeError(rpc.InvalidParams, "clients are required for host metrics", nil)
	}
	if params.Interval > 4*60 || params.Interval <= 0 {
		return nil, rpc.MakeError(rpc.InvalidParams, "Interval must be between 1 and 240 minutes", nil)
	}
	// Ratio 只对主机指标有意义；ping 用整窗聚合值直接比阈值。
	if !pingMode && (params.Ratio <= 0 || params.Ratio > 1) {
		return nil, rpc.MakeError(rpc.InvalidParams, "Ratio must be between 0 and 1", nil)
	}
	taskID, err := notification.AddLoadNotification(params.Clients, params.Name, params.Metric, params.Threshold, params.Ratio, params.Interval,
		params.Mode, params.BaselineDays, params.Multiplier, params.Tasks)
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, err.Error(), nil)
	}
	return map[string]any{"task_id": taskID}, nil
}

func adminDeleteLoadNotification(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		ID []uint `json:"id"`
	}
	req.BindParams(&params)
	if len(params.ID) == 0 {
		return nil, rpc.MakeError(rpc.InvalidParams, "id is required", nil)
	}
	if err := notification.DeleteLoadNotification(params.ID); err != nil {
		return nil, rpc.MakeError(rpc.InternalError, err.Error(), nil)
	}
	return nil, nil
}

func adminEditLoadNotification(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		Notifications []*models.LoadNotification `json:"notifications"`
	}
	if err := req.BindParams(&params); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid request data", nil)
	}
	if err := notification.EditLoadNotification(params.Notifications); err != nil {
		return nil, rpc.MakeError(rpc.InternalError, err.Error(), nil)
	}
	return nil, nil
}

func adminGetAllLoadNotifications(_ context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	list, err := notification.GetAllLoadNotifications()
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, err.Error(), nil)
	}
	return list, nil
}

func adminListOfflineNotifications(_ context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var notifications []models.OfflineNotification
	if err := dbcore.GetDBInstance().Model(&models.OfflineNotification{}).Find(&notifications).Error; err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to list offline notifications: "+err.Error(), nil)
	}
	return notifications, nil
}

func adminEditOfflineNotification(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var notifications []models.OfflineNotification
	if err := req.BindParams(&notifications); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid request body: "+err.Error(), nil)
	}
	if len(notifications) == 0 {
		return nil, rpc.MakeError(rpc.InvalidParams, "At least one notification is required", nil)
	}
	for _, noti := range notifications {
		if noti.Client == "" {
			return nil, rpc.MakeError(rpc.InvalidParams, "Client UUID cannot be empty", nil)
		}
		if noti.GracePeriod <= 0 {
			return nil, rpc.MakeError(rpc.InvalidParams, "GracePeriod must be a positive integer", nil)
		}
	}
	err := dbcore.GetDBInstance().Model(&models.OfflineNotification{}).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "client"}},
			DoUpdates: clause.AssignmentColumns([]string{"enable", "grace_period"}),
		}).
		Select("*").Create(notifications).Error
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to edit offline notifications: "+err.Error(), nil)
	}
	return nil, nil
}

// setOfflineNotificationEnable 是 enable/disable 的共享实现。
func setOfflineNotificationEnable(req *rpc.JsonRpcRequest, enable bool) *rpc.JsonRpcError {
	var uuids []string
	if err := req.BindParams(&uuids); err != nil {
		return rpc.MakeError(rpc.InvalidParams, "Invalid request body: "+err.Error(), nil)
	}
	notifications := make([]models.OfflineNotification, 0, len(uuids))
	for _, uuid := range uuids {
		notifications = append(notifications, models.OfflineNotification{Client: uuid, Enable: enable})
	}
	err := dbcore.GetDBInstance().Model(&models.OfflineNotification{}).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "client"}},
			DoUpdates: clause.AssignmentColumns([]string{"enable"}),
		}).
		Select("client", "enable").Create(notifications).Error
	if err != nil {
		return rpc.MakeError(rpc.InternalError, "Failed to update offline notifications: "+err.Error(), nil)
	}
	return nil
}

func adminEnableOfflineNotification(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	if e := setOfflineNotificationEnable(req, true); e != nil {
		return nil, e
	}
	return nil, nil
}

func adminDisableOfflineNotification(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	if e := setOfflineNotificationEnable(req, false); e != nil {
		return nil, e
	}
	return nil, nil
}
