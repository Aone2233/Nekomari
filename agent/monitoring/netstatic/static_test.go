package netstatic

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// resetState 把包级状态恢复原样，避免测试之间互相污染
func resetState(t *testing.T) {
	t.Helper()

	oldStore, oldConfig, oldCache := store, config, staticCache
	oldRunning, oldCounters := running, lastCounters
	oldDirty, oldLastWrite := storeDirty, lastWriteUnix
	oldSaveFilePath := SaveFilePath
	oldDetect, oldRewrite := DefaultDetectInterval, DefaultRewriteInterval

	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		store, config, staticCache = oldStore, oldConfig, oldCache
		running, lastCounters = oldRunning, oldCounters
		storeDirty, lastWriteUnix = oldDirty, oldLastWrite
		SaveFilePath = oldSaveFilePath
		DefaultDetectInterval, DefaultRewriteInterval = oldDetect, oldRewrite
	})

	mu.Lock()
	store = NetStatic{Interfaces: map[string][]TrafficData{}}
	staticCache = map[string][]TrafficData{}
	lastCounters = map[string]struct{ Tx, Rx uint64 }{}
	config = configOrDefault(NetStaticConfig{})
	storeDirty = false
	lastWriteUnix = 0
	mu.Unlock()
}

func TestFlushCacheSumsWindow(t *testing.T) {
	resetState(t)

	// 一个保存窗口内的多个采集样本，flush 之后应该合并成一个桶
	staticCache = map[string][]TrafficData{
		"eth0": {
			{Timestamp: 100, Tx: 10, Rx: 20},
			{Timestamp: 130, Tx: 5, Rx: 0},
			{Timestamp: 160, Tx: 0, Rx: 7},
		},
		// 全是 0 的接口不落桶
		"eth1": {
			{Timestamp: 100, Tx: 0, Rx: 0},
		},
	}
	// 已有历史数据时必须追加而不是覆盖
	store.Interfaces["eth0"] = []TrafficData{{Timestamp: 0, Tx: 1, Rx: 1}}

	flushCacheLocked(600)

	eth0 := store.Interfaces["eth0"]
	if len(eth0) != 2 {
		t.Fatalf("expected 2 buckets for eth0, got %d: %+v", len(eth0), eth0)
	}
	if eth0[0].Timestamp != 0 {
		t.Errorf("expected existing bucket to be preserved, got %+v", eth0[0])
	}
	if eth0[1].Timestamp != 600 || eth0[1].Tx != 15 || eth0[1].Rx != 27 {
		t.Errorf("expected summed bucket {600 15 27}, got %+v", eth0[1])
	}
	if _, ok := store.Interfaces["eth1"]; ok {
		t.Errorf("expected all-zero interface to be skipped, got %+v", store.Interfaces["eth1"])
	}
	if len(staticCache) != 0 {
		t.Errorf("expected cache to be cleared, got %+v", staticCache)
	}
	if !storeDirty {
		t.Error("expected store to be marked dirty after a flush")
	}
}

func TestFlushCacheEmptyIsNoop(t *testing.T) {
	resetState(t)

	flushCacheLocked(600)

	if len(store.Interfaces) != 0 {
		t.Errorf("expected no buckets, got %+v", store.Interfaces)
	}
	if storeDirty {
		t.Error("expected empty flush to leave the store clean")
	}
}

