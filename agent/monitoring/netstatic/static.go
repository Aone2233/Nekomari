package netstatic

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	gnet "github.com/shirou/gopsutil/v4/net"
)

/*
统计每个网卡的流量情况，保存最近DataPreserveDay天的数据，每DetectInterval秒采集一次

默认保存到当前目录下的net_static.json文件中
net_static.json 中有字段 config，表示当前的配置，如果没有则使用默认值
unix时间戳，单位秒

所有操作都尽可能在内存中完成，避免频繁的IO操作

只有在启动、停止和保存时，才会进行文件的读写操作
*/
// legacyDetectInterval 是旧版本的 DefaultDetectInterval。存量 net_static.json 的 config 里
// 记着 detect_interval=2，agent 从不下发这个字段（cmd/root.go 只下发 Nics），所以它只是当时
// 默认值的残留；见 migrateConfig。
const legacyDetectInterval = 2.0

// netStaticConfigVersion 当前配置代次。没有 config_version 字段的文件属于第 1 代。
const netStaticConfigVersion = 2

var (
	DefaultDataPreserveDay = 31.0 // in days，保存最近多少天的数据，过期数据会被删除
	// in seconds，采集间隔。落盘时 flushCacheLocked 会把 SaveInterval 窗口内的样本合并成一个桶，
	// 所以比 SaveInterval 细得多的采集间隔换不到任何分辨率，只换来 /proc/net/dev 的读取次数：
	// 2 秒 = 43200 次/天/节点，30 秒 = 2880 次/天/节点。取 30 秒（600 秒桶的 1/20），
	// 一个桶里因计数器重置最多丢一个采集间隔的流量（≤5%），并留出 20 个样本/桶的余量，
	// 避免采集与保存 tick 相邻时把桶拉空。
	DefaultDetectInterval = 30.0
	DefaultSaveInterval   = 60.0 * 10 // in seconds，写入到磁盘的间隔，避免大量IO操作，保存到文件的间隔也是这个值，而不是DetectInterval
	// DefaultRewriteInterval in seconds，两次整体重写 net_static.json 之间的最小间隔。
	// saveToFileLocked 每次都要把 31 天的全量数据重新序列化一遍（OC424 实测单网卡 216644 字节），
	// 按 SaveInterval 每 600 秒写一次是 144 次/天/节点；限流到 1800 秒后是 48 次/天/节点。
	// 代价是最多 3 个桶（30 分钟）只存在于内存，进程被 SIGKILL 时丢失；报表路径读的是内存，
	// 不受影响，Stop() 仍然无条件落盘。
	DefaultRewriteInterval = 60.0 * 30
	SaveFilePath           = "./net_static.json"
)

var (
	staticCache map[string][]TrafficData // key: interface name，统计缓存，当前没有被保存到文件中的，间隔DetectInterval，触发保存时，合并所有的tx/rx数据，以SaveInterval，写入到文件中，随后清空缓存
	config      NetStaticConfig
)

// NetStatic 网卡流量统计数据
type NetStatic struct {
	Interfaces map[string][]TrafficData `json:"interfaces"` // key: interface name
	Config     NetStaticConfig          `json:"config"`
	// LastCounters 记录上一次采集到的累计字节数，随账本一起落盘。
	//
	// 它为什么必须落盘：增量是 `当前 - 上次`，而"上次"原先只存在内存里。进程一重启，
	// 基线就没了，重启后第一次采样拿到的是一个**已经很高的累计值**，而基线是零 —— 于是
	// 整个累计量被当成一个采样间隔的增量。现场证据：一台节点在 agent 启动的那一分钟上报了
	// 单点 59.5 GB（真实速率约 7 GB/天，接口自开机累计仅 187 MB），把面板"今日流量"
	// 从 7 GB 抬到 160 GB。sampleOnceLocked 现在还会核对数值是否可信（见
	// plausibleDeltaBytes），这条字段是让重启后仍有基线可用，而不是只能丢弃一个间隔。
	LastCounters map[string]CounterSample `json:"last_counters,omitempty"`
}

