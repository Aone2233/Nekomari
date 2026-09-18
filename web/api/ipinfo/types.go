// Package ipinfo 实现第三方主题（LuminaPlus）所需的「IP 信息」HTTP 接口。
//
// 契约：GET /api/public/ip-info/v1/{status,lookup,latency} 与
// POST /api/admin/ip-info/v1/refresh，详见 docs/IP-INFO-API.md。
//
// 注意：这里的响应外壳是 {ok,data,meta} / {error:{message}}，与仓库内 RPC2 的
// {status,message,data} 不同。主题用严格 schema 校验字段名、类型与可空性，
// 所以本包单独维护一套 DTO，刻意不复用 web/api 的 Respond* 外壳。
package ipinfo

import (
	v2 "github.com/Aone2233/nekomari/protocol/v2"
)

const (
	// APIVersion 是接口的语义版本，经 /status 暴露给主题。
	APIVersion = "1.0.0"
	// SchemaVersion 是响应结构的版本号，所有 data 都会回显它。
	SchemaVersion = 1

	// CacheHit / CacheMiss 是 meta.cache 的两个取值。
	CacheHit  = "hit"
	CacheMiss = "miss"
)

// 分类（classification.type）的三个取值与对应中文标签。
const (
	ClassificationNative    = "native"
	ClassificationBroadcast = "broadcast"
	ClassificationUnknown   = "unknown"

	LabelNative    = "原生 IP"
	LabelBroadcast = "广播 IP"
	LabelUnknown   = "未知"
)

// classification.source 的取值：说明本次判定是「怎么来的」，不是「用了哪个数据源」。
//
// 这两个字符串来自参考实现（shanyang242/Komari-IP-Info 的
// normalizeNativeClassification），主题的 zod schema 把 source 定义成**必填**字符串：
//
//	source: d()   // z.string()，无 optional、无 default
//
// 缺了它整条 /lookup 响应会被主题判为无效 —— HTTP 仍然是 200，只是查询报错、
// 面板不渲染。所以这两个常量不能删，也不能留空。
const (
	ClassificationSourceCountryComparison = "country_comparison"
	ClassificationSourceUnavailable       = "unavailable"
)

// 延迟节点状态，只能是这三个值之一。
const (
	LatencyStatusOK          = "ok"
	LatencyStatusTimeout     = "timeout"
	LatencyStatusUnavailable = "unavailable"
)

// Envelope 是统一响应外壳：成功为 {ok:true,data,meta}，失败为 {error:{message}}。
type Envelope struct {
	OK   bool        `json:"ok"`
	Data interface{} `json:"data,omitempty"`
	Meta interface{} `json:"meta,omitempty"`
}

// ErrorBody 是失败响应体，主题读取 error.message。
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail 承载失败原因。
type ErrorDetail struct {
	Message string `json:"message"`
}

// StatusData 是 /status 的 data。
type StatusData struct {
	Available             bool               `json:"available"`
	Version               string             `json:"version"`
	SchemaVersion         int                `json:"schema_version"`
	MainlandChinaExcluded bool               `json:"mainland_china_excluded"`
	Capabilities          StatusCapabilities `json:"capabilities"`
}

// StatusCapabilities 声明本后端支持的能力，主题据此决定显示哪些面板。
type StatusCapabilities struct {
	Geo                  bool `json:"geo"`
	Network              bool `json:"network"`
	Reputation           bool `json:"reputation"`
	NativeClassification bool `json:"native_classification"`
	GlobalLatency        bool `json:"global_latency"`
	MediaUnlock          bool `json:"media_unlock"`
	AIUnlock             bool `json:"ai_unlock"`
}

// Address 是被查询地址本身。
type Address struct {
	Value  string `json:"value"`
	Family int    `json:"family"` // 只允许 4 或 6
}

// Location 是地理信息，字段全部来自 ipwho.is。
type Location struct {
	Continent             string  `json:"continent"`
	ContinentCode         string  `json:"continent_code"`
	Country               string  `json:"country"`
	CountryCode           string  `json:"country_code"`
	RegisteredCountry     string  `json:"registered_country"`
	RegisteredCountryCode string  `json:"registered_country_code"`
	Region                string  `json:"region"`
	RegionCode            string  `json:"region_code"`
	City                  string  `json:"city"`
	PostalCode            string  `json:"postal_code"`
	Timezone              string  `json:"timezone"`
	Latitude              float64 `json:"latitude"`
	Longitude             float64 `json:"longitude"`
	AccuracyRadius        int     `json:"accuracy_radius"`
}

// Network 是网络归属信息，ASN 来自 ipwho.is，route 来自 RIPEstat。
type Network struct {
	ASN          string `json:"asn"`        // 形如 "AS123"，无数据时为空串
	ASNNumber    int    `json:"asn_number"` // 形如 123，无数据时为 0
	Organization string `json:"organization"`
	Operator     string `json:"operator"`
	NetworkType  string `json:"network_type"`
	CompanyType  string `json:"company_type"`
	Route        string `json:"route"` // 形如 "1.2.3.0/24"
	RIR          string `json:"rir"`
	Domain       string `json:"domain"`
	Datacenter   string `json:"datacenter"`
}

// Classification 是原生/广播判定结果。
//
// Source 是主题 schema 的**必填**字段（见 ClassificationSource* 的注释）。它描述判定
// 依据，取值 "country_comparison"（比较地理定位国家与 ASN 注册国家）或 "unavailable"
// （任一国家未知，无法比较）。
type Classification struct {
	Type                  string `json:"type"`
	Label                 string `json:"label"`
	GeolocatedCountryCode string `json:"geolocated_country_code"`
	RegisteredCountryCode string `json:"registered_country_code"`
	Source                string `json:"source"`
}