func TestPurgeExpiredLockedEnforcesRetentionBoundary(t *testing.T) {
	resetState(t)

	config.DataPreserveDay = DefaultDataPreserveDay
	now := time.Now().Unix()
	retention := int64(DefaultDataPreserveDay * 24 * 60 * 60)

	store.Interfaces = map[string][]TrafficData{
		// 边界内：保留
		"eth0": {
			{Timestamp: uint64(now - retention + 60), Tx: 1, Rx: 1},
			{Timestamp: uint64(now), Tx: 2, Rx: 2},
		},
		// 全部过期：接口整体删除
		"eth1": {
			{Timestamp: uint64(now - retention - 60), Tx: 3, Rx: 3},
			{Timestamp: uint64(now - retention - 600), Tx: 4, Rx: 4},
		},
		// 部分过期：只留边界内的
		"eth2": {
			{Timestamp: uint64(now - retention - 60), Tx: 5, Rx: 5},
			{Timestamp: uint64(now - 60), Tx: 6, Rx: 6},
		},
	}

	purgeExpiredLocked()

	if got := len(store.Interfaces["eth0"]); got != 2 {
		t.Errorf("expected eth0 to keep 2 buckets, got %d", got)
	}
	if _, ok := store.Interfaces["eth1"]; ok {
		t.Errorf("expected fully expired interface eth1 to be deleted, got %+v", store.Interfaces["eth1"])
	}
	eth2 := store.Interfaces["eth2"]
	if len(eth2) != 1 || eth2[0].Tx != 6 {
		t.Errorf("expected eth2 to keep only the in-window bucket, got %+v", eth2)
	}
	if !storeDirty {
		t.Error("expected store to be marked dirty after purging expired buckets")
	}
}

func TestPurgeExpiredLockedKeepsEverythingInWindow(t *testing.T) {
	resetState(t)

	config.DataPreserveDay = DefaultDataPreserveDay
	now := uint64(time.Now().Unix())
	store.Interfaces = map[string][]TrafficData{
		"eth0": {
			{Timestamp: now - 60, Tx: 1, Rx: 1},
			{Timestamp: now, Tx: 2, Rx: 2},
		},
	}
	storeDirty = false

	purgeExpiredLocked()

	if got := len(store.Interfaces["eth0"]); got != 2 {
		t.Errorf("expected 2 buckets to survive, got %d", got)
	}
	if storeDirty {
		t.Error("expected store to stay clean when nothing expired")
	}
}

func TestShouldRewriteFileLocked(t *testing.T) {
	resetState(t)

	DefaultRewriteInterval = 60 * 30

	tests := []struct {
		name          string
		dirty         bool
		lastWriteUnix uint64
		now           uint64
		want          bool
	}{
		{name: "clean store is never rewritten", dirty: false, lastWriteUnix: 0, now: 1 << 40, want: false},
		{name: "first write is never throttled", dirty: true, lastWriteUnix: 0, now: 1 << 40, want: true},
		{name: "inside the window is skipped", dirty: true, lastWriteUnix: 1000, now: 1000 + 60*30 - 1, want: false},
		{name: "at the window boundary it writes", dirty: true, lastWriteUnix: 1000, now: 1000 + 60*30, want: true},
		{name: "past the window it writes", dirty: true, lastWriteUnix: 1000, now: 1000 + 60*30 + 1, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storeDirty = tt.dirty
			lastWriteUnix = tt.lastWriteUnix
			if got := shouldRewriteFileLocked(tt.now); got != tt.want {
				t.Errorf("shouldRewriteFileLocked(%d) = %v, want %v", tt.now, got, tt.want)
			}
		})
	}

	// 限流关闭时只要有变化就写
	DefaultRewriteInterval = 0
	storeDirty = true
	lastWriteUnix = 1000
	if !shouldRewriteFileLocked(1000) {
		t.Error("expected rewrite when throttling is disabled")
	}
}

