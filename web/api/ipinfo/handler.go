package ipinfo

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/Aone2233/nekomari/database/unlock"
	logger "github.com/Aone2233/nekomari/utils/log"
	"github.com/gin-gonic/gin"
)

// 两个接口各自的整体时间预算。上游客户端超时（5s）比它小，
// 这里兜住「串行调用叠加」的最坏情况，保证 handler 一定会返回。
const (
	lookupBudget  = 15 * time.Second
	latencyBudget = 25 * time.Second
)

// Handler 把 Service 挂到 gin 路由上。
type Handler struct {
	svc *Service
}

// NewHandler 用默认配置构造处理器。
func NewHandler() *Handler {
	return &Handler{svc: NewService(DefaultConfig())}
}

var (
	defaultHandlerOnce sync.Once
	defaultHandler     *Handler
)

// Default 返回进程级单例处理器。
// 公开读接口与管理刷新接口必须共享同一份缓存，否则刷新接口刷不到读接口的缓存。
func Default() *Handler {
	defaultHandlerOnce.Do(func() { defaultHandler = NewHandler() })
	return defaultHandler
}

// Status 处理 GET /api/public/ip-info/v1/status。
func (h *Handler) Status(c *gin.Context) {
	c.JSON(http.StatusOK, Envelope{OK: true, Data: h.svc.Status()})
}

// Lookup 处理 GET /api/public/ip-info/v1/lookup?uuid=&ip=。
func (h *Handler) Lookup(c *gin.Context) {
	ip, family, err := parsePublicIP(c.Query("ip"))
	if err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), lookupBudget)
	defer cancel()

	snapshot, meta, err := h.svc.Lookup(ctx, ip, false)
	if err != nil {
		if errors.Is(err, ErrBusy) {
			c.Header("Retry-After", "5")
			respondError(c, http.StatusTooManyRequests, err.Error())
			return
		}
		respondError(c, http.StatusBadGateway, "ip info lookup failed: "+err.Error())
		return
	}
	warnIfDegraded("lookup", ip, meta)
	c.JSON(http.StatusOK, Envelope{OK: true, Data: buildLookupData(c.Query("uuid"), ip, family, snapshot), Meta: meta})
}

// Latency 处理 GET /api/public/ip-info/v1/latency?uuid=&ip=。
// 该接口不会硬失败：Globalping 不可用时返回空节点集合 + meta.warning。
func (h *Handler) Latency(c *gin.Context) {
	ip, family, err := parsePublicIP(c.Query("ip"))
	if err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), latencyBudget)
	defer cancel()

	snapshot, meta := h.svc.Latency(ctx, ip, false)
	warnIfDegraded("latency", ip, meta)
	c.JSON(http.StatusOK, Envelope{
		OK:   true,
		Data: buildLatencyData(c.Query("uuid"), ip, family, snapshot, meta.Cache == CacheHit),
		Meta: meta,
	})
}

// Refresh 处理 POST /api/admin/ip-info/v1/refresh（管理员鉴权由路由中间件负责）。
//
// force 缺省视为 true：这个接口的语义就是「刷新」。显式传 force=false 时才允许命中缓存。
// include_latency=true 时顺带把延迟缓存也刷一遍，但延迟失败不影响本次返回。
func (h *Handler) Refresh(c *gin.Context) {
	var request RefreshRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		respondError(c, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	ip, family, err := parsePublicIP(request.IP)
	if err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}

	force := true
	if request.Force != nil {
		force = *request.Force
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), lookupBudget)
	defer cancel()

	snapshot, meta, err := h.svc.Lookup(ctx, ip, force)
	if err != nil {
		respondError(c, http.StatusBadGateway, "ip info refresh failed: "+err.Error())
		return
	}

	if request.IncludeLatency {
		latencyCtx, latencyCancel := context.WithTimeout(c.Request.Context(), latencyBudget)
		h.svc.Latency(latencyCtx, ip, force)
		latencyCancel()
	}

	warnIfDegraded("refresh", ip, meta)
	c.JSON(http.StatusOK, Envelope{OK: true, Data: buildLookupData(request.UUID, ip, family, snapshot), Meta: meta})
}

// buildLookupData 组装 /lookup 与 /refresh 的 data。
// excluded 恒为 false：本后端不做大陆 IP 排除，也没有排除理由。
//
// 解锁数据不参与上游缓存：它由节点上的探针单独上报，和地理/ASN 数据的生命周期不同，
// 所以每次请求现读一次（一条主键查询），探针一更新面板就能看到。
func buildLookupData(uuid string, ip net.IP, family int, snapshot lookupSnapshot) LookupData {
	unlockData := loadUnlock(uuid)
	return LookupData{
		UUID:           uuid,
		SchemaVersion:  SchemaVersion,
		Excluded:       false,
		ExcludedReason: nil,
		Address:        Address{Value: ip.String(), Family: family},
		Location:       snapshot.Location,
		Network:        snapshot.Network,
		Classification: snapshot.Classification,
		Reputation:     snapshot.Reputation,
		// 能力位跟着「有没有探测记录」走，主题据此决定要不要显示解锁区块。
		Capabilities: LookupCapabilities{
			MediaUnlock: unlockData != nil,
			AIUnlock:    unlockData != nil,
		},
		Unlock:   unlockData,
		Provider: snapshot.Provider,
	}
}

// loadUnlock 读取该节点的解锁快照。读失败只记一条日志：解锁是附加信息，
// 不该因为一次数据库抖动就让整个 IP 信息接口失败。
func loadUnlock(uuid string) *UnlockData {
	if uuid == "" {
		return nil
	}
	report, err := unlock.Load(uuid)
	if err != nil {
		logger.Warnf("ipinfo", "failed to load unlock report for %s: %v", uuid, err)
		return nil
	}
	if report == nil || len(report.Results) == 0 {
		return nil
	}
	return &UnlockData{
		EgressIP:     report.EgressIP,
		EgressRegion: report.EgressRegion,
		ProbedAt:     report.ProbedAt.UTC().Format(time.RFC3339),
		Results:      report.Results,
	}
}

// buildLatencyData 组装 /latency 的 data。
// providerCached 表示本次结果是否由本进程缓存提供（与 meta.cache 一致）。
func buildLatencyData(uuid string, ip net.IP, family int, snapshot latencySnapshot, providerCached bool) LatencyData {
	nodes := snapshot.Nodes
	if nodes == nil {
		nodes = []LatencyNode{}
	}
	return LatencyData{
		UUID:           uuid,
		SchemaVersion:  SchemaVersion,
		Address:        Address{Value: ip.String(), Family: family},
		Classification: snapshot.Classification,
		Latency: LatencyResult{
			Nodes:          nodes,
			AvailableCount: snapshot.AvailableCount,
			TimeoutCount:   snapshot.TimeoutCount,
			ProviderCached: providerCached,
		},
		Provider: snapshot.Provider,
	}
}

// respondError 输出契约里的失败体：{"error":{"message":"..."}}。
func respondError(c *gin.Context, status int, message string) {
	c.JSON(status, ErrorBody{Error: ErrorDetail{Message: message}})
}

// warnIfDegraded 在结果被上游故障降级时留一条日志，方便排查「面板显示不全」。
func warnIfDegraded(scope string, ip net.IP, meta Meta) {
	if meta.Warning == nil {
		return
	}
	logger.Warn("ipinfo", "ip info degraded", "scope", scope, "ip", ip.String(), "warning", *meta.Warning)
}
