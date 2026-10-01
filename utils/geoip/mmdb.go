package geoip // 与 geoip.go 保持相同的包名，表示它们是同一个包的组成部分

import (
	"fmt"
	logger "github.com/Aone2233/nekomari/utils/log"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath" // 新增导入，用于处理文件路径
	"sync"
	"time"

	"github.com/Aone2233/nekomari/database/auditlog"
	"github.com/oschwald/maxminddb-golang"
)

// GeoIpUrl 是 MaxMind 数据库的下载地址。
var GeoIpUrl = "https://raw.githubusercontent.com/Loyalsoldier/geoip/release/GeoLite2-Country.mmdb"

// GeoIpFilePath 是本地存储 MaxMind 数据库的路径。
var GeoIpFilePath = "./data/GeoLite2-Country.mmdb"

// GeoIpRecord 结构体定义了 MaxMind 数据库查询结果的原始结构。
// 它是 MaxMind 库特有的，用于从 .mmdb 文件中解析数据。
type GeoIpRecord struct {
	Country struct {
		ISOCode string            `maxminddb:"iso_code"`
		Names   map[string]string `maxminddb:"names"`
	} `maxminddb:"country"`
}

// MaxMindGeoIPService 是 GeoIPService 接口的一个具体实现，
// 它使用 MaxMind 数据库作为后端。
type MaxMindGeoIPService struct {
	// maxMindDBReader 内部持有的 MaxMind 数据库读取器实例。
	maxMindDBReader *maxminddb.Reader
	// dbFilePath 是 MaxMind 数据库文件的路径。
	dbFilePath string
	// mu 用于保护对 maxMindDBReader 的并发访问，确保线程安全。
	mu sync.RWMutex
}

// Name 返回服务的名称。
func (s *MaxMindGeoIPService) Name() string {
	return "MaxMind"
}

// NewMaxMindGeoIPService 创建并返回一个 MaxMindGeoIPService 实例。
// 它负责初始化服务，包括尝试加载或下载数据库。
func NewMaxMindGeoIPService() (*MaxMindGeoIPService, error) {
	dbFilePath := GeoIpFilePath
	service := &MaxMindGeoIPService{
		dbFilePath: dbFilePath,
	}

	// 确保数据目录存在
	if err := os.MkdirAll(filepath.Dir(dbFilePath), os.ModePerm); err != nil {
		auditlog.Log("", "", "Failed to create data directory for MaxMind database: "+err.Error(), "error")
		return nil, fmt.Errorf("failed to create data directory for MaxMind database: %w", err)
	}

	// 检查数据库文件是否存在，如果不存在则尝试下载
	if _, err := os.Stat(dbFilePath); os.IsNotExist(err) {
		if err := service.UpdateDatabase(); err != nil {
			auditlog.Log("", "", "Failed to download initial MaxMind database: "+err.Error(), "error")
			return nil, fmt.Errorf("failed to download initial MaxMind database: %w", err)
		}
	}

	// 初始化或重新加载 MaxMind 数据库。
	if err := service.initialize(); err != nil {
		auditlog.Log("", "", "Failed to initialize MaxMind database: "+err.Error(), "error")
		return nil, fmt.Errorf("failed to initialize MaxMind database: %w", err)
	}
	return service, nil
}

// initialize 初始化或重新加载 MaxMind 数据库。
// 这是一个内部方法，供 NewMaxMindGeoIPService 和 UpdateDatabase 调用。
// 它会关闭现有连接（如果存在）并重新打开数据库文件。
func (s *MaxMindGeoIPService) initialize() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 如果已存在数据库读取器，则先关闭它。
	if s.maxMindDBReader != nil {
		s.maxMindDBReader.Close()
		s.maxMindDBReader = nil
	}

	// 尝试打开新的数据库文件。
	reader, err := maxminddb.Open(s.dbFilePath)
	if err != nil {
		return fmt.Errorf("error opening MaxMind database at %s: %w", s.dbFilePath, err)
	}
	s.maxMindDBReader = reader
	return nil
}

