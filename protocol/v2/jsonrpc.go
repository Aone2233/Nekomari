package v2

import (
	"bytes"
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
)

// UnlockParams 是 agent.unlock 的负载：探针从自己的出口测得的解锁结论。
//
// 为什么由探针测而不是服务端测：解锁取决于【发起请求的那个 IP】，服务端在别的机房，
// 只能测到它自己。出口地址随结果一起上来，因为它不总是节点自己的地址 —— 本机群里
// 有主机是经由另一台节点出网的。
type UnlockParams struct {
	EgressIP     string       `json:"egress_ip,omitempty"`
	EgressRegion string       `json:"egress_region,omitempty"`
	ProbedAt     time.Time    `json:"probed_at"`
	Results      []UnlockItem `json:"results"`
}

// UnlockItem 是单个服务的结论。字段与 agent/unlock.Result 一一对应；服务端与探针是
// 两个独立模块，所以这里重新声明而不是共享类型 —— 线上形状就是这个结构体。
type UnlockItem struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
	Region string `json:"region,omitempty"`
	Basis  string `json:"basis"`
	Detail string `json:"detail,omitempty"`
}

type Request struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
	ID      any    `json:"id,omitempty"`
}

// RawRequest is the wire shape of one inbound JSON-RPC request whose params
// have not been decoded yet.
//
// The panel has to hand the params to a method-specific struct. Decoding them
// through Request.Params (an untyped `any`) builds a map[string]any for the
// whole payload — every number, nested object and string — only to marshal it
// back to JSON and unmarshal it into the typed struct. RawRequest keeps the
// params as the bytes that arrived, so the payload is decoded exactly once, by
// the code that knows what it is.
//
// Request itself is unchanged: it is the shape this protocol uses when the
// panel *builds* a message (web/agent, web/rpc/jsonrpc), where the params are
// Go values that still have to be marshalled onto the wire.
type RawRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      any             `json:"id,omitempty"`
}

// DecodeParams unmarshals the request params into target.
//
// Params are optional in JSON-RPC, and an omitted or explicitly null value
// leaves target untouched and reports no error, so method defaults keep
// applying exactly as they did when the params travelled through an untyped
// map.
func (r RawRequest) DecodeParams(target any) error {
	params := bytes.TrimSpace(r.Params)
	if len(params) == 0 || bytes.Equal(params, []byte("null")) {
		return nil
	}
	return json.Unmarshal(params, target)
}

type Response struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *RPCError `json:"error,omitempty"`
}

