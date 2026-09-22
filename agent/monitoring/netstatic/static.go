package netstatic

import (
	"encoding/json"
	"errors"
	"io"
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

	// 上次采集到的累计字节数（用于计算 delta）
	lastCounters = map[string]struct{ Tx, Rx uint64 }{}
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
	// 启动时清理过期数据
	purgeExpiredLocked()
	return nil
}

func saveToFileLocked() error {
	// 确保目录存在
	if err := os.MkdirAll(filepath.Dir(SaveFilePath), 0o755); err != nil {
		return err
	}
	// 写入时带上当前 config
	store.Config = configOrDefault(config)
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

func sampleOnceLocked() {
	ios, err := gnet.IOCounters(true)
	if err != nil {
		return
	}
	ts := nowUnix()
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
			dtx := safeDelta(curTx, prev.Tx)
			drx := safeDelta(curRx, prev.Rx)
			// 首次采样不记录
			if dtx > 0 || drx > 0 {
				staticCache[name] = append(staticCache[name], TrafficData{Timestamp: ts, Tx: dtx, Rx: drx})
			} else {
				// 即便为 0，也可以记录，但为了降低噪音与占用，这里忽略 0
			}
		}
		lastCounters[name] = struct{ Tx, Rx uint64 }{Tx: curTx, Rx: curRx}
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
					_ = saveToFileLocked()
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
	lastCounters = map[string]struct{ Tx, Rx uint64 }{}
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
	_ = saveToFileLocked()
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
