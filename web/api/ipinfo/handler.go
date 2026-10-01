package ipinfo

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Aone2233/nekomari/database/unlock"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
	logger "github.com/Aone2233/nekomari/utils/log"
	"github.com/Aone2233/nekomari/web/api"
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

	// loadUnlock 读取某个节点的解锁快照（按调用者身份判断可否读取）。
	// 生产实现是 loadVisibleUnlock，可见性规则在 database/unlock。
	//
	// 做成字段是为了让本包的单元测试能在不建数据库的前提下驱动「不可见 → 明确错误」
	// 与「可见 → 原样返回」这两条分支：本包既有测试刻意不建库（见 unlock_test.go），
	// 而这里一旦依赖真实客户端表，那些用 test-uuid 的用例会全部变成 404。
	// nil 表示使用生产实现。
	loadUnlock func(uuid string, loggedIn bool) (*UnlockData, error)
}

// unlockLoader 返回本次请求使用的解锁读取实现。
func (h *Handler) unlockLoader() func(string, bool) (*UnlockData, error) {
	if h.loadUnlock != nil {
		return h.loadUnlock
	}
	return loadVisibleUnlock
}

// isAdmin 判断当前请求是否来自已登录管理员。IdentityMiddleware 未运行时 api.GetRole
// 返回 guest，于是拿不到任何节点数据 —— 这是安全的缺省方向。
func isAdmin(c *gin.Context) bool {
	return api.GetRole(c) == api.RoleAdmin
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

	// 节点相关的数据（解锁快照里的 egress_ip）必须在调用上游之前先过可见性判断：
	// 未通过就不该产生任何节点数据，也不该为此消耗上游配额。
	// 规则是「uuid 存在 && 非 hidden（或管理员）」，与 public:* 读取历史的约定相同；
	// 实现在 database/unlock.LoadVisible。
	uuid := strings.TrimSpace(c.Query("uuid"))
	var unlockData *UnlockData
	if uuid != "" {
		loaded, unlockErr := h.unlockLoader()(uuid, isAdmin(c))
		unlockData = loaded
		switch {
		case errors.Is(unlockErr, unlock.ErrNotVisible):
			// 隐藏节点与不存在的 uuid 给出完全相同的响应：两者一旦可区分，这个接口
			// 就成了隐藏节点存在性的判定器。
			respondError(c, http.StatusNotFound, "unknown node: "+uuid)
			return
		case unlockErr != nil:
			// 解锁是附加信息：一次数据库抖动不该让整个 IP 信息接口失败。但既然无法
			// 确认可见性，就不返回任何节点数据。
			logger.Warnf("ipinfo", "failed to load unlock report for %s: %v", uuid, unlockErr)
			unlockData = nil
		}
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
	c.JSON(http.StatusOK, Envelope{OK: true, Data: buildLookupData(uuid, ip, family, snapshot, unlockData), Meta: meta})
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

	// Refresh 挂在管理员路由下（web/router/router.go 的 RequireRole(admin)），所以这里
	// 显式以 loggedIn=true 取值：管理员可以读任何节点的快照。未知 uuid 只是没有解锁
	// 数据（与改动前一致），不影响刷新本身。
	unlockData, unlockErr := h.unlockLoader()(request.UUID, true)
	if unlockErr != nil {
		if !errors.Is(unlockErr, unlock.ErrNotVisible) {
			logger.Warnf("ipinfo", "failed to load unlock report for %s: %v", request.UUID, unlockErr)
		}
		unlockData = nil
	}

	warnIfDegraded("refresh", ip, meta)
	c.JSON(http.StatusOK, Envelope{OK: true, Data: buildLookupData(request.UUID, ip, family, snapshot, unlockData), Meta: meta})
}

// buildLookupData 组装 /lookup 与 /refresh 的 data。
// excluded 恒为 false：本后端不做大陆 IP 排除，也没有排除理由。
//
// unlockData 由调用方在此之前解析，这里不再自己按 uuid 读一次：公开的 /lookup 必须先
// 过节点可见性判断，不能出现「谁问都给」的第二条读取路径。
func buildLookupData(uuid string, ip net.IP, family int, snapshot lookupSnapshot, unlockData *UnlockData) LookupData {
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

// loadVisibleUnlock 是生产实现：可见性判断与读取都在 database/unlock 里完成，
// ErrNotVisible 由调用方映射成 HTTP 错误。
func loadVisibleUnlock(uuid string, loggedIn bool) (*UnlockData, error) {
	report, err := unlock.LoadVisible(uuid, loggedIn)
	if err != nil {
		return nil, err
	}
	return visibleUnlockData(report), nil
}

// visibleUnlockData 把节点上报的解锁快照转成接口 DTO。
// 没有探测结果时返回 nil：面板据此显示「等待探测」而不是「未解锁」。
func visibleUnlockData(report *v2.UnlockParams) *UnlockData {
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