func TestSaveToFileLockedRefreshesThrottleState(t *testing.T) {
	resetState(t)

	SaveFilePath = filepath.Join(t.TempDir(), "net_static.json")
	store.Interfaces = map[string][]TrafficData{
		"eth0": {{Timestamp: 100, Tx: 1, Rx: 2}},
	}
	storeDirty = true
	lastWriteUnix = 0

	if err := saveToFileLocked(); err != nil {
		t.Fatalf("saveToFileLocked failed: %v", err)
	}
	if storeDirty {
		t.Error("expected store to be clean after a successful save")
	}
	if lastWriteUnix == 0 {
		t.Error("expected lastWriteUnix to be refreshed after a successful save")
	}
	if shouldRewriteFileLocked(lastWriteUnix + 60) {
		t.Error("expected a save to start a new throttle window")
	}

	data, err := os.ReadFile(SaveFilePath)
	if err != nil {
		t.Fatalf("failed to read saved file: %v", err)
	}
	var ns NetStatic
	if err := json.Unmarshal(data, &ns); err != nil {
		t.Fatalf("failed to unmarshal saved file: %v", err)
	}
	if len(ns.Interfaces["eth0"]) != 1 {
		t.Errorf("expected the saved file to contain the eth0 bucket, got %+v", ns.Interfaces)
	}
	if ns.Config.ConfigVersion != netStaticConfigVersion {
		t.Errorf("expected saved config to carry config_version=%d, got %d", netStaticConfigVersion, ns.Config.ConfigVersion)
	}
}

func TestMigrateConfigLegacyDetectInterval(t *testing.T) {
	resetState(t)

	tests := []struct {
		name       string
		in         NetStaticConfig
		wantDetect float64
	}{
		{
			name:       "legacy default is migrated",
			in:         NetStaticConfig{DetectInterval: legacyDetectInterval},
			wantDetect: DefaultDetectInterval,
		},
		{
			name:       "explicit non-legacy value is kept",
			in:         NetStaticConfig{DetectInterval: 5},
			wantDetect: 5,
		},
		{
			name:       "already migrated config is kept as is",
			in:         NetStaticConfig{ConfigVersion: netStaticConfigVersion, DetectInterval: legacyDetectInterval},
			wantDetect: legacyDetectInterval,
		},
		{
			name:       "zero value falls back to the new default",
			in:         NetStaticConfig{},
			wantDetect: DefaultDetectInterval,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := configOrDefault(tt.in)
			if got.DetectInterval != tt.wantDetect {
				t.Errorf("expected detect_interval=%v, got %v", tt.wantDetect, got.DetectInterval)
			}
			if got.ConfigVersion != netStaticConfigVersion {
				t.Errorf("expected config_version=%d, got %d", netStaticConfigVersion, got.ConfigVersion)
			}
			if got.SaveInterval != DefaultSaveInterval {
				t.Errorf("expected save_interval=%v, got %v", DefaultSaveInterval, got.SaveInterval)
			}
		})
	}
}

func TestLoadFromFileMigratesLegacyConfig(t *testing.T) {
	resetState(t)

	SaveFilePath = filepath.Join(t.TempDir(), "net_static.json")
	// 存量节点上的文件形态：detect_interval 是旧默认值 2，没有 config_version
	legacy := fmt.Sprintf(`{"interfaces":{"eth0":[{"timestamp":%d,"tx":1,"rx":2}]},"config":{"data_preserve_day":31,"detect_interval":2,"save_interval":600,"nics":["eth0"]}}`, time.Now().Unix()-60)
	if err := os.WriteFile(SaveFilePath, []byte(legacy), 0o644); err != nil {
		t.Fatalf("failed to write legacy file: %v", err)
	}

	if err := loadFromFileLocked(); err != nil {
		t.Fatalf("loadFromFileLocked failed: %v", err)
	}

	if config.DetectInterval != DefaultDetectInterval {
		t.Errorf("expected legacy detect_interval to migrate to %v, got %v", DefaultDetectInterval, config.DetectInterval)
	}
	if config.ConfigVersion != netStaticConfigVersion {
		t.Errorf("expected config_version=%d, got %d", netStaticConfigVersion, config.ConfigVersion)
	}
	if len(store.Interfaces["eth0"]) != 1 {
		t.Errorf("expected history to be preserved, got %+v", store.Interfaces)
	}
	if !storeDirty {
		t.Error("expected a migrated config to be marked dirty so it gets persisted")
	}
}
