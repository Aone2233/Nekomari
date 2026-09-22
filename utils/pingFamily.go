package utils

import (
	"net"
	"strings"
	"sync"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
)

// loadClientAddresses 一次性取出所有节点的地址族信息。
//
// 这里直接查表而不走 database/clients 包，是因为存在导入环：
//
//	utils -> database/clients -> database/tasks -> utils
//
// （database/tasks 需要 utils 来重载时间表）。绕开 clients 包即可断开这个环。
// 只需要 uuid / name / ipv4 / ipv6 四列，所以显式 Select，避免把整个 clients 表
// 拉进内存。
func loadClientAddresses() (byUUID map[string]models.Client, err error) {
	var rows []models.Client
	err = dbcore.GetDBInstance().
		Model(&models.Client{}).
		Select("uuid", "name", "ipv4", "ipv6").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	byUUID = make(map[string]models.Client, len(rows))
	for _, c := range rows {
		byUUID[c.UUID] = c
	}
	return byUUID, nil
}

// clientAddressLookup 在一次调度内复用 clients 的地址族信息。
//
// 为什么需要它：每个任务原本各查一次 clients 表，一次调度里 N 个任务就是 N 次
// 全表读取，而调度是秒级的。地址族信息在一次调度内不会变化，查一次即可。同一批
// 任务是并发执行的，所以用 sync.Once 收敛：只产生一次查询，而不是同时穿透。
type clientAddressLookup struct {
	once   sync.Once
	byUUID map[string]models.Client
	err    error
}

// newClientAddressLookup 开始一次查询生命周期（一次调度，或一次调用）。
func newClientAddressLookup() *clientAddressLookup {
	return &clientAddressLookup{}
}

// addresses 返回本生命周期内（缓存的）地址族信息。
//
// nil 接收者表示调用方没有共享范围：直接查一次，保持旧行为。
func (l *clientAddressLookup) addresses() (map[string]models.Client, error) {
	if l == nil {
		return loadClientAddresses()
	}
	l.once.Do(func() {
		l.byUUID, l.err = loadClientAddresses()
	})
	return l.byUUID, l.err
}

// targetAddressFamily 是延迟监测目标所要求的地址族。
//
// 为什么需要它：一个只有 IPv6 的节点去探一个 IPv4 字面量目标，永远不可能成功，
// 下发给它的结果就是一条恒定的 100% 丢包曲线。那不是网络故障，却和故障长得一模
// 一样——它会污染图表、触发误告警，还让人以为目标不稳定。这和协议不匹配（netcheck
// 处理的那类问题）是同一类错误，只是判断依据从协议换成了地址族。
type targetAddressFamily int

const (
	// familyAny 表示无法从目标本身判断地址族：域名（由 agent 自行解析，可能双栈）
	// 或空目标。此时不做筛选。
	familyAny targetAddressFamily = iota
	familyIPv4
	familyIPv6
)

// pingTargetHost 取出目标里的主机部分，兼容几种常见写法：
//
//	1.1.1.1:443            -> 1.1.1.1
//	[2001:db8::1]:443      -> 2001:db8::1
//	2001:db8::1            -> 2001:db8::1   （裸 IPv6，冒号不是端口分隔符）
//	www.example.com:443    -> www.example.com
//	www.example.com        -> www.example.com
func pingTargetHost(target string) string {
	target = strings.TrimSpace(target)
	if target == "" {
		return ""
	}

	// 带方括号的 IPv6，可能后面跟端口。
	if strings.HasPrefix(target, "[") {
		if end := strings.Index(target, "]"); end > 0 {
			return target[1:end]
		}
		return strings.Trim(target, "[]")
	}

	// 裸 IPv6：出现两个以上冒号时，冒号是地址的一部分而不是端口分隔符。
	if strings.Count(target, ":") > 1 {
		return target
	}

	// host:port
	if idx := strings.LastIndex(target, ":"); idx > 0 {
		return target[:idx]
	}
	return target
}

// pingTargetFamily 判断目标要求哪种地址族。
func pingTargetFamily(target string) targetAddressFamily {
	host := pingTargetHost(target)
	if host == "" {
		return familyAny
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return familyAny // 域名
	}
	// To4 对 "::ffff:1.51.3.134" 这类 IPv4-mapped 地址也返回非 nil，
	// 那实际上是 IPv4 目标，应该按 IPv4 判断。
	if ip.To4() != nil {
		return familyIPv4
	}
	return familyIPv6
}

// clientCanReachFamily 判断某个节点能否抵达指定地址族。
//
// 只有【确知】节点缺少所需地址族时才返回 false。两种不确定的情况都放行：
//   - familyAny：目标没给出可用信息
//   - 节点两个地址都为空：通常是刚接入、还没上报过，把正常节点排除掉比多下发一次
//     更糟
func clientCanReachFamily(client models.Client, family targetAddressFamily) bool {
	if family == familyAny {
		return true
	}

	hasV4 := strings.TrimSpace(client.IPv4) != ""
	hasV6 := strings.TrimSpace(client.IPv6) != ""

	if !hasV4 && !hasV6 {
		return true // 无地址信息，不做判断
	}

	if family == familyIPv4 {
		return hasV4
	}
	return hasV6
}

// filterClientsByTargetFamily 从候选节点里剔除无法抵达目标地址族的节点。
//
// 返回保留下来的 UUID。byUUID 来自一次批量查询，避免每个节点各查一次库。
func filterClientsByTargetFamily(candidates []string, byUUID map[string]models.Client, family targetAddressFamily) (keep []string, skipped []string) {
	if family == familyAny {
		return candidates, nil
	}

	keep = make([]string, 0, len(candidates))
	for _, uuid := range candidates {
		client, ok := byUUID[uuid]
		if !ok {
			// 节点已不存在：下发也没有连接可收，跳过。
			skipped = append(skipped, uuid)
			continue
		}
		if clientCanReachFamily(client, family) {
			keep = append(keep, uuid)
			continue
		}
		skipped = append(skipped, uuid)
	}
	return keep, skipped
}