type Event struct {
	ID        string    `json:"id"`
	Method    string    `json:"method"`
	Params    any       `json:"params,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type ReportParams struct {
	Report      Report   `json:"report"`
	AckEventIDs []string `json:"ack_event_ids,omitempty"`
}

type Message struct {
	Type      string `json:"type"`
	Content   string `json:"content"`
	Sender    string `json:"sender"`
	Timestamp int64  `json:"timestamp"`
}

type IPAddress struct {
	Ipv4 string `json:"ipv4"`
	Ipv6 string `json:"ipv6"`
}

type Report struct {
	UUID        string            `json:"uuid,omitempty"`
	CPU         CPUReport         `json:"cpu"`
	Ram         RamReport         `json:"ram"`
	Swap        RamReport         `json:"swap"`
	Load        LoadReport        `json:"load"`
	Disk        DiskReport        `json:"disk"`
	Network     NetworkReport     `json:"network"`
	Connections ConnectionsReport `json:"connections"`
	GPU         *GPUDetailReport  `json:"gpu,omitempty"`
	Uptime      int64             `json:"uptime"`
	Process     int               `json:"process"`
	// Backup 是可选的备份新鲜度上报。agent 未配置备份状态文件时为 nil，
	// 因此不影响未使用该功能的部署，也不会写入任何 point。
	Backup    *BackupReport `json:"backup,omitempty"`
	Message   string        `json:"message"`
	Method    string        `json:"method,omitempty"`
	UpdatedAt time.Time     `json:"updated_at"`
}

// BoundedPayload limits retained strings and GPU cardinality before queueing.
func (r Report) BoundedPayload() bool {
	size := len(r.UUID) + len(r.CPU.Name) + len(r.CPU.Arch) + len(r.Message) + len(r.Method)
	if r.Backup != nil {
		size += len(r.Backup.Message)
	}
	if r.GPU != nil {
		if len(r.GPU.DetailedInfo) > 32 {
			return false
		}
		for _, gpu := range r.GPU.DetailedInfo {
			size += len(gpu.Name)
		}
	}
	return size <= 4096
}

// BackupReport 是备份新鲜度上报。
//
// 动机：备份「有没有在跑」和「最后一次成功是什么时候」往往没有人看，
// 直到真需要恢复时才发现已经坏了几个月。把最后成功时间变成可监控的指标，
// 就能用与主机指标相同的方式设阈值/基线告警。
type BackupReport struct {
	// AgeSeconds 距最后一次【成功】备份的秒数；-1 表示无法确定
	//（状态文件缺失或不可解析），此时 Ok 必为 0。
	AgeSeconds int64 `json:"age_seconds"`
	// Ok 为 1 表示最近一次备份成功，0 表示失败或状态未知。
	Ok int `json:"ok"`
	// Message 是可选的人类可读说明（例如失败原因），便于排查。
	Message string `json:"message,omitempty"`
}

type CPUReport struct {
	Name  string  `json:"name,omitempty"`
	Cores int     `json:"cores,omitempty"`
	Arch  string  `json:"arch,omitempty"`
	Usage float64 `json:"usage,omitempty"`
}

type GPUDetailReport struct {
	Count        int             `json:"count"`
	AverageUsage float64         `json:"average_usage"`
	DetailedInfo []GPUDeviceInfo `json:"detailed_info"`
}

type GPUDeviceInfo struct {
	Name        string  `json:"name"`
	MemoryTotal int64   `json:"memory_total"`
	MemoryUsed  int64   `json:"memory_used"`
	Utilization float64 `json:"utilization"`
	Temperature int     `json:"temperature"`
}

type RamReport struct {
	Total int64 `json:"total"`
	Used  int64 `json:"used"`
}

type LoadReport struct {
	Load1  float64 `json:"load1"`
	Load5  float64 `json:"load5"`
	Load15 float64 `json:"load15"`
}

type DiskReport struct {
	Total int64 `json:"total"`
	Used  int64 `json:"used"`
}

type NetworkReport struct {
	Up        int64 `json:"up"`
	Down      int64 `json:"down"`
	TotalUp   int64 `json:"totalUp"`
	TotalDown int64 `json:"totalDown"`
}

type ConnectionsReport struct {
	TCP int `json:"tcp"`
	UDP int `json:"udp"`
}

type BasicInfoParams struct {
	Info map[string]interface{} `json:"info"`
}

type PingResultParams struct {
	TaskID     uint      `json:"task_id"`
	PingType   string    `json:"ping_type"`
	Value      int       `json:"value"`
	FinishedAt time.Time `json:"finished_at"`
	// Role 为空表示主目标；"reference" 表示这是参考点的结果。
	Role string `json:"role,omitempty"`
}

type TaskResultParams struct {
	TaskID     string    `json:"task_id"`
	Result     string    `json:"result"`
	ExitCode   int       `json:"exit_code"`
	FinishedAt time.Time `json:"finished_at"`
}

type PullParams struct {
	Capabilities []string `json:"capabilities,omitempty"`
	AckEventIDs  []string `json:"ack_event_ids,omitempty"`
	LastEventID  string   `json:"last_event_id,omitempty"`
}

type ExecParams struct {
	TaskID  string `json:"task_id"`
	Command string `json:"command"`
}

type PingParams struct {
	TaskID uint   `json:"ping_task_id"`
	Type   string `json:"ping_type"`
	Target string `json:"ping_target"`
	// Reference 非空时，agent 会在同一周期额外探测该目标，
	// 并以 role="reference" 上报，用于主机 vs 网关的路径归因。
	Reference string `json:"ping_reference,omitempty"`
}

type MessageParams struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type EventParams struct {
	Type string `json:"type"`
	Data any    `json:"data,omitempty"`
}

type TerminalRequestParams struct {
	RequestID string `json:"request_id"`
}

// FileOperation is metadata-only. File contents travel through the dedicated
// HTTP transfer endpoint rather than through JSON-RPC.
type FileOperation struct {
	UUID      string         `json:"uuid"`
	RequestID string         `json:"request_id"`
	Op        string         `json:"op"`
	Args      map[string]any `json:"args,omitempty"`
}

type FileResult struct {
	UUID      string          `json:"uuid"`
	RequestID string          `json:"request_id"`
	OK        bool            `json:"ok"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
}

func Success(id any, result any) Response {
	return Response{JSONRPC: Version, ID: id, Result: result}
}

func Error(id any, code int, message string, data any) Response {
	return Response{JSONRPC: Version, ID: id, Error: &RPCError{Code: code, Message: message, Data: data}}
}
