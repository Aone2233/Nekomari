package jsonrpc

import (
	"context"
	"sort"
	"strconv"
	"time"

	"github.com/Aone2233/nekomari/database"
	"github.com/Aone2233/nekomari/database/clients"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/database/tasks"
	"github.com/Aone2233/nekomari/internal/metricstore"
	"github.com/Aone2233/nekomari/pkg/rpc"
	"github.com/Aone2233/nekomari/utils"
	agent_runtime "github.com/Aone2233/nekomari/web/agent"
)

// public.go
// 公开（guest 可访问）的只读 RPC2 方法。命名空间 public:* 对 guest 开放。
// 这些方法保持与原 REST 接口完全一致的响应形状。

func init() {
	rpc.Allow("public:*", rpc.RoleGuest)
	regPublic("getMe", publicGetMe, "Get current user info (guest-aware)")
	regPublic("getNodesInformation", publicGetNodesInformation, "List visible nodes (basic info)")
	regPublic("getPublicSettings", publicGetPublicSettings, "Get public site settings")
	regPublic("getVersion", publicGetVersion, "Get server version")
	regPublic("getClientRecentRecords", publicGetClientRecentRecords, "Get a client's recent records")
	regPublic("getRecordsByUUID", publicGetRecordsByUUID, "Get load records for a client")
	regPublic("getPingRecords", publicGetPingRecords, "Get ping records")
	regPublic("getPublicPingTasks", publicGetPublicPingTasks, "List public ping tasks")
}

func regPublic(name string, h rpc.Handler, summary string) {
	RegisterWithGroupAndMeta(name, "public", h, &rpc.MethodMeta{Name: "public:" + name, Summary: summary})
}

// isLoginFromCtx 依据 meta 判断是否为已登录管理员。
func isLoginFromCtx(ctx context.Context) bool {
	if meta := rpc.MetaFromContext(ctx); meta != nil {
		return meta.Principal != nil && meta.Principal.CanReadPrivateNodes()
	}
	return false
}

// clientHistoryReadable reports whether stored history for uuid may be returned to
// a caller with this login state.
//
// Existence is part of the answer. The metric store keeps a node's retained history
// after its client row is deleted, so a UUID that is absent from the client table
// must not be read as "exists and is not hidden" — that is what let an anonymous
// caller request an unknown or deleted UUID and receive history the panel no longer
// lists. A hidden node's history stays readable to a logged-in user.
//
// It is pure so the rule can be tested directly; callers load `hidden` once per
// request rather than once per record.
func clientHistoryReadable(hidden map[string]bool, loggedIn bool, uuid string) bool {
	isHidden, known := hidden[uuid]
	return known && (!isHidden || loggedIn)
}

// clientVisibility loads the uuid -> hidden map for every client row. The key set
// is the existence check used by clientHistoryReadable.
func clientVisibility() (map[string]bool, error) {
	return clients.HiddenClients()
}

// visibleClientUUIDs filters a stored client list down to the uuids this caller may see.
//
// It exists because a client list is a second place a hidden node's uuid can leak:
// `PingTask.Clients` is echoed by the guest-readable public:getPublicPingTasks and by
// common:getRecords when filtering by task, and an anonymous caller could read a hidden
// uuid there and then confirm it through the history endpoints (P1-9).
//
// An empty result is not "every node": PingTask.AppliesToClient compares uuids one by
// one, so filtering can only narrow a task's scope, never widen it.
//
// A logged-in administrator gets the stored list verbatim. Filtering it by existence
// would also drop stale references to deleted nodes from a task the administrator is
// editing, which is a different decision than "do not publish hidden nodes".
func visibleClientUUIDs(uuids []string, hidden map[string]bool, loggedIn bool) []string {
	if loggedIn {
		return uuids
	}
	out := make([]string, 0, len(uuids))
	for _, uuid := range uuids {
		if clientHistoryReadable(hidden, false, uuid) {
			out = append(out, uuid)
		}
	}
	return out
}

func publicGetNodesInformation(ctx context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	clientList, err := clients.GetAllClientBasicInfo()
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to retrieve client information: "+err.Error(), nil)
	}
	isLogin := isLoginFromCtx(ctx)
	j := 0
	for i := 0; i < len(clientList); i++ {
		if clientList[i].Hidden && !isLogin {
			continue
		}
		clientList[i].IPv4 = ""
		clientList[i].IPv6 = ""
		clientList[i].Remark = ""
		clientList[i].Version = ""
		clientList[i].Token = ""
		clientList[j] = clientList[i]
		j++
	}
	clientList = clientList[:j]
	return clientList, nil
}

