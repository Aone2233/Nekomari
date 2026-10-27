package geoip

import (
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/internal/config"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func geoIPConfigDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "settings.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	config.SetDb(db)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func storeGeoIPConfig(t *testing.T, db *gorm.DB, enabled, provider any) {
	t.Helper()
	for key, value := range map[string]any{config.GeoIpEnabledKey: enabled, config.GeoIpProviderKey: provider} {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Save(&config.ConfigItem{Key: key, Value: string(raw)}).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func requireEmptyGeoIP(t *testing.T) {
	t.Helper()
	if _, ok := CurrentProvider.(*EmptyProvider); !ok {
		t.Fatalf("provider = %T, want EmptyProvider", CurrentProvider)
	}
	if info, err := GetGeoInfo(net.ParseIP("192.0.2.1")); err == nil || info != nil {
		t.Fatal("disabled or invalid GeoIP must not return a stale cached location")
	}
}

func TestGeoIPReloadRejectsMalformedSettingsWithoutPanic(t *testing.T) {
	db := geoIPConfigDB(t)
	previous := CurrentProvider
	t.Cleanup(func() { CurrentProvider = previous; geoCache.Flush() })
	for _, test := range []struct {
		name     string
		enabled  any
		provider any
	}{
		{"string boolean", "true", "ipinfo"},
		{"number boolean", 1, "ipinfo"},
		{"null boolean", nil, "ipinfo"},
		{"object boolean", map[string]any{}, "ipinfo"},
		{"number provider", true, 12},
		{"null provider", true, nil},
		{"array provider", true, []string{"ipinfo"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			CurrentProvider, _ = NewIPInfoService()
			storeGeoIPConfig(t, db, test.enabled, test.provider)
			defer func() {
				if got := recover(); got != nil {
					t.Errorf("malformed optional GeoIP settings must not panic: %v", got)
				}
			}()
			InitGeoIp()
			requireEmptyGeoIP(t)
		})
	}
}

func TestGeoIPReloadDisableReenableAndProviderChange(t *testing.T) {
	db := geoIPConfigDB(t)
	previous := CurrentProvider
	t.Cleanup(func() { CurrentProvider = previous; geoCache.Flush() })
	storeGeoIPConfig(t, db, true, "ipinfo")
	InitGeoIp()
	if _, ok := CurrentProvider.(*IPInfoService); !ok {
		t.Fatalf("enabled provider = %T, want IPInfoService", CurrentProvider)
	}
	geoCache.SetDefault("ipinfo.io:192.0.2.1", &GeoInfo{ISOCode: "US"})
	storeGeoIPConfig(t, db, false, "ipinfo")
	InitGeoIp()
	requireEmptyGeoIP(t)
	storeGeoIPConfig(t, db, true, "geojs")
	InitGeoIp()
	if _, ok := CurrentProvider.(*GeoJSService); !ok {
		t.Fatalf("re-enabled provider = %T, want GeoJSService", CurrentProvider)
	}
	if geoCache.ItemCount() != 0 {
		t.Fatal("provider reload must invalidate old cached locations")
	}
}

func TestGeoIPReloadClosedDatabaseFallsBackWithoutPanic(t *testing.T) {
	db := geoIPConfigDB(t)
	previous := CurrentProvider
	t.Cleanup(func() { CurrentProvider = previous; geoCache.Flush() })
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	CurrentProvider, _ = NewIPInfoService()
	InitGeoIp()
	requireEmptyGeoIP(t)
}

type blockingGeoIP struct {
	entered chan struct{}
	release chan struct{}
	closed  atomic.Bool
	calls   atomic.Int64
	closes  atomic.Int64
}

func (p *blockingGeoIP) Name() string { return "blocking-test" }
func (p *blockingGeoIP) GetGeoInfo(net.IP) (*GeoInfo, error) {
	p.calls.Add(1)
	if p.entered != nil {
		close(p.entered)
		<-p.release
	}
	if p.closed.Load() {
		return nil, errors.New("lookup raced provider close")
	}
	return &GeoInfo{ISOCode: "US"}, nil
}
func (p *blockingGeoIP) UpdateDatabase() error {
	if p.entered != nil {
		close(p.entered)
		<-p.release
	}
	if p.closed.Load() {
		return errors.New("database update raced provider close")
	}
	return nil
}
func (p *blockingGeoIP) Close() error {
	p.closed.Store(true)
	p.closes.Add(1)
	return nil
}

func TestGeoIPReloadWaitsForLookupAndClearsRetiredCache(t *testing.T) {
	db := geoIPConfigDB(t)
	previous := CurrentProvider
	t.Cleanup(func() { CurrentProvider = previous; geoCache.Flush() })
	provider := &blockingGeoIP{entered: make(chan struct{}), release: make(chan struct{})}
	CurrentProvider = provider
	geoCache.Flush()
	storeGeoIPConfig(t, db, false, "ipinfo")
	lookup := make(chan error, 1)
	go func() { _, err := GetGeoInfo(net.ParseIP("192.0.2.1")); lookup <- err }()
	<-provider.entered
	reloaded := make(chan struct{})
	go func() { InitGeoIp(); close(reloaded) }()
	if provider.closed.Load() {
		t.Fatal("provider closed before its active lookup finished")
	}
	close(provider.release)
	if err := <-lookup; err != nil {
		t.Fatal(err)
	}
	select {
	case <-reloaded:
	case <-time.After(5 * time.Second):
		t.Fatal("reload did not finish after lookup")
	}
	if provider.closes.Load() != 1 || geoCache.ItemCount() != 0 {
		t.Fatal("reload must close the old provider exactly once and clear its late cache write")
	}
	requireEmptyGeoIP(t)
}

func TestGeoIPConcurrentLookupReloadAndShutdown(t *testing.T) {
	db := geoIPConfigDB(t)
	previous := CurrentProvider
	previousStopped := stopped
	t.Cleanup(func() { CurrentProvider = previous; stopped = previousStopped; geoCache.Flush() })
	storeGeoIPConfig(t, db, false, "ipinfo")
	provider := &blockingGeoIP{}
	CurrentProvider = provider
	geoCache.Flush()
	var group sync.WaitGroup
	for range 6 {
		group.Go(func() {
			for range 100 {
				_, _ = GetGeoInfo(net.ParseIP("192.0.2.1"))
			}
		})
	}
	for range 3 {
		group.Go(func() { InitGeoIp() })
	}
	group.Go(func() { _ = Shutdown() })
	group.Wait()
	if provider.closes.Load() != 1 {
		t.Fatalf("provider closed %d times, want once", provider.closes.Load())
	}
	// A reload queued before process teardown must not reopen a provider afterward.
	storeGeoIPConfig(t, db, true, "ipinfo")
	InitGeoIp()
	requireEmptyGeoIP(t)
}

func TestGeoIPReloadWaitsForDatabaseUpdate(t *testing.T) {
	db := geoIPConfigDB(t)
	previous := CurrentProvider
	t.Cleanup(func() { CurrentProvider = previous; geoCache.Flush() })
	provider := &blockingGeoIP{entered: make(chan struct{}), release: make(chan struct{})}
	CurrentProvider = provider
	storeGeoIPConfig(t, db, false, "ipinfo")
	updated := make(chan error, 1)
	go func() { updated <- UpdateDatabase() }()
	<-provider.entered
	reloaded := make(chan struct{})
	go func() { InitGeoIp(); close(reloaded) }()
	if provider.closed.Load() {
		t.Fatal("provider closed before its active database update finished")
	}
	close(provider.release)
	if err := <-updated; err != nil {
		t.Fatal(err)
	}
	select {
	case <-reloaded:
	case <-time.After(5 * time.Second):
		t.Fatal("reload did not finish after database update")
	}
	if provider.closes.Load() != 1 {
		t.Fatal("reload must close the retired provider exactly once")
	}
	requireEmptyGeoIP(t)
}

func TestGeoIPReloadUnknownProvider(t *testing.T) {
	db := geoIPConfigDB(t)
	previous := CurrentProvider
	t.Cleanup(func() { CurrentProvider = previous; geoCache.Flush() })
	for _, name := range []string{"empty", "", "unrecognized"} {
		CurrentProvider, _ = NewIPInfoService()
		storeGeoIPConfig(t, db, true, name)
		InitGeoIp()
		requireEmptyGeoIP(t)
	}
}
