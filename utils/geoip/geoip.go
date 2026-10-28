package geoip

import (
	"net"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/Aone2233/nekomari/internal/config"
	logger "github.com/Aone2233/nekomari/utils/log"
	"github.com/patrickmn/go-cache"
)

var CurrentProvider GeoIPService
var geoCache *cache.Cache
var providerMu sync.RWMutex
var reloadMu sync.Mutex
var stopped bool

// Retain empty results briefly to limit upstream traffic while allowing a
// temporary missing country to recover sooner than the 48-hour positive TTL.
const emptyCountryCacheTTL = time.Hour

type GeoInfo struct {
	ISOCode string
	Name    string
}

func init() {
	CurrentProvider = &EmptyProvider{}
	geoCache = cache.New(48*time.Hour, 1*time.Hour)
}

// GeoIPService 接口定义了获取地理位置信息的核心方法。
// 任何实现此接口的类型都可以作为地理位置服务提供者。
type GeoIPService interface {
	Name() string

	GetGeoInfo(ip net.IP) (*GeoInfo, error)

	UpdateDatabase() error

	Close() error
}

func GetRegionUnicodeEmoji(isoCode string) string {
	if len(isoCode) != 2 {
		return ""
	}
	isoCode = strings.ToUpper(isoCode)

	if !unicode.IsLetter(rune(isoCode[0])) || !unicode.IsLetter(rune(isoCode[1])) {
		return ""
	}

	rune1 := rune(0x1F1E6 + (rune(isoCode[0]) - 'A'))
	rune2 := rune(0x1F1E6 + (rune(isoCode[1]) - 'A'))
	return string(rune1) + string(rune2)
}

func InitGeoIp() {
	// Serialize reloads and read current settings rather than event snapshots, so
	// queued reloads converge to the latest state and cannot publish after shutdown.
	reloadMu.Lock()
	defer reloadMu.Unlock()
	if stopped {
		return
	}
	if config.Database() == nil {
		logger.Warn("geoip", "configuration unavailable; using EmptyProvider")
		replaceProvider(&EmptyProvider{})
		return
	}
	conf, err := config.GetMany(map[string]any{
		config.GeoIpEnabledKey:  true,
		config.GeoIpProviderKey: "ipinfo",
	})
	if err != nil {
		logger.Warn("geoip", "failed to read configuration; using EmptyProvider")
		replaceProvider(&EmptyProvider{})
		return
	}
	enabled, ok := conf[config.GeoIpEnabledKey].(bool)
	if !ok {
		logger.Warn("geoip", "invalid GeoIP enabled setting; expected boolean; using EmptyProvider")
		replaceProvider(&EmptyProvider{})
		return
	}
	if !enabled {
		replaceProvider(&EmptyProvider{})
		logger.Info("geoip", "GeoIP disabled")
		return
	}
	name, ok := conf[config.GeoIpProviderKey].(string)
	if !ok {
		logger.Warn("geoip", "invalid GeoIP provider setting; expected string; using EmptyProvider")
		replaceProvider(&EmptyProvider{})
		return
	}
	var provider GeoIPService = &EmptyProvider{}
	switch name {
	case "mmdb":
		if service, initErr := NewMaxMindGeoIPService(); initErr == nil && service != nil {
			provider = service
		} else {
			err = initErr
		}
	case "ip-api":
		if service, initErr := NewIPAPIService(); initErr == nil && service != nil {
			provider = service
		} else {
			err = initErr
		}
	case "geojs":
		if service, initErr := NewGeoJSService(); initErr == nil && service != nil {
			provider = service
		} else {
			err = initErr
		}
	case "ipinfo":
		if service, initErr := NewIPInfoService(); initErr == nil && service != nil {
			provider = service
		} else {
			err = initErr
		}
	case "", "empty":
		logger.Info("geoip", "no GeoIP provider selected; using EmptyProvider")
	default:
		logger.Warn("geoip", "unknown GeoIP provider; using EmptyProvider")
	}
	if err != nil {
		logger.Warn("geoip", "provider initialization failed; using EmptyProvider")
	}
	replaceProvider(provider)
	logger.Info("geoip", "using GeoIP provider", "provider", provider.Name())
}

// Callers hold reloadMu. Readers finish before a provider is closed, and cannot
// repopulate the cache with results from the retired instance after the flush.
func replaceProvider(next GeoIPService) error {
	providerMu.Lock()
	previous := CurrentProvider
	CurrentProvider = next
	geoCache.Flush()
	providerMu.Unlock()
	if previous != nil {
		if err := previous.Close(); err != nil {
			logger.Warn("geoip", "failed to close retired GeoIP provider")
			return err
		}
	}
	return nil
}

// Shutdown 关闭当前 GeoIP provider 持有的资源（如 mmdb 文件句柄）。供关闭流程调用。
func Shutdown() error {
	reloadMu.Lock()
	defer reloadMu.Unlock()
	stopped = true
	return replaceProvider(&EmptyProvider{})
}

func GetGeoInfo(ip net.IP) (*GeoInfo, error) {
	providerMu.RLock()
	defer providerMu.RUnlock()
	providerName := CurrentProvider.Name()
	cacheKey := providerName + ":" + ip.String()

	if cachedInfo, found := geoCache.Get(cacheKey); found {
		return cachedInfo.(*GeoInfo), nil
	}

	info, err := CurrentProvider.GetGeoInfo(ip)
	if err == nil && info != nil {
		if strings.TrimSpace(info.ISOCode) == "" && strings.TrimSpace(info.Name) == "" {
			// An in-flight empty lookup must not overwrite or extend a concurrent result.
			_ = geoCache.Add(cacheKey, info, emptyCountryCacheTTL)
		} else {
			geoCache.Set(cacheKey, info, cache.DefaultExpiration)
		}
	}
	return info, err
}

func UpdateDatabase() error {
	// MMDB constructors and updates share the same on-disk database/temp path.
	reloadMu.Lock()
	defer reloadMu.Unlock()
	providerMu.RLock()
	defer providerMu.RUnlock()
	err := CurrentProvider.UpdateDatabase()
	if err == nil {
		geoCache.Flush()
		logger.Info("geoip", "cache cleared due to database update")
	}
	return err
}