func publicGetPublicSettings(ctx context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	p, e := database.GetPublicInfo()
	if e != nil {
		return nil, rpc.MakeError(rpc.InternalError, e.Error(), nil)
	}
	// 临时访问许可由 transport 层在 meta 标注；此处沿用原逻辑判断 temp_key。
	if meta := rpc.MetaFromContext(ctx); meta != nil && meta.TempShareValid {
		p["private_site"] = false
	}

	// logged_in 必须出现在公开设置里，不能只放在 /api/me。
	//
	// 第三方主题（LuminaPlus）用它决定是否显示"IP 信息"标签页：
	//
	//	b = !r && n?.logged_in === true
	//	x = gn(e, s?.ipv4, s?.ipv6, s?.region, b)   // b 作为第 5 个参数
	//
	// 其中 n 是公开设置。缺少该字段时 n?.logged_in 是 undefined，`undefined === true`
	// 恒为 false，于是标签页永远不渲染 —— 而服务端接口本身完全正常，所以从网络面板
	// 查不出任何问题。这是"IP 信息"面板长期不显示的真正原因。
	//
	// 它不泄露任何信息：只表示"发起这次请求的人是否已登录"，与 /api/me 的口径一致。
	p["logged_in"] = false
	if meta := rpc.MetaFromContext(ctx); meta != nil && meta.User != nil {
		p["logged_in"] = true
	}

	return p, nil
}

func publicGetVersion(_ context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	return map[string]any{
		"version": utils.CurrentVersion,
		"hash":    utils.VersionHash,
	}, nil
}

// publicGetMe 返回当前用户信息；未登录时返回 Guest 占位，保持原 /api/me 的扁平形状。
func publicGetMe(ctx context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	guest := map[string]any{"username": "Guest", "logged_in": false}
	meta := rpc.MetaFromContext(ctx)
	if meta == nil || meta.User == nil {
		return guest, nil
	}
	u := meta.User
	return map[string]any{
		"username":    u.Username,
		"logged_in":   true,
		"uuid":        u.UUID,
		"sso_type":    u.SSOType,
		"sso_id":      u.SSOID,
		"2fa_enabled": u.TwoFactor != "",
	}, nil
}

func publicGetClientRecentRecords(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		UUID string `json:"uuid"`
	}
	req.BindParams(&params)
	if params.UUID == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "UUID is required", nil)
	}
	hidden, err := clientVisibility()
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to retrieve client information: "+err.Error(), nil)
	}
	// Same answer for a hidden node and an unknown one, so the response cannot be
	// used to probe which UUIDs exist.
	if !clientHistoryReadable(hidden, isLoginFromCtx(ctx), params.UUID) {
		return nil, rpc.MakeError(rpc.InvalidParams, "UUID is required", nil)
	}
	return agent_runtime.GetRecentReports(params.UUID), nil
}

