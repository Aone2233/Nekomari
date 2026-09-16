package jsonrpc

// admin.netcheck.go
// 面板侧的目标预检：在【面板服务器】上对一个目标做 ICMP/TCP 可达性探测，
// 用于在保存延迟监测任务前先确认「目标答应哪种协议」。
//
// 背景：监测任务的类型（icmp/tcp/http）是每个任务一个，选错了就会恒定 100% 丢包
// ——看起来像宕机，其实是协议不匹配。这个接口把 pkg/netcheck 的结论直接返回给面板，
// 让管理员在【配置阶段】就被拦住。
//
// 注意：探测在面板服务器上进行，结论反映的是【面板服务器】到目标的可达性。
// 目标「是否响应 ICMP」通常是目标自身的属性（各机器结论一致），
// 但「是否可达」可能因机器而异 —— 因此返回体里带 probed_from 字段明确来源。

import (
	"context"
	"strings"
	"time"

	"github.com/Aone2233/nekomari/pkg/netcheck"
	"github.com/Aone2233/nekomari/pkg/rpc"
)

const (
	netcheckMaxPorts   = 16               // 单次最多测这么多端口，避免被当作端口扫描器
	netcheckMaxCount   = 10               // ICMP 次数上限
	netcheckMaxTimeout = 5 * time.Second  // 单次探测超时上限
)

func init() {
	RegisterWithGroupAndMeta("netcheck", rpc.RoleAdmin, adminNetcheck, &rpc.MethodMeta{
		Name:    "admin:netcheck",
		Summary: "Probe a target from the panel server to check which probe type it supports (ICMP/TCP)",
		Description: "Runs a DNS + ICMP + TCP reachability probe and returns a verdict such as " +
			"'icmp_only', 'tcp_only' or 'both_ok', plus a configuration advice string. " +
			"Use this before saving a ping task to avoid picking a protocol the target does not answer. " +
			"The probe runs on the panel server, so it reflects that vantage point.",
		Returns: "{ report: netcheck.Report, probed_from: string }",
	})
}

func adminNetcheck(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		Target    string `json:"target"`
		Ports     []int  `json:"ports"`
		Count     int    `json:"count"`
		TimeoutMs int    `json:"timeout_ms"`
		SkipICMP  bool   `json:"skip_icmp"`
	}
	if err := req.BindParams(&params); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "invalid params", nil)
	}

	target := strings.TrimSpace(params.Target)
	if target == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "target is required", nil)
	}
	if len(target) > 255 {
		return nil, rpc.MakeError(rpc.InvalidParams, "target too long", nil)
	}

	// 端口白名单式收敛：只保留合法端口，并限制数量
	var ports []int
	seen := map[int]bool{}
	for _, p := range params.Ports {
		if p > 0 && p <= 65535 && !seen[p] {
			seen[p] = true
			ports = append(ports, p)
		}
		if len(ports) >= netcheckMaxPorts {
			break
		}
	}

	count := params.Count
	if count <= 0 {
		count = 3
	} else if count > netcheckMaxCount {
		count = netcheckMaxCount
	}

	timeout := time.Duration(params.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 3 * time.Second
	} else if timeout > netcheckMaxTimeout {
		timeout = netcheckMaxTimeout
	}

	rep := netcheck.Run(target, netcheck.Options{
		Ports:    ports,
		Count:    count,
		Timeout:  timeout,
		SkipICMP: params.SkipICMP,
	})

	return map[string]any{
		"report": rep,
		// 明确告诉调用方这个结论来自哪里，避免被误读成「某台 agent 的结论」
		"probed_from": "server",
	}, nil
}