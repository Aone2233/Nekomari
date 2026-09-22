package utils

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/scheduler"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
	logger "github.com/Aone2233/nekomari/utils/log"
	agent_runtime "github.com/Aone2233/nekomari/web/agent"
)

// PingTaskManager 管理定时器和任务
type PingTaskManager struct {
	mu    sync.Mutex
	tasks map[int][]models.PingTask
}

var manager = &PingTaskManager{
	tasks: make(map[int][]models.PingTask),
}

// Reload 重载时间表
func (m *PingTaskManager) Reload(pingTasks []models.PingTask) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	scheduler.RemovePrefix("ping:")
	m.tasks = make(map[int][]models.PingTask)

	// 按Interval分组任务
	taskGroups := make(map[int][]models.PingTask)
	for _, task := range pingTasks {
		if task.Interval <= 0 {
			continue
		}
		taskGroups[task.Interval] = append(taskGroups[task.Interval], task)
	}

	// 为每个唯一的Interval创建协程
	for interval, tasks := range taskGroups {
		interval := interval
		tasks := append([]models.PingTask(nil), tasks...)
		m.tasks[interval] = tasks
		if err := scheduler.AddContextFunc(fmt.Sprintf("ping:%d", interval), scheduler.Every(time.Duration(interval)*time.Second), false, func(ctx context.Context) {
			// 一次调度共用一份地址族信息：地址族是这一批任务的共同输入，逐任务
			// 查询会让 N 个任务产生 N 次 clients 全表读取。
			addresses := newClientAddressLookup()
			for _, task := range tasks {
				go executePingTask(ctx, task, addresses)
			}
		}); err != nil {
			return err
		}
	}

	// 配置加载时汇报一次因地址族被跳过的节点。放在这里而不是每次调度里：调度是
	// 秒级的，逐次打印会刷满日志，而这是配置属性，配置不变就不会变。
	for _, note := range describeFamilySkips(pingTasks) {
		logger.Infof("pingschedule", "[family-skip] %s", note)
	}

	return nil
}

// executePingTask 执行单个PingTask
//
// addresses 是本次调度共享的地址族查询；nil 表示自己查一次。
func executePingTask(ctx context.Context, task models.PingTask, addresses *clientAddressLookup) {
	for _, clientUUID := range targetPingClientUUIDs(task, addresses) {
		select {
		case <-ctx.Done():
			// Context was canceled, stop sending pings.
			return
		default:
			// Context is still active, continue.
		}

		agent_runtime.DispatchPing(clientUUID, v2.PingParams{TaskID: task.Id, Type: task.Type, Target: task.Target, Reference: task.Reference})
	}
}

// targetPingClientUUIDs 根据任务配置计算本次调度需要下发的在线服务器列表。
//
// 会剔除【地址族不匹配】的节点：目标是 IPv4 字面量时，纯 IPv6 节点永远不可能成功，
// 下发只会换来一条恒定的 100% 丢包曲线，看起来像目标故障，实际是结构上做不到。
// 域名目标不筛选——由 agent 自行解析，可能双栈。
//
// addresses 由调用方在一次调度内共享（见 clientAddressLookup）；nil 表示本次调用
// 自己查一次库。
func targetPingClientUUIDs(task models.PingTask, addresses *clientAddressLookup) []string {
	family := pingTargetFamily(task.Target)
	if family == familyAny {
		return task.Clients
	}

	// 共享一次批量查询，而不是每个节点查一次库。
	byUUID, err := addresses.addresses()
	if err != nil {
		// 拿不到地址就不筛选：多下发一次远好过把正常节点排除掉。
		return task.Clients
	}

	keep, _ := filterClientsByTargetFamily(task.Clients, byUUID, family)
	return keep
}

// describeFamilySkips 在重载时间表时汇报被跳过的节点，只在配置变化时打印一次。
//
// 放在 Reload 而不是每次调度里：调度是秒级的，逐次打印会把日志刷满；而"哪些节点
// 因为地址族被跳过"是配置属性，配置不变就不会变。
func describeFamilySkips(pingTasks []models.PingTask) []string {
	// 跳过判断和下面的名字映射共用一份地址族信息：原来这里会查两次 clients。
	addresses := newClientAddressLookup()
	byTask, err := pingTasksFamilySkips(pingTasks, addresses)
	if err != nil {
		return nil
	}
	byUUID, _ := addresses.addresses()
	nameOf := make(map[string]string, len(byUUID))
	for uuid, c := range byUUID {
		nameOf[uuid] = c.Name
	}

	var notes []string
	for _, task := range pingTasks {
		skipped := byTask[task.Id]
		if len(skipped) == 0 {
			continue
		}
		names := make([]string, 0, len(skipped))
		for _, uuid := range skipped {
			if n, ok := nameOf[uuid]; ok {
				names = append(names, n)
			} else {
				names = append(names, uuid[:min(8, len(uuid))]+" (removed)")
			}
		}
		fam := "IPv4"
		if pingTargetFamily(task.Target) == familyIPv6 {
			fam = "IPv6"
		}
		notes = append(notes, fmt.Sprintf("task %d (%s, %s target %s): skipping %s",
			task.Id, task.Name, fam, task.Target, strings.Join(names, ", ")))
	}
	return notes
}

// PingTasksFamilySkips 返回每个任务里会因地址族不匹配而被跳过的节点 UUID。
//
// 供管理界面标注：让用户看到"这个节点被跳过了"，而不是只发现某条曲线没有数据。
// 与实际调度共用 filterClientsByTargetFamily，所以界面显示的和真正下发的不会分叉。
func PingTasksFamilySkips(pingTasks []models.PingTask) (map[uint][]string, error) {
	return pingTasksFamilySkips(pingTasks, newClientAddressLookup())
}

func pingTasksFamilySkips(pingTasks []models.PingTask, addresses *clientAddressLookup) (map[uint][]string, error) {
	byUUID, err := addresses.addresses()
	if err != nil {
		return nil, err
	}

	out := make(map[uint][]string)
	for _, task := range pingTasks {
		family := pingTargetFamily(task.Target)
		if family == familyAny {
			continue // 域名目标不筛选
		}
		_, skipped := filterClientsByTargetFamily(task.Clients, byUUID, family)
		if len(skipped) > 0 {
			out[task.Id] = skipped
		}
	}
	return out, nil
}

// ReloadPingSchedule 加载或重载时间表
func ReloadPingSchedule(pingTasks []models.PingTask) error {
	return manager.Reload(pingTasks)
}