// GetGeoInfo 根据 IP 地址获取 MaxMind 的地理位置信息。
// 它查询 MaxMind 数据库并将其特有的 GeoIpRecord 转换为通用的 GeoInfo 结构体。
func (s *MaxMindGeoIPService) GetGeoInfo(ip net.IP) (*GeoInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.maxMindDBReader == nil {
		return nil, fmt.Errorf("MaxMind database is not initialized or failed to open")
	}
	if ip == nil {
		return nil, fmt.Errorf("IP address cannot be nil")
	}

	var record GeoIpRecord // 使用原始的 GeoIpRecord 结构体来接收查询结果
	err := s.maxMindDBReader.Lookup(ip, &record)
	if err != nil {
		// 返回错误，但避免直接返回 maxminddb 库的内部错误，提供更友好的信息
		return nil, fmt.Errorf("error looking up IP %s in MaxMind database: %w", ip.String(), err)
	}

	// 将 MaxMind 的特定结构体转换为通用的 GeoInfo 结构体
	geoInfo := &GeoInfo{
		ISOCode: record.Country.ISOCode,
		// 尝试获取英文国家名称，如果不存在则使用 ISO 代码作为备用
		Name: record.Country.Names["en"],
	}
	if geoInfo.Name == "" && geoInfo.ISOCode != "" {
		geoInfo.Name = geoInfo.ISOCode // 如果没有英文名称，回退到 ISO 代码
	}
	return geoInfo, nil
}

// UpdateDatabase 实现了 GeoIPService 接口的 UpdateDatabase 方法。
// 它会下载最新的 GeoLite2-Country.mmdb 文件并重新加载数据库。
//
// 下载与替换在 downloadToTempFile 中完成，写锁的作用域就在那个函数里；initialize
// 自己也会取写锁，所以必须等它返回（锁已释放）之后再调用 —— 两件事不能在同一层
// defer 下，否则就是自死锁（原代码在成功路径上显式 Unlock 正是为了避开它）。
func (s *MaxMindGeoIPService) UpdateDatabase() error {
	if err := s.downloadToTempFile(); err != nil {
		return err
	}
	// 重新加载数据库以使用新下载的文件
	return s.initialize()
}

// downloadToTempFile 持写锁完成下载、校验与原子替换。
//
// 写锁的全部作用域就是这一个函数，所以五条早退路径都会经 defer 释放它。原来的写法把
// Lock 放在 UpdateDatabase 顶部、Unlock 放在唯一的成功路径上，于是任何一次下载失败都会
// 让写锁一直被握着，GetGeoInfo（RLock）随之永久阻塞 —— 而它在 agent 上报基本信息的
// 同步路径上，表现为整个上报流程卡死。
func (s *MaxMindGeoIPService) downloadToTempFile() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(GeoIpUrl)
	if err != nil {
		return fmt.Errorf("failed to initiate MaxMind database download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download MaxMind database: HTTP status %s", resp.Status)
	}

	// 确保数据目录存在（NewMaxMindGeoIPService 已处理，但这里再次确保以防直接调用）
	if err := os.MkdirAll(filepath.Dir(s.dbFilePath), os.ModePerm); err != nil {
		return fmt.Errorf("failed to create data directory for MaxMind database update: %w", err)
	}

	// 先写临时文件、校验通过后再原子替换线上库。旧的 os.Create(s.dbFilePath) 会先把
	// 线上库截断成 0 字节，随后的拷贝一旦失败（或下载到的其实是一段错误页），可用的库
	// 就被换成了一个空文件或垃圾内容。
	tmp := s.dbFilePath + ".new"
	out, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("failed to create temporary MaxMind database file at %s: %w", tmp, err)
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("failed to write MaxMind database file: %w", err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("failed to close temporary MaxMind database file at %s: %w", tmp, err)
	}
	if err := validateMmdb(tmp); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, s.dbFilePath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("failed to replace MaxMind database at %s: %w", s.dbFilePath, err)
	}
	return nil
}

// validateMmdb 拒绝空文件，以及无法作为 MaxMind 数据库打开的文件。
//
// 下载失败最常见的样子并不是 HTTP 错误，而是一个 200 加一段 HTML 错误页或截断的响应；
// 那种内容一旦写进线上路径，下次启动打开的就是一个坏库。
func validateMmdb(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("downloaded MaxMind database is unreadable: %w", err)
	}
	if info.Size() == 0 {
		return fmt.Errorf("downloaded MaxMind database is empty")
	}
	reader, err := maxminddb.Open(path)
	if err != nil {
		return fmt.Errorf("downloaded file is not a usable MaxMind database: %w", err)
	}
	_ = reader.Close()
	return nil
}

// Close 实现了 GeoIPService 接口的 Close 方法。
// 它关闭 MaxMind 数据库读取器，释放文件句柄和其他资源。
func (s *MaxMindGeoIPService) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.maxMindDBReader != nil {
		err := s.maxMindDBReader.Close()
		s.maxMindDBReader = nil // 清空读取器实例
		if err != nil {
			return fmt.Errorf("error closing MaxMind database: %w", err)
		}
	}
	logger.InfoArgs("geoip", "MaxMind GeoIP service closed.")
	return nil
}
