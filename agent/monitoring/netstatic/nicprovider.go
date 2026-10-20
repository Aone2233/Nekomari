package netstatic

import "log"

/*
网卡白名单刷新

白名单原先只是启动时的快照：cmd/root.go 取一次当前网卡，交给 SetNewConfig，此后不再更新。于是
之后才出现的网卡（docker 网桥、热插拔的物理网卡、启动时还没 up 的接口）永远不会被统计 —— 而且是
静默的，因为"不在白名单里"和"这张卡没有流量"在账本上看起来完全一样。

provider 把"启动快照"变成"每次采样前问一次"，由调用方注入（cmd/root.go 传
monitoring.InterfaceList）。本包不直接导入系统枚举器，所以这里既没有平台分支，也可以在测试里注入
任意列表与错误。
*/

var (
	// nicProvider 由 SetNICProvider 注入；nil 表示不做刷新（保持原有快照行为）。
	nicProvider func() ([]string, error)
	// nicProviderFailureLogged 让"刷新失败"每轮只记一条日志：采样是 30 秒级的，枚举网卡在
	// 极端情况下会持续失败，不限流就是每 30 秒一行。
	nicProviderFailureLogged bool
)

// SetNICProvider 安装每次采样前用来询问"当前该监控哪些网卡"的回调；传 nil 关闭刷新。
//
// 回调返回空列表表示"本次没有可用结果"，此时保留旧列表而不是退回"监控所有网卡"：后者会把
// 回环与容器网卡重新算进账本，正是本功能要避免的事情。
func SetNICProvider(provider func() ([]string, error)) {
	mu.Lock()
	nicProvider = provider
	nicProviderFailureLogged = false
	mu.Unlock()
}

// refreshNicsLocked 询问 provider 当前的白名单，变化时写回配置。调用方必须持有 mu。
func refreshNicsLocked() {
	if nicProvider == nil {
		return
	}
	nics, err := nicProvider()
	if err != nil {
		if !nicProviderFailureLogged {
			nicProviderFailureLogged = true
			log.Println("netstatic: cannot refresh the monitored interface list, keeping the previous one:", err)
		}
		return
	}
	nicProviderFailureLogged = false
	if len(nics) == 0 {
		// 过滤之后一张网卡都不剩。把它当成"监控所有网卡"会让回环/容器网卡重新进入账本，
		// 所以保留上一份白名单。
		return
	}
	if sameNicSet(config.Nics, nics) {
		return
	}
	// 变化才写 store：白名单是少数几次事件，但比较本身每 30 秒一次，而落盘会把 31 天的全量
	// 数据重新序列化一遍。
	config.Nics = append([]string(nil), nics...)
	store.Config.Nics = append([]string(nil), nics...)
	storeDirty = true
	pruneUnmonitoredLocked()
}

// sameNicSet 判断两份白名单是否等价（顺序无关）。重排一次网卡枚举顺序不该触发一次落盘。
func sameNicSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]struct{}, len(a))
	for _, name := range a {
		seen[name] = struct{}{}
	}
	for _, name := range b {
		if _, ok := seen[name]; !ok {
			return false
		}
	}
	return true
}

// pruneUnmonitoredLocked 丢掉不在白名单内的缓存与基线，避免已经不再监控的网卡继续积累数据，
// 也避免它们重新出现在白名单里时用一个过期的基线去算增量。调用方必须持有 mu。
func pruneUnmonitoredLocked() {
	if len(config.Nics) == 0 {
		return
	}
	allowed := make(map[string]struct{}, len(config.Nics))
	for _, name := range config.Nics {
		allowed[name] = struct{}{}
	}
	for name := range lastCounters {
		if _, ok := allowed[name]; !ok {
			delete(lastCounters, name)
		}
	}
	for name := range staticCache {
		if _, ok := allowed[name]; !ok {
			delete(staticCache, name)
		}
	}
}