func publicGetRecordsByUUID(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		UUID     string `json:"uuid"`
		LoadType string `json:"load_type"`
		Hours    string `json:"hours"`
	}
	req.BindParams(&params)
	if params.UUID == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "UUID is required", nil)
	}
	hidden, err := clientVisibility()
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to retrieve client information: "+err.Error(), nil)
	}
	if !clientHistoryReadable(hidden, isLoginFromCtx(ctx), params.UUID) {
		return nil, rpc.MakeError(rpc.InvalidParams, "UUID is required", nil)
	}
	hours := params.Hours
	if hours == "" {
		hours = "4"
	}
	hoursInt, err := strconv.Atoi(hours)
	if err != nil || hoursInt <= 0 || hoursInt > maxMetricQueryHours {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid hours parameter", nil)
	}
	validLoadTypes := map[string]bool{
		"cpu": true, "ram": true, "swap": true,
		"load": true, "temp": true, "disk": true, "network": true,
		"process": true, "connections": true, "all": true, "": true,
	}
	if !validLoadTypes[params.LoadType] {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid load_type parameter", nil)
	}
	now := time.Now().UTC()
	clientRecords, err := metricstore.GetRecordsByClientAndTime(ctx, params.UUID, now.Add(-time.Duration(hoursInt)*time.Hour), now)
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to fetch records: "+err.Error(), nil)
	}
	response := map[string]any{
		"records": clientRecords,
		"count":   len(clientRecords),
	}
	if params.LoadType != "" && params.LoadType != "all" {
		filtered := filterPublicRecordsByLoadType(clientRecords, params.LoadType)
		response = map[string]any{
			"records":   filtered,
			"count":     len(filtered),
			"load_type": params.LoadType,
		}
	}
	if params.LoadType == "" || params.LoadType == "all" || params.LoadType == "gpu" {
		gpuRecords, err := metricstore.GetGPURecordsByClientAndTime(ctx, params.UUID, now.Add(-time.Duration(hoursInt)*time.Hour), now)
		if err == nil && len(gpuRecords) > 0 {
			gpuDevices := make(map[string]any)
			for _, record := range gpuRecords {
				deviceKey := strconv.Itoa(record.DeviceIndex)
				if gpuDevices[deviceKey] == nil {
					gpuDevices[deviceKey] = map[string]any{
						"device_index": record.DeviceIndex,
						"device_name":  record.DeviceName,
						"records":      []models.GPURecord{},
					}
				}
				device := gpuDevices[deviceKey].(map[string]any)
				recs := device["records"].([]models.GPURecord)
				device["records"] = append(recs, record)
				gpuDevices[deviceKey] = device
			}
			response["gpu_devices"] = gpuDevices
			response["has_gpu_data"] = true
		} else {
			response["has_gpu_data"] = false
		}
	}
	return response, nil
}

func publicGetPublicPingTasks(ctx context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	pingTasks, err := tasks.GetAllPingTasks()
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, err.Error(), nil)
	}
	// 这个接口是 guest 可读的，所以它回显的 clients 列表必须过与历史接口同一道可见性门：
	// 隐藏节点的 uuid 不是公开信息。以前这里原样返回 task.Clients，匿名调用者因此能
	// 直接枚举出隐藏节点，再用 common:getNodeRecentStatus / common:getRecords 逐个确认 ——
	// 三处叠成一条完整的「找出隐藏节点」链路（P1-9）。
	isLogin := isLoginFromCtx(ctx)
	hidden, err := clientVisibility()
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to retrieve client visibility: "+err.Error(), nil)
	}
	type publicPingTask struct {
		Id        uint     `json:"id"`
		Weight    int      `json:"weight"`
		Name      string   `json:"name"`
		Clients   []string `json:"clients"`
		DefaultOn bool     `json:"default_on"`
		Type      string   `json:"type"`
		Interval  int      `json:"interval"`
	}
	out := make([]publicPingTask, len(pingTasks))
	for i, task := range pingTasks {
		out[i] = publicPingTask{
			Id:        task.Id,
			Weight:    task.Weight,
			Name:      task.Name,
			Clients:   visibleClientUUIDs(task.Clients, hidden, isLogin),
			DefaultOn: task.DefaultOn,
			Type:      task.Type,
			Interval:  task.Interval,
		}
	}
	return out, nil
}

// filterPublicRecordsByLoadType 复刻原 public 接口的字段投影逻辑。
func filterPublicRecordsByLoadType(recs []models.Record, loadType string) []map[string]any {
	out := make([]map[string]any, 0, len(recs))
	for _, r := range recs {
		record := map[string]any{"client": r.Client, "time": r.Time}
		switch loadType {
		case "cpu":
			record["cpu"] = r.Cpu
		case "gpu":
			record["gpu"] = r.Gpu
		case "ram":
			record["ram"] = r.Ram
			record["ram_total"] = r.RamTotal
			if r.RamTotal > 0 {
				record["ram_percent"] = float32(r.Ram) / float32(r.RamTotal) * 100
			}
		case "swap":
			record["swap"] = r.Swap
			record["swap_total"] = r.SwapTotal
			if r.SwapTotal > 0 {
				record["swap_percent"] = float32(r.Swap) / float32(r.SwapTotal) * 100
			}
		case "load":
			record["load"] = r.Load
		case "temp":
			record["temp"] = r.Temp
		case "disk":
			record["disk"] = r.Disk
			record["disk_total"] = r.DiskTotal
			if r.DiskTotal > 0 {
				record["disk_percent"] = float32(r.Disk) / float32(r.DiskTotal) * 100
			}
		case "network":
			record["net_in"] = r.NetIn
			record["net_out"] = r.NetOut
			record["net_total_up"] = r.NetTotalUp
			record["net_total_down"] = r.NetTotalDown
		case "process":
			record["process"] = r.Process
		case "connections":
			record["connections"] = r.Connections
			record["connections_udp"] = r.ConnectionsUdp
			record["connections_tcp"] = r.Connections - r.ConnectionsUdp
		}
		out = append(out, record)
	}
	return out
}

