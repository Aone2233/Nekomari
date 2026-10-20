package v2

import (
	"encoding/json"
	"time"
)

const (
	Version               = "2.0"
	MethodAgentReport     = "agent.report"
	MethodAgentBasicInfo  = "agent.basicInfo"
	MethodAgentPingResult = "agent.pingResult"
	MethodAgentTaskResult = "agent.taskResult"
	MethodAgentExec       = "agent.exec"
	MethodAgentPing       = "agent.ping"
	MethodAgentMessage    = "agent.message"
	MethodAgentEvent      = "agent.event"
	MethodAgentTerminal   = "agent.terminal.request"
	MethodAgentPull       = "agent.pull"
	MethodAgentFile       = "agent.file"
	MethodAgentFileResult = "agent.file.result"
	MethodAgentUnlock     = "agent.unlock"
	// MethodAgentUpdate 让面板指定这个节点要运行的版本（roadmap H6）。
	//
	// 走事件通道而不是配置：agent 的配置是部署级的（环境变量/systemd），面板没有运行时通道能改它，
	// 而事件队列是既有的面板→agent 指令路径 —— 能立刻送达在线节点，也能留在队列里等离线节点回来。
	MethodAgentUpdate = "agent.update"
)

type Request struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
	ID      interface{} `json:"id,omitempty"`
}

type Response struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id,omitempty"`
	Result  interface{} `json:"result,omitempty"`
	Error   *RPCError   `json:"error,omitempty"`
}

type RPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

type TaskResultParams struct {
	TaskID     string    `json:"task_id"`
	Result     string    `json:"result"`
	ExitCode   int       `json:"exit_code"`
	FinishedAt time.Time `json:"finished_at"`
}

// UpdateParams 是一次指定版本升级的请求，或一次取消。
//
// Target 为空表示取消指定、回到跟随最新发布；这正是回滚的操作方式 —— 把一个更早的版本写进去，
// 或者清空让它重新跟随。
type UpdateParams struct {
	Target string `json:"target"`
}

type Event struct {
	ID        string      `json:"id"`
	Method    string      `json:"method"`
	Params    interface{} `json:"params,omitempty"`
	CreatedAt time.Time   `json:"created_at"`
	ExpiresAt time.Time   `json:"expires_at"`
}

type EventResult struct {
	Status string  `json:"status,omitempty"`
	Events []Event `json:"events,omitempty"`
}

// FileOperation is metadata-only. File contents travel through the dedicated
// HTTP transfer endpoint rather than through JSON-RPC.
type FileOperation struct {
	UUID      string                 `json:"uuid"`
	RequestID string                 `json:"request_id"`
	Op        string                 `json:"op"`
	Args      map[string]interface{} `json:"args,omitempty"`
}

