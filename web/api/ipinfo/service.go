package ipinfo

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// 默认上游地址。ip-api.com 免费接口只有明文 HTTP，这里不能写成 https。
const (
	defaultIPWhoIsBaseURL    = "https://ipwho.is"
	defaultRIPEstatBaseURL   = "https://stat.ripe.net"
	defaultIPAPIBaseURL      = "http://ip-api.com"
	defaultGlobalpingBaseURL = "https://api.globalping.io"
)

// 环境变量开关，沿用仓库既有的 KOMARI_* 风格。
const (
	envDisableGlobalLatency = "NEKOMARI_IP_INFO_DISABLE_LATENCY"
	envGlobalpingToken      = "NEKOMARI_GLOBALPING_TOKEN"
)

// reputationMethodID 是风险分算法的标识，会写进 reputation.method.id。
const reputationMethodID = "nekomari-flags-v1"

// errLatencyDisabled 表示本进程关闭了全球延迟测量。
var errLatencyDisabled = errors.New("global latency measurement is disabled")

// Config 是服务的可调参数。零值字段会在 normalized() 里补默认值，
// 因此测试可以只写关心的几项（例如只替换上游 baseURL）。
type Config struct {
	IPWhoIsBaseURL    string
	RIPEstatBaseURL   string
	IPAPIBaseURL      string
	GlobalpingBaseURL string

	// GlobalpingToken 可选：公共 API 免 token，限流时带 token 额度更高。
	GlobalpingToken string
	// GlobalLatencyDisabled 关闭 Globalping 测量，同时让 /status 声明不支持。
	GlobalLatencyDisabled bool

	// HTTPClient 是所有上游共用的客户端；必须带显式超时。
	HTTPClient *http.Client
	// Now 可注入时钟，测试用。
	Now func() time.Time

	// 缓存时长。
	PositiveTTL        time.Duration // 成功结果的新鲜期，契约要求 >= 1h
	StaleTTL           time.Duration // 新鲜期结束后仍保留用于兜底的时长
	NegativeTTL        time.Duration // 失败结果的新鲜期（短暂，避免反复打上游）
	LatencyTTL         time.Duration // 延迟结果的新鲜期
	LatencyNegativeTTL time.Duration // 延迟失败的短缓存
	LookupCacheSize    int
	LatencyCacheSize   int

	// Globalping 轮询参数。
	GlobalpingPollInterval time.Duration
	GlobalpingMaxWait      time.Duration
}

// DefaultConfig 返回生产默认配置。
func DefaultConfig() Config {
	return Config{
		IPWhoIsBaseURL:    defaultIPWhoIsBaseURL,
		RIPEstatBaseURL:   defaultRIPEstatBaseURL,
		IPAPIBaseURL:      defaultIPAPIBaseURL,
		GlobalpingBaseURL: defaultGlobalpingBaseURL,
		GlobalpingToken:   strings.TrimSpace(os.Getenv(envGlobalpingToken)),
		// 公共 API 可能限流或需要 token，给运维一个不改代码就能关掉的口子。
		GlobalLatencyDisabled:  isTruthyEnv(envDisableGlobalLatency),
		HTTPClient:             &http.Client{Timeout: defaultHTTPTimeout},
		Now:                    time.Now,
		PositiveTTL:            time.Hour,
		StaleTTL:               24 * time.Hour,
		NegativeTTL:            5 * time.Minute,
		LatencyTTL:             10 * time.Minute,
		LatencyNegativeTTL:     2 * time.Minute,
		LookupCacheSize:        4096,
		LatencyCacheSize:       2048,
		GlobalpingPollInterval: time.Second,
		GlobalpingMaxWait:      20 * time.Second,
	}
}