// ReputationSignals 是逐项风险信号。
type ReputationSignals struct {
	CountryCode string `json:"country_code"`
	Proxy       bool   `json:"proxy"`
	Tor         bool   `json:"tor"`
	VPN         bool   `json:"vpn"`
	Datacenter  bool   `json:"datacenter"`
	Abuser      bool   `json:"abuser"`
	Crawler     bool   `json:"crawler"`
}

// ReputationMethod 描述风险分的算法与本次计算状态。
type ReputationMethod struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// Reputation 是信誉/纯净度面板数据。
//
// 注意 database_scores / database_signals / available_sources / failed_sources
// 必须是 JSON 对象/数组而非 null，因此构造时一律初始化为空集合。
type Reputation struct {
	Available           bool              `json:"available"`
	PurityScore         int               `json:"purity_score"`
	RiskScore           int               `json:"risk_score"`
	PollutionScore      int               `json:"pollution_score"`
	RiskLevel           string            `json:"risk_level"`
	PollutionLevel      string            `json:"pollution_level"`
	PositiveSignalCount int               `json:"positive_signal_count"`
	ValidSignalCount    int               `json:"valid_signal_count"`
	Signals             ReputationSignals `json:"signals"`
	DatabaseScores      map[string]any    `json:"database_scores"`
	DatabaseSignals     map[string]any    `json:"database_signals"`
	AvailableSources    []string          `json:"available_sources"`
	FailedSources       []string          `json:"failed_sources"`
	Method              ReputationMethod  `json:"method"`
}

// LookupCapabilities 是 lookup 的 data.capabilities。
type LookupCapabilities struct {
	MediaUnlock bool `json:"media_unlock"`
	AIUnlock    bool `json:"ai_unlock"`
}

// LookupProvider 描述本次数据由哪些上游提供。
type LookupProvider struct {
	ID                    string   `json:"id"`
	Name                  string   `json:"name"`
	Homepage              string   `json:"homepage"`
	BaseSource            string   `json:"base_source"`
	QualitySources        []string `json:"quality_sources"`
	SecurityDataAvailable bool     `json:"security_data_available"`
}

// UnlockData 是「流媒体 / AI 解锁」面板数据。
//
// 它由节点上的探针测量并上报，服务端只负责存与转发：解锁取决于发起请求的那个 IP，
// 服务端在别的机房，只能测到它自己的出口。没有探测记录时整个字段为 null，面板据此
// 显示「等待探测」而不是「未解锁」—— 这两件事必须分得开。
//
// EgressIP 是探测实际用的出口地址，不一定等于节点地址：本机群里有主机经由另一台
// 节点出网，不带上它就会把结论算到错误的节点上。
type UnlockData struct {
	EgressIP     string          `json:"egress_ip,omitempty"`
	EgressRegion string          `json:"egress_region,omitempty"`
	ProbedAt     string          `json:"probed_at"`
	Results      []v2.UnlockItem `json:"results"`
}

// LookupData 是 /lookup 与 /refresh 的 data。
type LookupData struct {
	UUID           string             `json:"uuid"`
	SchemaVersion  int                `json:"schema_version"`
	Excluded       bool               `json:"excluded"`
	ExcludedReason *string            `json:"excluded_reason"`
	Address        Address            `json:"address"`
	Location       Location           `json:"location"`
	Network        Network            `json:"network"`
	Classification Classification     `json:"classification"`
	Reputation     Reputation         `json:"reputation"`
	Capabilities   LookupCapabilities `json:"capabilities"`
	// Unlock 是节点探针上报的解锁快照。主题的 zod 对象会丢弃未知键，所以老主题
	// 拿到它不会报错，只是不显示。
	Unlock   *UnlockData    `json:"unlock,omitempty"`
	Provider LookupProvider `json:"provider"`
}

// Meta 是所有 data 的元信息。warning 的 schema 是 string | null，
// 因此用指针并禁用 omitempty：无警告时显式输出 null。
type Meta struct {
	Cache      string  `json:"cache"`
	Stale      bool    `json:"stale"`
	UpdatedAt  string  `json:"updated_at"`
	ExpiresAt  string  `json:"expires_at"`
	StaleUntil string  `json:"stale_until"`
	Warning    *string `json:"warning"`
}

// LatencyNode 是单个探测节点的结果。
type LatencyNode struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	City        string `json:"city"`
	CountryCode string `json:"country_code"`
	LatencyMS   int    `json:"latency_ms"`
	Status      string `json:"status"`
}

// LatencyResult 是延迟面板主体。
type LatencyResult struct {
	Nodes          []LatencyNode `json:"nodes"`
	AvailableCount int           `json:"available_count"`
	TimeoutCount   int           `json:"timeout_count"`
	ProviderCached bool          `json:"provider_cached"`
}

// LatencyProvider 描述延迟数据来源。
type LatencyProvider struct {
	ID                      string `json:"id"`
	Name                    string `json:"name"`
	Homepage                string `json:"homepage"`
	ClassificationAvailable bool   `json:"classification_available"`
	LatencyAvailable        bool   `json:"latency_available"`
}

// LatencyData 是 /latency 的 data。
type LatencyData struct {
	UUID           string          `json:"uuid"`
	SchemaVersion  int             `json:"schema_version"`
	Address        Address         `json:"address"`
	Classification Classification  `json:"classification"`
	Latency        LatencyResult   `json:"latency"`
	Provider       LatencyProvider `json:"provider"`
}

// RefreshRequest 是 POST /api/admin/ip-info/v1/refresh 的请求体。
type RefreshRequest struct {
	UUID           string `json:"uuid"`
	IP             string `json:"ip"`
	Force          *bool  `json:"force"`
	IncludeLatency bool   `json:"include_latency"`
}