type FileResult struct {
	UUID      string          `json:"uuid"`
	RequestID string          `json:"request_id"`
	OK        bool            `json:"ok"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
}

func NewNotification(method string, params interface{}) []byte {
	payload, _ := json.Marshal(Request{JSONRPC: Version, Method: method, Params: params})
	return payload
}

func NewRequest(id interface{}, method string, params interface{}) []byte {
	payload, _ := json.Marshal(Request{JSONRPC: Version, Method: method, Params: params, ID: id})
	return payload
}

func BuildReportPayload(report []byte) []byte {
	return NewNotification(MethodAgentReport, reportParams{Report: json.RawMessage(report)})
}

func BuildReportRequest(id interface{}, report []byte, ackEventIDs []string) []byte {
	return NewRequest(id, MethodAgentReport, reportParams{Report: json.RawMessage(report), AckEventIDs: ackEventIDs})
}

func BuildBasicInfoPayload(info map[string]interface{}) []byte {
	return NewNotification(MethodAgentBasicInfo, map[string]interface{}{"info": info})
}

// BuildUnlockPayload 把一次解锁探测的结果包成通知。
// report 由 agent/unlock 构造；协议层只负责包外壳，不关心它的字段。
func BuildUnlockPayload(report interface{}) Request {
	return Request{JSONRPC: Version, Method: MethodAgentUnlock, Params: report}
}

type reportParams struct {
	Report      json.RawMessage `json:"report"`
	AckEventIDs []string        `json:"ack_event_ids,omitempty"`
}

func BuildPingResultPayload(taskID uint, pingType string, value int, finishedAt time.Time) interface{} {
	return BuildPingResultPayloadWithRoleAndFamily(taskID, pingType, "", "", value, finishedAt)
}

// BuildPingResultPayloadWithRole 在基础负载上附加 role。
// role 为空表示测的是主目标；"reference" 表示测的是参考点（路径归因用）。
// 空 role 不写入字段，保持与旧服务端/旧数据的兼容。
func BuildPingResultPayloadWithRole(taskID uint, pingType, role string, value int, finishedAt time.Time) interface{} {
	return BuildPingResultPayloadWithRoleAndFamily(taskID, pingType, role, "", value, finishedAt)
}

// BuildPingResultPayloadWithRoleAndFamily 在基础负载上附加 role 与 family。
//
// family 是本次测量【实际使用】的地址族（"ipv4"/"ipv6"）。为什么需要它：目标是
// 域名时由各节点自行解析，双栈域名在不同节点上可能落到不同族，两条路径的延迟与
// 丢包不可比。面板靠这个字段把它们拆成两条序列，而不是混成一条曲线。
//
// 空 family 不写入字段：旧服务端会忽略它，旧 agent 也不会发它，两侧都保持兼容。
//
// 本函数保持旧签名与旧线缆形状（没有 value_us）：value 是毫秒，这是旧面板唯一认识的
// 单位。真实测量路径请用 BuildPingResultPayloadWithRoleFamilyAndMicros 附带微秒值。
func BuildPingResultPayloadWithRoleAndFamily(taskID uint, pingType, role, family string, value int, finishedAt time.Time) interface{} {
	return buildPingResultPayload(taskID, pingType, role, family, value, nil, finishedAt)
}

// BuildPingResultPayloadWithRoleFamilyAndMicros 在带 role/family 的负载上再附加微秒精度。
//
// ## 为什么同时发两个字段
//
// 面板把 value 存成 ping 记录的毫秒值（Value<0 记为丢包），历史上只能到毫秒。亚毫秒的
// 目标（同机房、loopback）在毫秒单位下不是 0 就是 1，图表上变成阶梯；而 ICMP 的平均
// RTT 本来就有微秒分辨率。所以：
//
//   - value 仍是毫秒（微秒值 / 1000，截断），旧面板照旧工作；
//   - value_us 是测量到的微秒值，新面板可以用它画出真实分辨率；失败时也是 -1，
//     与 value 的丢包哨兵一致，避免"字段缺失"和"未知"两种含义混在一起；
//   - 旧面板直接忽略不认识的 value_us（Go 的 json.Unmarshal 会跳过未知字段）。
//
// 本函数是 agent 真实测量路径唯一该用的构建函数。
func BuildPingResultPayloadWithRoleFamilyAndMicros(taskID uint, pingType, role, family string, value int, valueUS int64, finishedAt time.Time) interface{} {
	return buildPingResultPayload(taskID, pingType, role, family, value, &valueUS, finishedAt)
}

// buildPingResultPayload 组装 ping 结果通知；valueUS 为 nil 时不写 value_us（旧形状）。
func buildPingResultPayload(taskID uint, pingType, role, family string, value int, valueUS *int64, finishedAt time.Time) interface{} {
	params := map[string]interface{}{
		"task_id":     taskID,
		"ping_type":   pingType,
		"value":       value,
		"finished_at": finishedAt.Format(time.RFC3339Nano),
	}
	if role != "" {
		params["role"] = role
	}
	if family != "" {
		params["family"] = family
	}
	if valueUS != nil {
		params["value_us"] = *valueUS
	}
	return Request{
		JSONRPC: Version,
		Method:  MethodAgentPingResult,
		Params:  params,
	}
}

func BindParams(raw interface{}, target interface{}) error {
	b, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, target)
}

func BindResult(raw interface{}, target interface{}) error {
	return BindParams(raw, target)
}