// CounterSample 是一次采集到的累计值，以及读到它的时刻（unix 秒）。
// 时刻是判断"增量是否可信"的依据：增量的上限随时间线性增长。
type CounterSample struct {
	Tx uint64 `json:"tx"`
	Rx uint64 `json:"rx"`
	At uint64 `json:"at"`
}

type NetStaticConfig struct {
	DataPreserveDay float64  `json:"data_preserve_day"`        // in days，保存最近多少天的数据，过期数据会被删除
	DetectInterval  float64  `json:"detect_interval"`          // in seconds，采集间隔
	SaveInterval    float64  `json:"save_interval"`            // in seconds，写入到磁盘的间隔，避免大量IO操作
	Nics            []string `json:"nics"`                     // 仅监控指定的网卡名称列表，空表示监控所有网卡
	ConfigVersion   int      `json:"config_version,omitempty"` // 这份配置是哪一代默认值写出来的，用于迁移
}

type TrafficData struct {
	Timestamp uint64 `json:"timestamp"`
	Tx        uint64 `json:"tx"` // 第n与n-1次采集的差值
	Rx        uint64 `json:"rx"` // 第n与n-1次采集的差值
}

var (
	mu           sync.RWMutex
	running      bool
	detectTicker *time.Ticker
	saveTicker   *time.Ticker
	stopCh       chan struct{}

	// 内存持久区（与文件内容一致，但仅在启动、保存、停止时与磁盘交互）
	store NetStatic

	// storeDirty 表示内存中的 store 自上次成功落盘后是否变过（新桶、过期清理、整体替换等）。
	// 没变过就没必要把整个文件重写一遍。
	storeDirty bool

	// lastWriteUnix 上次成功落盘的时间，用于限流整体重写（见 DefaultRewriteInterval）
	lastWriteUnix uint64

	// 上次采集到的累计字节数（用于计算 delta）。初值从账本恢复，见
	// NetStatic.LastCounters：重启后丢基线正是把累计值当增量上报的成因。
	lastCounters = map[string]CounterSample{}
)

func nowUnix() uint64 { return uint64(time.Now().Unix()) }

// isNicAllowed 判断网卡是否在监控白名单内；当未配置白名单（空切片或nil）时，允许所有网卡
func isNicAllowed(name string) bool {
	if len(config.Nics) == 0 {
		return true
	}
	for _, n := range config.Nics {
		if n == name {
			return true
		}
	}
	return false
}

func ensureInitLocked() {
	if store.Interfaces == nil {
		store.Interfaces = make(map[string][]TrafficData)
	}
	if staticCache == nil {
		staticCache = make(map[string][]TrafficData)
	}
	if config.DataPreserveDay == 0 {
		config.DataPreserveDay = DefaultDataPreserveDay
	}
	if config.DetectInterval == 0 {
		config.DetectInterval = DefaultDetectInterval
	}
	if config.SaveInterval == 0 {
		config.SaveInterval = DefaultSaveInterval
	}
}

func loadFromFileLocked() error {
	// 读完盘之后内存状态至少要落盘一次：配置可能被迁移过（见 migrateConfig），
	// 坏文件也会在这一轮被一份干净的文件替换掉。
	storeDirty = true

	// 不存在则用默认配置
	f, err := os.Open(SaveFilePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			ensureInitLocked()
			store.Config = configOrDefault(config)
			return nil
		}
		return err
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		ensureInitLocked()
		store.Config = configOrDefault(config)
		return nil
	}
	var ns NetStatic
	if err := json.Unmarshal(data, &ns); err != nil {
		// 文件损坏则不阻塞使用，采用默认并备份坏文件
		_ = os.Rename(SaveFilePath, SaveFilePath+".bak")
		ensureInitLocked()
		store.Config = configOrDefault(config)
		return nil
	}
	store = ns
	config = configOrDefault(ns.Config)
	ensureInitLocked()
	// 恢复上次采集到的累计值。这就是"重启后仍有基线"的那一步：没有它，重启后第一次
	// 采样会把整个累计量当成一个间隔的增量（见 NetStatic.LastCounters 的说明）。
	restoreLastCountersLocked()
	// 启动时清理过期数据
	purgeExpiredLocked()
	return nil
}