// PUBLIC_PING_RECORDS_PLACEHOLDER

func publicGetPingRecords(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		UUID   string `json:"uuid"`
		TaskID string `json:"task_id"`
		Hours  string `json:"hours"`
	}
	req.BindParams(&params)
	if params.UUID == "" && params.TaskID == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "UUID or task_id is required", nil)
	}
	isLogin := isLoginFromCtx(ctx)

	type recordsResp struct {
		TaskId uint      `json:"task_id,omitempty"`
		Time   time.Time `json:"time"`
		// Value is milliseconds as a JSON number. Fractional since v1.6.24 (the agent
		// reports value_us); being float64 keeps integer readings byte-identical on the
		// wire — 20 encodes as 20 — while a 420µs probe no longer reads as 0.
		Value  float64 `json:"value"`
		Client string  `json:"client,omitempty"`
		// PingType 是实际使用的探测协议（icmp/tcp/http）；dual 任务靠它区分两条记录。
		PingType string `json:"ping_type,omitempty"`
		Family   string `json:"family,omitempty"`
		// Role 为空表示主目标；"reference" 表示路径归因的参考点。
		Role string `json:"role,omitempty"`
	}
	type clientBasicInfo struct {
		Client string  `json:"client"`
		Loss   float64 `json:"loss"`
		Min    float64 `json:"min"`
		Max    float64 `json:"max"`
	}
	type resp struct {
		Count     int               `json:"count"`
		BasicInfo []clientBasicInfo `json:"basic_info,omitempty"`
		Records   []recordsResp     `json:"records"`
		Tasks     []map[string]any  `json:"tasks,omitempty"`
	}

	hidden, err := clientVisibility()
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to retrieve client information: "+err.Error(), nil)
	}
	response := &resp{Count: 0, Records: []recordsResp{}}

	// A hidden node for a guest, or a UUID that no longer exists: the same empty
	// shape either way, so the response cannot be used to probe which UUIDs exist.
	if params.UUID != "" && !clientHistoryReadable(hidden, isLogin, params.UUID) {
		return response, nil
	}

	hours := params.Hours
	if hours == "" {
		hours = "4"
	}
	hoursInt, err := strconv.Atoi(hours)
	if err != nil || hoursInt <= 0 || hoursInt > maxMetricQueryHours {
		return nil, rpc.MakeError(rpc.InvalidParams, "hours must be between 1 and 8784", nil)
	}
	endTime := time.Now().UTC()
	startTime := endTime.Add(-time.Duration(hoursInt) * time.Hour)

	taskId := -1
	if params.TaskID != "" {
		taskId, err = strconv.Atoi(params.TaskID)
		if err != nil {
			return nil, rpc.MakeError(rpc.InvalidParams, "Invalid task_id parameter", nil)
		}
	}

	recs, err := metricstore.GetPingRecords(ctx, params.UUID, taskId, startTime, endTime)
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to fetch ping records: "+err.Error(), nil)
	}

	// Keep only what the caller may see, once: the per-client and per-task summaries
	// below both work from this filtered set, so a hidden node's latency cannot leak
	// into a guest's aggregate either.
	visible := make([]models.PingRecord, 0, len(recs))
	for _, r := range recs {
		// History of a hidden node (for a guest) or of a node whose client row is gone
		// is not public, including when the request named only a task.
		if r.Client != "" && !clientHistoryReadable(hidden, isLogin, r.Client) {
			continue
		}
		visible = append(visible, r)
		response.Records = append(response.Records, recordsResp{
			Time: r.Time.UTC(), Value: pingRecordMilliseconds(r), Client: r.Client,
			TaskId: r.TaskId, PingType: r.PingType, Role: r.Role, Family: r.Family,
		})
	}

	if len(visible) > 0 {
		byClient := make(map[string][]models.PingRecord, len(visible))
		for _, r := range visible {
			byClient[r.Client] = append(byClient[r.Client], r)
		}
		clients := make([]string, 0, len(byClient))
		for client := range byClient {
			clients = append(clients, client)
		}
		// Deterministic order: a summary that reshuffles between requests cannot be diffed.
		sort.Strings(clients)
		response.BasicInfo = make([]clientBasicInfo, 0, len(clients))
		for _, client := range clients {
			summary := summarizePingLatency(byClient[client])
			loss := float64(0)
			if summary.Total > 0 {
				loss = float64(summary.Loss) / float64(summary.Total) * 100
			}
			response.BasicInfo = append(response.BasicInfo, clientBasicInfo{
				Client: client, Loss: loss, Min: summary.Min, Max: summary.Max,
			})
		}
	}

	if params.UUID != "" || taskId != -1 {
		pingTasks, err := tasks.GetAllPingTasks()
		if err != nil {
			return nil, rpc.MakeError(rpc.InternalError, "Failed to fetch ping tasks: "+err.Error(), nil)
		}
		tasksList := make([]map[string]any, 0, len(pingTasks))
		for _, t := range pingTasks {
			if taskId != -1 && t.Id != uint(taskId) {
				continue
			}
			if params.UUID != "" && !t.AppliesToClient(params.UUID) {
				continue
			}
			taskRecords := make([]models.PingRecord, 0, len(visible))
			for _, r := range visible {
				if r.TaskId != t.Id {
					continue
				}
				if params.UUID != "" && r.Client != params.UUID {
					continue
				}
				taskRecords = append(taskRecords, r)
			}
			summary := summarizePingLatency(taskRecords)
			lossRate := float64(0)
			if summary.Total > 0 {
				lossRate = float64(summary.Loss) / float64(summary.Total) * 100
			}
			taskInfo := map[string]any{
				"id": t.Id, "name": t.Name, "type": t.Type, "interval": t.Interval,
				"default_on": t.DefaultOn, "loss": lossRate, "min": summary.Min,
				"max": summary.Max, "avg": summary.Avg, "total": summary.Total,
			}
			if params.UUID == "" && taskId != -1 {
				taskInfo["clients"] = t.Clients
			}
			tasksList = append(tasksList, taskInfo)
		}
		response.Tasks = tasksList
	}

	response.Count = len(response.Records)
	return response, nil
}