func isTruthyEnv(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func (c Config) normalized() Config {
	if c.IPWhoIsBaseURL == "" {
		c.IPWhoIsBaseURL = defaultIPWhoIsBaseURL
	}
	if c.RIPEstatBaseURL == "" {
		c.RIPEstatBaseURL = defaultRIPEstatBaseURL
	}
	if c.IPAPIBaseURL == "" {
		c.IPAPIBaseURL = defaultIPAPIBaseURL
	}
	if c.GlobalpingBaseURL == "" {
		c.GlobalpingBaseURL = defaultGlobalpingBaseURL
	}
	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{Timeout: defaultHTTPTimeout}
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.PositiveTTL <= 0 {
		c.PositiveTTL = time.Hour
	}
	if c.StaleTTL <= 0 {
		c.StaleTTL = 24 * time.Hour
	}
	if c.NegativeTTL <= 0 {
		c.NegativeTTL = 5 * time.Minute
	}
	if c.LatencyTTL <= 0 {
		c.LatencyTTL = 10 * time.Minute
	}
	if c.LatencyNegativeTTL <= 0 {
		c.LatencyNegativeTTL = 2 * time.Minute
	}
	if c.LookupCacheSize <= 0 {
		c.LookupCacheSize = 4096
	}
	if c.LatencyCacheSize <= 0 {
		c.LatencyCacheSize = 2048
	}
	if c.GlobalpingPollInterval <= 0 {
		c.GlobalpingPollInterval = time.Second
	}
	if c.GlobalpingMaxWait <= 0 {
		c.GlobalpingMaxWait = 20 * time.Second
	}
	return c
}

// lookupSnapshot 是缓存里存的查询结果（不含随请求变化的 uuid）。
type lookupSnapshot struct {
	Location       Location
	Network        Network
	Classification Classification
	Reputation     Reputation
	Provider       LookupProvider
	// Warning 记录本次聚合过程中部分上游失败的提示，会写进 meta.warning。
	Warning string
}

// latencySnapshot 是缓存里存的延迟结果。
type latencySnapshot struct {
	Classification Classification
	Nodes          []LatencyNode
	AvailableCount int
	TimeoutCount   int
	Provider       LatencyProvider
	Warning        string
}

// negativeEntry 是「失败」的负缓存，只存错误信息。
type negativeEntry struct {
	message string
}

// Service 聚合各上游并对外提供带缓存的查询。
type Service struct {
	cfg        Config
	geo        *ipWhoIsProvider
	ripe       *ripeStatProvider
	ipapi      *ipAPIProvider
	globalping *globalpingProvider

	lookups   *ttlCache[lookupSnapshot]
	latency   *ttlCache[latencySnapshot]
	negatives *ttlCache[negativeEntry]
}

// NewService 构造服务。所有上游共享同一个 HTTP 客户端（各自带超时），
// 服务本身不起后台 goroutine、不注册定时器，因此不需要 Close。
func NewService(cfg Config) *Service {
	cfg = cfg.normalized()
	return &Service{
		cfg:        cfg,
		geo:        newIPWhoIsProvider(cfg.IPWhoIsBaseURL, cfg.HTTPClient),
		ripe:       newRIPEstatProvider(cfg.RIPEstatBaseURL, cfg.HTTPClient),
		ipapi:      newIPAPIProvider(cfg.IPAPIBaseURL, cfg.HTTPClient, cfg.Now),
		globalping: newGlobalpingProvider(cfg.GlobalpingBaseURL, cfg.HTTPClient, cfg.GlobalpingToken, cfg.GlobalpingPollInterval, cfg.GlobalpingMaxWait),
		lookups:    newTTLCache[lookupSnapshot](cfg.LookupCacheSize, cfg.Now),
		latency:    newTTLCache[latencySnapshot](cfg.LatencyCacheSize, cfg.Now),
		negatives:  newTTLCache[negativeEntry](cfg.LookupCacheSize, cfg.Now),
	}
}

// Status 组装 /status 的 data。能力位反映本后端的真实能力：
// geo/network/reputation 由固定上游提供，native_classification 由
// 「地理国家 vs ASN 注册国家」推导，global_latency 取决于 Globalping 是否启用；
// media_unlock / ai_unlock 没有数据源，恒为 false。
func (s *Service) Status() StatusData {
	return StatusData{
		Available:             true,
		Version:               APIVersion,
		SchemaVersion:         SchemaVersion,
		MainlandChinaExcluded: false,
		Capabilities: StatusCapabilities{
			Geo:                  true,
			Network:              true,
			Reputation:           true,
			NativeClassification: true,
			GlobalLatency:        !s.cfg.GlobalLatencyDisabled,
			MediaUnlock:          false,
			AIUnlock:             false,
		},
	}
}

// Lookup 返回某个 IP 的完整信息快照。
//
// 缓存策略：
//   - force=false 且命中新鲜条目 -> 直接返回，meta.cache=hit；
//   - 否则并发查询上游：只要有任意一个上游给出数据就算成功，写入正缓存（meta.cache=miss）；
//   - 上游全挂时，若还有「过期但保留」的条目就兜底返回（meta.stale=true），
//     否则写入短暂负缓存并返回错误。
func (s *Service) Lookup(ctx context.Context, ip net.IP, force bool) (lookupSnapshot, Meta, error) {
	key := ip.String()

	if !force {
		if hit, ok := s.lookups.get(key); ok && hit.Fresh {
			// 缓存条目自带「上次有哪些上游没答上来」的提示，命中时一并透出。
			return hit.Value, metaFromCache(hit, CacheHit, hit.Value.Warning), nil
		}
		if hit, ok := s.negatives.get(key); ok && hit.Fresh {
			return lookupSnapshot{}, Meta{}, errors.New(hit.Value.message)
		}
	}

	snapshot, err := s.query(ctx, ip)
	if err != nil {
		if hit, ok := s.lookups.get(key); ok {
			warning := joinWarnings(hit.Value.Warning,
				"upstream lookup failed; serving a stale cached result: "+err.Error())
			return hit.Value, metaFromCache(hit, CacheHit, warning), nil
		}
		s.negatives.set(key, negativeEntry{message: err.Error()}, s.cfg.NegativeTTL, 0)
		return lookupSnapshot{}, Meta{}, err
	}

	s.negatives.delete(key)
	s.lookups.set(key, snapshot, s.cfg.PositiveTTL, s.cfg.StaleTTL)
	hit, _ := s.lookups.get(key)
	return snapshot, metaFromCache(hit, CacheMiss, snapshot.Warning), nil
}

// query 并发调用三个互不依赖的上游，再做两次有条件的补充查询。
func (s *Service) query(ctx context.Context, ip net.IP) (lookupSnapshot, error) {
	var (
		wg sync.WaitGroup

		geo      *geoRecord
		geoErr   error
		prefix   string
		asns     []int
		netErr   error
		flags    *ipAPIFlags
		flagsErr error
	)

	wg.Add(3)
	go func() {
		defer wg.Done()
		geo, geoErr = s.geo.lookup(ctx, ip)
	}()
	go func() {
		defer wg.Done()
		prefix, asns, netErr = s.ripe.networkInfo(ctx, ip)
	}()
	go func() {
		defer wg.Done()
		flags, flagsErr = s.ipapi.lookup(ctx, ip)
	}()
	wg.Wait()

	// warnings 汇总到 meta.warning；sources/failed 落到 reputation 的两个列表。
	warnings := make([]string, 0, 4)
	ripeAnswered := netErr == nil
	if netErr != nil {
		warnings = append(warnings, netErr.Error())
	}
	if geoErr != nil {
		warnings = append(warnings, geoErr.Error())
	}
	if flagsErr != nil {
		warnings = append(warnings, flagsErr.Error())
	}

	// 三个上游全挂才算查询失败；只要有一个答上来就降级返回。
	if geo == nil && netErr != nil && flags == nil {
		return lookupSnapshot{}, fmt.Errorf("all upstream sources failed: %s", strings.Join(warnings, "; "))
	}

	// ASN 优先取 ipwho.is，缺了再用 RIPEstat 的 asns[0]。
	asnNumber := 0
	if geo != nil && geo.ASNNumber > 0 {
		asnNumber = geo.ASNNumber
	} else if len(asns) > 0 {
		asnNumber = asns[0]
	}

	// whois 依赖 ASN，只能串行；失败不影响已拿到的前缀数据。
	var registration *asnRegistration
	if asnNumber > 0 {
		got, err := s.ripe.asnRegistration(ctx, asnNumber)
		if err != nil {
			warnings = append(warnings, err.Error())
		} else {
			registration = got
			ripeAnswered = true
		}
	}

	domain := ""
	if geo != nil {
		domain = strings.TrimSpace(geo.Domain)
	}
	// ipwho.is 没给域名时才去问反向解析，省一次上游调用。
	if domain == "" {
		if ptr, err := s.ripe.reverseDNS(ctx, ip); err == nil && ptr != "" {
			domain = domainFromPTR(ptr)
			ripeAnswered = true
		}
	}

	countryCode := ""
	if geo != nil {
		countryCode = strings.ToUpper(strings.TrimSpace(geo.CountryCode))
	}
	registeredCode := ""
	rir := ""
	if registration != nil {
		registeredCode = registration.Country
		rir = registration.RIR
	}

	sources := make([]string, 0, 3)
	failed := make([]string, 0, 3)
	if geo != nil {
		sources = append(sources, sourceIPWhoIs)
	} else {
		failed = append(failed, sourceIPWhoIs)
	}
	if ripeAnswered {
		sources = append(sources, sourceRIPEstat)
	} else {
		failed = append(failed, sourceRIPEstat)
	}
	if flags != nil {
		sources = append(sources, sourceIPAPI)
	} else {
		failed = append(failed, sourceIPAPI)
	}

	return lookupSnapshot{
		Location:       buildLocation(geo, registeredCode),
		Network:        buildNetwork(geo, flags, prefix, asnNumber, domain, rir),
		Classification: deriveClassification(countryCode, registeredCode),
		Reputation:     buildReputation(flags, countryCode, sources, failed),
		Provider:       buildProvider(flags != nil),
		Warning:        strings.Join(warnings, "; "),
	}, nil
}

// Latency 返回全球延迟快照。任何上游失败都只降级成「节点为空 + warning」，
// 不返回错误 —— 契约要求这个接口不能硬失败。
func (s *Service) Latency(ctx context.Context, ip net.IP, force bool) (latencySnapshot, Meta) {
	key := ip.String()

	if !force {
		if hit, ok := s.latency.get(key); ok && hit.Fresh {
			// 失败结果的短缓存里也带着原因，命中时同样要透出。
			return hit.Value, metaFromCache(hit, CacheHit, hit.Value.Warning)
		}
	}

	var (
		wg             sync.WaitGroup
		classification = deriveClassification("", "")
		nodes          []LatencyNode
		warning        string
		latencyErr     error
	)

	// 分类数据走普通查询（通常直接命中缓存），与测量并发，避免串行叠加时延。
	wg.Add(2)
	go func() {
		defer wg.Done()
		snapshot, _, err := s.Lookup(ctx, ip, false)
		if err == nil {
			classification = snapshot.Classification
		}
	}()
	go func() {
		defer wg.Done()
		nodes, warning, latencyErr = s.measureLatency(ctx, ip)
	}()
	wg.Wait()

	if latencyErr != nil {
		// 有兜底条目就返回它，否则返回空节点集合并说明原因。
		if hit, ok := s.latency.get(key); ok {
			staleWarning := joinWarnings(hit.Value.Warning,
				"global latency measurement failed; serving a stale cached result: "+latencyErr.Error())
			return hit.Value, metaFromCache(hit, CacheHit, staleWarning)
		}
		snapshot := latencySnapshot{
			Classification: classification,
			Nodes:          []LatencyNode{},
			Provider:       s.latencyProvider(classification, false),
			Warning:        warning,
		}
		s.latency.set(key, snapshot, s.cfg.LatencyNegativeTTL, 0)
		hit, _ := s.latency.get(key)
		return snapshot, metaFromCache(hit, CacheMiss, warning)
	}

	snapshot := latencySnapshot{
		Classification: classification,
		Nodes:          nodes,
		AvailableCount: countLatencyStatus(nodes, LatencyStatusOK),
		TimeoutCount:   countLatencyStatus(nodes, LatencyStatusTimeout),
		Provider:       s.latencyProvider(classification, len(nodes) > 0),
	}
	s.latency.set(key, snapshot, s.cfg.LatencyTTL, s.cfg.StaleTTL)
	hit, _ := s.latency.get(key)
	return snapshot, metaFromCache(hit, CacheMiss, snapshot.Warning)
}

// measureLatency 执行一次 Globalping 测量；失败时返回空节点集合与提示。
func (s *Service) measureLatency(ctx context.Context, ip net.IP) ([]LatencyNode, string, error) {
	if s.cfg.GlobalLatencyDisabled {
		return []LatencyNode{}, errLatencyDisabled.Error(), errLatencyDisabled
	}
	probes, err := s.globalping.measure(ctx, ip)
	if err != nil {
		return []LatencyNode{}, "global latency measurement failed: " + err.Error(), err
	}
	return latencyNodes(probes), "", nil
}

func (s *Service) latencyProvider(classification Classification, latencyAvailable bool) LatencyProvider {
	return LatencyProvider{
		ID:       "globalping",
		Name:     "Globalping",
		Homepage: "https://globalping.io",
		// 分类数据来自 ipwho.is + RIPEstat，与延迟测量本身相互独立。
		ClassificationAvailable: classification.Type != ClassificationUnknown,
		LatencyAvailable:        latencyAvailable,
	}
}

// buildLocation 组装地理信息。registered_country 留空：
// RIPEstat 只给 ASN 的注册国家代码，没有国家名，宁缺勿造。
func buildLocation(geo *geoRecord, registeredCode string) Location {
	location := Location{
		RegisteredCountryCode: registeredCode,
	}
	if geo == nil {
		return location
	}
	location.Continent = geo.Continent
	location.ContinentCode = strings.ToUpper(strings.TrimSpace(geo.ContinentCode))
	location.Country = geo.Country
	location.CountryCode = strings.ToUpper(strings.TrimSpace(geo.CountryCode))
	location.Region = geo.Region
	location.RegionCode = geo.RegionCode
	location.City = geo.City
	location.PostalCode = geo.PostalCode
	location.Timezone = geo.Timezone
	location.Latitude = geo.Latitude
	location.Longitude = geo.Longitude
	return location
}

// buildNetwork 组装网络归属信息。
//
// network_type / company_type 由 ip-api.com 的 mobile / hosting 布尔推导；
// datacenter 在 hosting 为真时填运营商名（上游没有单独的机房名字段）；
// rir 来自 RIPEstat whois 的 source 字段。
func buildNetwork(geo *geoRecord, flags *ipAPIFlags, prefix string, asnNumber int, domain, rir string) Network {
	network := Network{
		Route:  prefix,
		Domain: domain,
		RIR:    rir,
	}
	if asnNumber > 0 {
		network.ASNNumber = asnNumber
		network.ASN = fmt.Sprintf("AS%d", asnNumber)
	}
	if geo != nil {
		network.Organization = firstNonEmpty(geo.Org, geo.ISP)
		network.Operator = firstNonEmpty(geo.ISP, geo.Org)
	}
	if flags != nil {
		if flags.Mobile {
			network.NetworkType = "mobile"
		}
		if flags.Hosting {
			network.CompanyType = "hosting"
			network.Datacenter = firstNonEmpty(network.Organization, network.Operator)
		}
	}
	return network
}

// buildProvider 描述数据来源。quality_sources 是固定配置的补充源，
// 本次是否真的答上来了看 reputation.available_sources / failed_sources。
func buildProvider(securityDataAvailable bool) LookupProvider {
	return LookupProvider{
		ID:                    "nekomari-ip-info",
		Name:                  "Nekomari IP Info",
		Homepage:              "https://github.com/Aone2233/nekomari",
		BaseSource:            sourceIPWhoIs,
		QualitySources:        []string{sourceRIPEstat, sourceIPAPI},
		SecurityDataAvailable: securityDataAvailable,
	}
}

// deriveClassification 由「地理定位国家」与「ASN 注册国家」推导原生/广播：
//   - 两者都拿到且相同 -> native / 原生 IP
//   - 两者都拿到但不同   -> broadcast / 广播 IP
//   - 任一缺失           -> unknown / 未知
//
// Source 恒为非空字符串：主题把 classification.source 定义成必填 string，缺了整条
// 响应都会被判为无效（HTTP 仍 200，面板静默不渲染）。取值沿用参考实现的约定。
func deriveClassification(geolocatedCode, registeredCode string) Classification {
	geolocated := strings.ToUpper(strings.TrimSpace(geolocatedCode))
	registered := strings.ToUpper(strings.TrimSpace(registeredCode))

	result := Classification{
		GeolocatedCountryCode: geolocated,
		RegisteredCountryCode: registered,
	}
	switch {
	case geolocated == "" || registered == "":
		result.Type, result.Label = ClassificationUnknown, LabelUnknown
		result.Source = ClassificationSourceUnavailable
	case geolocated == registered:
		result.Type, result.Label = ClassificationNative, LabelNative
		result.Source = ClassificationSourceCountryComparison
	default:
		result.Type, result.Label = ClassificationBroadcast, LabelBroadcast
		result.Source = ClassificationSourceCountryComparison
	}
	return result
}

// buildReputation 由布尔信号推导风险分。
//
// 风险分公式（权重之和恰为 100，因此命中全部信号正好是满分，无需再封顶）：
//
//	tor        +30  出口节点，几乎必然被风控
//	abuser     +30  有滥用记录
//	proxy      +20  公开代理
//	vpn        +15  VPN 出口
//	datacenter +5   机房/云厂商网段（本身中性，权重最低）
//
//	risk_score   = Σ 命中权重        （0..100）
//	purity_score = 100 - risk_score
//
// risk_level 在 risk_score=0 时留空，表示「没有命中任何风险信号」；
// pollution_score / pollution_level 恒为 0 / ""：判断「污染」需要邮件黑名单或
// 污染库，当前几个数据源都不提供，宁可不填也不编造。
//
// valid_signal_count = 6（ip-api.com 答上来时，六个布尔信号都有确定取值）；
// positive_signal_count = 其中判定为「干净」的个数。
func buildReputation(flags *ipAPIFlags, countryCode string, sources, failed []string) Reputation {
	reputation := Reputation{
		DatabaseScores:   map[string]any{},
		DatabaseSignals:  map[string]any{},
		AvailableSources: append([]string{}, sources...),
		FailedSources:    append([]string{}, failed...),
		Signals:          ReputationSignals{CountryCode: countryCode},
		Method:           ReputationMethod{ID: reputationMethodID, Status: "unavailable"},
	}
	if flags == nil {
		// 拿不到布尔信号时给中性分：不冤枉 IP，也不谎称干净（risk_level 留空=无数据）。
		reputation.PurityScore = 100
		return reputation
	}

	reputation.Available = true
	reputation.Signals.Proxy = flags.Proxy
	reputation.Signals.Datacenter = flags.Hosting
	// ip-api.com 免费接口只报告 hosting / mobile / proxy，
	// tor / vpn / abuser / crawler 一律按「未命中」处理。
	reputation.Signals.Tor = false
	reputation.Signals.VPN = false
	reputation.Signals.Abuser = false
	reputation.Signals.Crawler = false

	risk := 0
	if reputation.Signals.Tor {
		risk += 30
	}
	if reputation.Signals.Abuser {
		risk += 30
	}
	if reputation.Signals.Proxy {
		risk += 20
	}
	if reputation.Signals.VPN {
		risk += 15
	}
	if reputation.Signals.Datacenter {
		risk += 5
	}
	if risk > 100 {
		risk = 100
	}

	reputation.RiskScore = risk
	reputation.PurityScore = 100 - risk
	reputation.RiskLevel = riskLevel(risk)
	reputation.ValidSignalCount = 6
	positive := 0
	for _, hit := range []bool{
		reputation.Signals.Proxy,
		reputation.Signals.Tor,
		reputation.Signals.VPN,
		reputation.Signals.Datacenter,
		reputation.Signals.Abuser,
		reputation.Signals.Crawler,
	} {
		if !hit {
			positive++
		}
	}
	reputation.PositiveSignalCount = positive

	reputation.Method.Status = "ok"
	if len(failed) > 0 {
		// 有源没答上来时，风险分只覆盖了部分来源，标成 partial 更诚实。
		reputation.Method.Status = "partial"
	}
	return reputation
}

// riskLevel 把风险分映射成等级标签；0 分返回空串表示「无风险信号」。
func riskLevel(risk int) string {
	switch {
	case risk <= 0:
		return ""
	case risk < 30:
		return "low"
	case risk < 60:
		return "medium"
	case risk < 80:
		return "high"
	default:
		return "critical"
	}
}

func countLatencyStatus(nodes []LatencyNode, status string) int {
	count := 0
	for _, node := range nodes {
		if node.Status == status {
			count++
		}
	}
	return count
}

// metaFromCache 把缓存命中信息翻译成契约里的 meta。
func metaFromCache[T any](hit cacheHit[T], cache string, warning string) Meta {
	meta := Meta{
		Cache:      cache,
		Stale:      !hit.Fresh,
		UpdatedAt:  hit.UpdatedAt.UTC().Format(time.RFC3339),
		ExpiresAt:  hit.ExpiresAt.UTC().Format(time.RFC3339),
		StaleUntil: hit.StaleUntil.UTC().Format(time.RFC3339),
	}
	if warning != "" {
		meta.Warning = &warning
	}
	return meta
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// joinWarnings 用 "; " 拼接非空提示，供 meta.warning 使用。
func joinWarnings(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			kept = append(kept, trimmed)
		}
	}
	return strings.Join(kept, "; ")
}