// restoreLastCountersLocked 把账本里的基线搬回内存。
//
// At 缺失（旧账本没有这个字段）时保留 0，让 plausibleDelta 以"没有流逝时间"为由拒绝
// 第一次增量 —— 宁可丢一个间隔，也不能拿一个来路不明的基线去算差值。
//
// lastWriteUnix 归零是这一步的一半，不是附带效果：启动时落盘的文件写于**基线还空着**
// 的时候，而 shouldRewriteFileLocked 会把那次落盘当成"刚写过"，于是真正的基线要等一个
// 完整重写周期（默认 30 分钟）才可能落盘。归零让下一次保存 tick 立刻把它写下去 ——
// 现场第一次部署就是这样：账本在 12:41 被写过（空基线），14 分钟后读到的仍是没有基线的
// 版本，而修复看起来"没生效"。
func restoreLastCountersLocked() {
	lastCounters = make(map[string]CounterSample, len(store.LastCounters))
	for name, sample := range store.LastCounters {
		lastCounters[name] = sample
	}
	// 内存里的基线还没有对应的文件内容，下一次保存必须落盘。
	lastWriteUnix = 0
}

// snapshotLastCountersLocked 复制内存基线，供落盘使用。
//
// 复制而不是直接把 map 交给 store：落盘之后采集还会继续改 lastCounters，共享同一个 map
// 会让"内存中的基线"和"文件里的基线"变成一份东西，purgeExpiredLocked 之类的清理一旦
// 动它，文件内容就跟着变了。
func snapshotLastCountersLocked() map[string]CounterSample {
	if len(lastCounters) == 0 {
		return nil
	}
	out := make(map[string]CounterSample, len(lastCounters))
	for name, sample := range lastCounters {
		out[name] = sample
	}
	return out
}

func saveToFileLocked() error {
	// 确保目录存在
	if err := os.MkdirAll(filepath.Dir(SaveFilePath), 0o755); err != nil {
		return err
	}
	// 写入时带上当前 config
	store.Config = configOrDefault(config)
	// 基线随账本落盘，重启才能接上（见 NetStatic.LastCounters）。
	store.LastCounters = snapshotLastCountersLocked()
	b, err := json.Marshal(store) // 紧凑格式（不缩进）
	if err != nil {
		return err
	}
	tmp := SaveFilePath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, SaveFilePath); err != nil {
		return err
	}
	// 落盘成功：内存与文件一致，刷新限流时间戳
	storeDirty = false
	lastWriteUnix = nowUnix()
	return nil
}

// saveFailureLogged 记录「当前这一轮失败是否已经打过日志」。
//
// 保存是每秒级的，磁盘满或文件不可写会一直失败；不做限流的话，一次写不进去会变成
// 每秒一行日志，把 journal 写爆 —— 那本身就是一场事故。
var saveFailureLogged bool

// persistLocked 写盘并记录失败，调用方需持有 mu。
//
// 为什么不能像以前那样直接丢弃返回值：流量统计会静默停止持久化，而第一个症状是
// 重启之后数字不对（或者到套餐上限时才发现），那时已经无从追查是哪一天开始没写进去的。
// 这里只记「每轮失败一条 + 恢复一条」，和面板侧上传清理路径的处理方式一致。
func persistLocked() {
	err := saveToFileLocked()
	if err == nil {
		if saveFailureLogged {
			saveFailureLogged = false
			log.Println("netstatic: traffic ledger is being persisted again after a previous failure")
		}
		return
	}
	if !saveFailureLogged {
		saveFailureLogged = true
		log.Println("netstatic: cannot persist the traffic ledger, traffic history is not being saved (repeats suppressed until it recovers):", err)
	}
}

// shouldRewriteFileLocked 判断这次周期保存是否需要整体重写文件。
// store 没变过就不写；变过也要等离上次落盘至少 DefaultRewriteInterval 秒，
// 避免每 SaveInterval 都把 31 天的全量数据重新序列化一遍。
func shouldRewriteFileLocked(now uint64) bool {
	if !storeDirty {
		return false
	}
	if lastWriteUnix == 0 || DefaultRewriteInterval <= 0 {
		return true
	}
	return now >= lastWriteUnix+uint64(DefaultRewriteInterval)
}