// pingRecordMilliseconds returns one ping record's latency in milliseconds at the
// precision the agent reported.
//
// ValueUS is the same measurement in microseconds and wins when present: Value is
// truncated to whole milliseconds, so a 420µs probe would read as 0 without it — that
// truncation is the "0 ms" the dashboard showed for two hosts in one city. Loss stays
// the negative marker in Value (ValueUS is 0 for a failed probe), so every loss test in
// this file keeps its meaning.
func pingRecordMilliseconds(record models.PingRecord) float64 {
	if record.Value < 0 {
		return float64(record.Value)
	}
	if record.ValueUS > 0 {
		return float64(record.ValueUS) / 1000
	}
	return float64(record.Value)
}

// pingLatencySummary is the decimal aggregate the summaries in this file report: integer
// counts plus latency in fractional milliseconds. Min/Max/Avg stay 0 when Valid is 0,
// which is how the response shape has always been read ("no measurement").
type pingLatencySummary struct {
	Total int
	Loss  int
	Valid int
	Min   float64
	Max   float64
	Avg   float64
}

// summarizePingLatency aggregates records for one client or one task.
//
// Min is tracked with a flag rather than the old "min == 0 means unset" sentinel: with
// fractional milliseconds a genuine 0.42 (or a real 0) must not be mistaken for "no
// reading yet", and the sentinel silently dropped every sample below the smallest
// non-zero value seen.
func summarizePingLatency(records []models.PingRecord) pingLatencySummary {
	var summary pingLatencySummary
	var sum float64
	minSet := false
	for _, record := range records {
		summary.Total++
		if record.Value < 0 {
			summary.Loss++
			continue
		}
		value := pingRecordMilliseconds(record)
		summary.Valid++
		sum += value
		if !minSet || value < summary.Min {
			summary.Min = value
			minSet = true
		}
		if value > summary.Max {
			summary.Max = value
		}
	}
	if summary.Valid > 0 {
		summary.Avg = sum / float64(summary.Valid)
	}
	return summary
}