// migrateConfig 把上一代默认值写出来的配置升级到当前代次。
// 第 1 代文件里的 detect_interval=2 是当时的默认值（agent 只会通过 SetNewConfig 下发 Nics，
// 从不主动设置采集间隔），不迁移的话把 DefaultDetectInterval 调大对存量节点完全不生效。
// 只有恰好等于旧默认值时才替换，其它值视为用户/面板的选择，原样保留。
func migrateConfig(c NetStaticConfig) NetStaticConfig {
	if c.ConfigVersion >= netStaticConfigVersion {
		return c
	}
	if c.DetectInterval == legacyDetectInterval {
		c.DetectInterval = DefaultDetectInterval
	}
	c.ConfigVersion = netStaticConfigVersion
	return c
}

func configOrDefault(c NetStaticConfig) NetStaticConfig {
	c = migrateConfig(c)
	if c.DataPreserveDay == 0 {
		c.DataPreserveDay = DefaultDataPreserveDay
	}
	if c.DetectInterval == 0 {
		c.DetectInterval = DefaultDetectInterval
	}
	if c.SaveInterval == 0 {
		c.SaveInterval = DefaultSaveInterval
	}
	return c
}

func purgeExpiredLocked() {
	// 根据 DataPreserveDay 删除过期数据
	ttl := time.Duration(config.DataPreserveDay * 24 * float64(time.Hour))
	cutoff := uint64(time.Now().Add(-ttl).Unix())
	for name, arr := range store.Interfaces {
		// 仅保留 >= cutoff 的数据
		kept := arr[:0]
		for _, td := range arr {
			if td.Timestamp >= cutoff {
				kept = append(kept, td)
			}
		}
		if len(kept) == 0 {
			delete(store.Interfaces, name)
			storeDirty = true
		} else {
			if len(kept) != len(arr) {
				storeDirty = true
			}
			store.Interfaces[name] = kept
		}
	}
}

func safeDelta(cur, prev uint64) uint64 {
	if cur >= prev {
		return cur - prev
	}
	// 处理计数器回绕或重置，视为 0 增量
	return 0
}

// assumedLinkBytesPerSecond 是判断增量是否可信时使用的链路速率上限（1 Gbps）。
//
// 为什么用一个假定值而不是读真实速率：`/sys/class/net/<iface>/speed` 在很多虚拟网卡上
// 返回 -1 或不存在，而这条路走不通时**不能退回"不做检查"** —— 现场那次 59.5 GB 的假增量
// 就是在一个读不到真实速率的节点上产生的。1 Gbps 是这类 VPS 的常见上限，配上下面的
// headroom 已经足够宽松：一个 30 秒采样间隔在 1 Gbps 上最多 3.75 GB，×3 余量 = 11.25 GB，
// 而假增量是 59.5 GB。真按 10 Gbps 网卡跑的机器由 ceilingBytes 的真实速率优先分支覆盖。
const assumedLinkBytesPerSecond = 1e9 / 8

// deltaHeadroom 给"瞬时速率可能短暂高于链路标称值"留的余量（网卡突发、计数器批量刷新）。
const deltaHeadroom = 3

// ceilingBytes 返回一段时间内物理上可能的字节数上限。
//
// 上限随 elapsed 线性增长，而不是固定在"一个采样间隔"：进程停了几小时再起来时，
// 那段间隔里的累计增量是真实的，按一个间隔去卡会把它整段丢掉。
func ceilingBytes(elapsed time.Duration) uint64 {
	if elapsed <= 0 {
		return 0
	}
	return uint64(elapsed.Seconds() * assumedLinkBytesPerSecond * deltaHeadroom)
}

// deltaGapLimit 是"上次记录与本次之间的时差"超过多少就拒绝一个区间增量。
//
// 一个桶代表不到一个 SaveInterval 的时间。若基线比这还旧（进程停了很久、或账本被
// 手工搬到了别的机器），把整段累计值记成一个桶会污染日/月统计，而这段时间的数据本来
// 也无法归到某一天。拒绝它并重新取基线，代价是这段时间的流量不计入 —— 与"重启丢一个
// 采集间隔"的既有取舍同向。
func deltaGapLimit() time.Duration {
	limit := time.Duration(DefaultSaveInterval * float64(time.Second))
	if limit <= 0 {
		return time.Minute
	}
	return limit
}

// invalidDeltaLogged 记录"当前这一轮"是否已经就丢弃增量打过日志，避免每次采集一行。
var invalidDeltaLogged bool

// plausibleDelta 判断一个增量是否物理上可信，并返回原因（可信时为空）。
//
// name 只用于让原因自带上下文：调用方会把它连同 reason 一起写进日志，而"哪个网卡"是
// 排查时第一个要问的问题。
func plausibleDelta(name string, dtx, drx uint64, elapsed time.Duration) (bool, string) {
	if elapsed <= 0 {
		return false, fmt.Sprintf("%s: no elapsed time since the previous sample", name)
	}
	if elapsed > deltaGapLimit() {
		return false, fmt.Sprintf(
			"%s: the previous sample is %s old, older than the %s bucket it would be attributed to",
			name, elapsed.Round(time.Second), deltaGapLimit())
	}
	ceiling := ceilingBytes(elapsed)
	if dtx > ceiling || drx > ceiling {
		return false, fmt.Sprintf(
			"%s: counted %s up / %s down in %s, more than the %s a 1 Gbps link can carry in that time",
			name, humanBytes(dtx), humanBytes(drx), elapsed.Round(time.Second), humanBytes(ceiling))
	}
	return true, ""
}

func humanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	value := float64(n)
	for _, suffix := range []string{"KiB", "MiB", "GiB", "TiB"} {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.1f %s", value, suffix)
		}
	}
	return fmt.Sprintf("%.1f PiB", value/unit)
}

func sampleOnceLocked() {
	ios, err := gnet.IOCounters(true)
	if err != nil {
		return
	}
	ts := nowUnix()
	// 用真实时钟而不是 ts：测试会把 nowUnix 换成受控时钟，而"距上次采集多久"必须反映
	// 真实流逝时间，否则上限判断会随之失真。
	sampledAt := uint64(time.Now().Unix())
	for _, io := range ios {
		name := io.Name
		// 仅监控指定网卡（当配置了 Nics 时）
		if !isNicAllowed(name) {
			continue
		}
		curTx := io.BytesSent
		curRx := io.BytesRecv
		prev, ok := lastCounters[name]
		if ok {
			elapsed := time.Duration(sampledAt-prev.At) * time.Second
			dtx := safeDelta(curTx, prev.Tx)
			drx := safeDelta(curRx, prev.Rx)
			if plausible, reason := plausibleDelta(name, dtx, drx, elapsed); plausible {
				// 首次采样不记录
				if dtx > 0 || drx > 0 {
					staticCache[name] = append(staticCache[name], TrafficData{Timestamp: ts, Tx: dtx, Rx: drx})
				}
				// 即便为 0，也可以记录，但为了降低噪音与占用，这里忽略 0
			} else if dtx > 0 || drx > 0 {
				// 不可信的增量要留下痕迹：静默丢弃会让"数字不对"这件事无从追查，
				// 而这正是这个字段存在的意义（见 docs/SILENT-FAILURES.md 的同类问题）。
				if !invalidDeltaLogged {
					invalidDeltaLogged = true
					log.Printf("netstatic: discarded an implausible traffic delta on %s (%s); "+
						"re-baselining from the current counters, this interval is not counted",
						name, reason)
				}
				lastCounters[name] = CounterSample{Tx: curTx, Rx: curRx, At: sampledAt}
				storeDirty = true
				continue
			}
		}
		lastCounters[name] = CounterSample{Tx: curTx, Rx: curRx, At: sampledAt}
		storeDirty = true
	}
}

func flushCacheLocked(ts uint64) {
	if len(staticCache) == 0 {
		return
	}
	for name, arr := range staticCache {
		var sumTx, sumRx uint64
		for _, td := range arr {
			sumTx += td.Tx
			sumRx += td.Rx
		}
		if sumTx > 0 || sumRx > 0 {
			store.Interfaces[name] = append(store.Interfaces[name], TrafficData{Timestamp: ts, Tx: sumTx, Rx: sumRx})
			storeDirty = true
		}
	}
	// 清空缓存
	staticCache = make(map[string][]TrafficData)
}

// startGoroutinesLocked 启动采集和保存的 goroutines（调用前必须已持有锁）
func startGoroutinesLocked() {
	// 采集 goroutine
	go func() {
		for {
			select {
			case <-detectTicker.C:
				mu.Lock()
				sampleOnceLocked()
				mu.Unlock()
			case <-stopCh:
				return
			}
		}
	}()

	// 保存 goroutine
	go func() {
		for {
			select {
			case t := <-saveTicker.C:
				mu.Lock()
				flushCacheLocked(uint64(t.Unix()))
				purgeExpiredLocked()
				if shouldRewriteFileLocked(uint64(t.Unix())) {
					persistLocked()
				}
				mu.Unlock()
			case <-stopCh:
				return
			}
		}
	}()
}

// GetNetStatic 获取当前的所有流量统计数据
func GetNetStatic() (*NetStatic, error) {
	mu.RLock()
	defer mu.RUnlock()
	ensureInitLocked()
	// 合并 store + cache（cache 不合并为单点，直接以原样返回临时视图）
	merged := NetStatic{Interfaces: map[string][]TrafficData{}, Config: configOrDefault(config)}
	for name, arr := range store.Interfaces {
		cp := make([]TrafficData, len(arr))
		copy(cp, arr)
		merged.Interfaces[name] = cp
	}
	for name, arr := range staticCache {
		merged.Interfaces[name] = append(merged.Interfaces[name], arr...)
	}
	return &merged, nil
}

// StartOrContinue 开始或继续流量统计
func StartOrContinue() error {
	if running {
		return nil
	}
	mu.Lock()
	defer mu.Unlock()
	ensureInitLocked()
	// 读取历史
	if err := loadFromFileLocked(); err != nil {
		return err
	}
	// 启动 ticker
	detectTicker = time.NewTicker(time.Duration(config.DetectInterval * float64(time.Second)))
	saveTicker = time.NewTicker(time.Duration(config.SaveInterval * float64(time.Second)))
	stopCh = make(chan struct{})
	running = true

	// 启动 goroutines
	startGoroutinesLocked()
	return nil
}

// Clear 清除所有流量统计数据
func Clear() error {
	mu.Lock()
	defer mu.Unlock()
	ensureInitLocked()
	store.Interfaces = make(map[string][]TrafficData)
	staticCache = make(map[string][]TrafficData)
	lastCounters = map[string]CounterSample{}
	storeDirty = true
	// 不落盘，等下次保存或停止时写
	return nil
}

// Stop 停止流量统计
func Stop() error {
	mu.Lock()
	if !running {
		mu.Unlock()
		return nil
	}
	running = false
	if detectTicker != nil {
		detectTicker.Stop()
	}
	if saveTicker != nil {
		saveTicker.Stop()
	}
	close(stopCh)
	// 最后一轮 flush + 保存
	flushCacheLocked(nowUnix())
	purgeExpiredLocked()
	err := saveToFileLocked()
	mu.Unlock()
	return err
}

// GetNetStaticBetween 获取指定时间段内的流量统计数据，start和end为unix时间戳
func GetNetStaticBetween(start, end uint64) (*NetStatic, error) {
	mu.RLock()
	defer mu.RUnlock()
	ensureInitLocked()
	res := NetStatic{Interfaces: map[string][]TrafficData{}, Config: configOrDefault(config)}
	inRange := func(ts uint64) bool { return (start == 0 || ts >= start) && (end == 0 || ts <= end) }
	for name, arr := range store.Interfaces {
		var filtered []TrafficData
		for _, td := range arr {
			if inRange(td.Timestamp) {
				filtered = append(filtered, td)
			}
		}
		if len(filtered) > 0 {
			res.Interfaces[name] = filtered
		}
	}
	// 合并缓存
	for name, arr := range staticCache {
		for _, td := range arr {
			if inRange(td.Timestamp) {
				res.Interfaces[name] = append(res.Interfaces[name], td)
			}
		}
	}
	return &res, nil
}

// GetTotalTraffic 获取总流量统计数据, key为网卡名称, value为对应的流量数据总和
func GetTotalTraffic() (map[string]TrafficData, error) {
	mu.RLock()
	defer mu.RUnlock()
	ensureInitLocked()
	res := map[string]TrafficData{}
	add := func(name string, tx, rx uint64) {
		cur := res[name]
		cur.Tx += tx
		cur.Rx += rx
		res[name] = cur
	}
	for name, arr := range store.Interfaces {
		var tx, rx uint64
		for _, td := range arr {
			tx += td.Tx
			rx += td.Rx
		}
		add(name, tx, rx)
	}
	for name, arr := range staticCache {
		var tx, rx uint64
		for _, td := range arr {
			tx += td.Tx
			rx += td.Rx
		}
		add(name, tx, rx)
	}
	return res, nil
}

// GetTotalTrafficBetween 获取指定时间段内的总流量统计数据，start和end为unix时间戳
func GetTotalTrafficBetween(start, end uint64) (map[string]TrafficData, error) {
	mu.RLock()
	defer mu.RUnlock()
	ensureInitLocked()
	res := map[string]TrafficData{}
	inRange := func(ts uint64) bool { return (start == 0 || ts >= start) && (end == 0 || ts <= end) }
	add := func(name string, tx, rx uint64) {
		cur := res[name]
		cur.Tx += tx
		cur.Rx += rx
		res[name] = cur
	}
	for name, arr := range store.Interfaces {
		var tx, rx uint64
		for _, td := range arr {
			if inRange(td.Timestamp) {
				tx += td.Tx
				rx += td.Rx
			}
		}
		if tx > 0 || rx > 0 {
			add(name, tx, rx)
		}
	}
	for name, arr := range staticCache {
		var tx, rx uint64
		for _, td := range arr {
			if inRange(td.Timestamp) {
				tx += td.Tx
				rx += td.Rx
			}
		}
		if tx > 0 || rx > 0 {
			add(name, tx, rx)
		}
	}
	return res, nil
}

// SetNewConfig 设置新的配置，config中的值如果为0则表示不修改对应的配置项
func SetNewConfig(newCfg NetStaticConfig) error {
	mu.Lock()
	defer mu.Unlock()
	ensureInitLocked()
	// 合并新配置
	if newCfg.DataPreserveDay != 0 {
		store.Config.DataPreserveDay = newCfg.DataPreserveDay
	}
	if newCfg.DetectInterval != 0 {
		store.Config.DetectInterval = newCfg.DetectInterval
	}
	if newCfg.SaveInterval != 0 {
		store.Config.SaveInterval = newCfg.SaveInterval
	}
	// Nics: nil 表示不修改；非 nil 则更新（空切片表示监控所有网卡）
	if newCfg.Nics != nil {
		// 做一份拷贝以避免外部切片后续修改影响内部配置
		tmp := make([]string, len(newCfg.Nics))
		copy(tmp, newCfg.Nics)
		store.Config.Nics = tmp
	}
	// 更新生效配置
	cfg := configOrDefault(store.Config)
	store.Config = cfg
	config = cfg
	// 重新配置 ticker（若运行中）
	if running {
		// 先停止旧的 ticker 和 goroutines
		if detectTicker != nil {
			detectTicker.Stop()
		}
		if saveTicker != nil {
			saveTicker.Stop()
		}
		close(stopCh)

		// 重新创建 ticker 和 channel
		detectTicker = time.NewTicker(time.Duration(cfg.DetectInterval * float64(time.Second)))
		saveTicker = time.NewTicker(time.Duration(cfg.SaveInterval * float64(time.Second)))
		stopCh = make(chan struct{})

		// 重新启动 goroutines
		startGoroutinesLocked()

		// 当配置了指定网卡白名单时，清理不在白名单内的缓存与上次计数，避免无用数据积累
		if len(cfg.Nics) > 0 {
			allowed := make(map[string]struct{}, len(cfg.Nics))
			for _, n := range cfg.Nics {
				allowed[n] = struct{}{}
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
	}
	// 立即写盘
	persistLocked()
	// 同时做一次过期清理
	purgeExpiredLocked()
	return nil
}

func ForceReplaceRecord(rec map[string][]TrafficData) error {
	mu.Lock()
	defer mu.Unlock()
	ensureInitLocked()
	store.Interfaces = rec
	storeDirty = true
	// 不立即写盘，等下一次周期性保存或停止时写
	// 同时做一次过期清理
	purgeExpiredLocked()
	return nil
}
