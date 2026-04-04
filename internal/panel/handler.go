package panel

import (
	"context"
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"pulse/internal/alert"
	"pulse/internal/auth"
	"pulse/internal/buildinfo"
	"pulse/internal/idgen"
	"pulse/internal/inbounds"
	"pulse/internal/jobs"
	"pulse/internal/nodes"
	"pulse/internal/orders"
	"pulse/internal/outbounds"
	"pulse/internal/plans"
	"pulse/internal/routerules"
	"pulse/internal/usage"
	"pulse/internal/users"
)

// SettingsStore 公告/设置持久化接口（与 auth.SettingsStore 相同签名，避免循环依赖）。
type SettingsStore interface {
	GetSetting(key string) (string, bool)
	SetSetting(key, value string) error
}

//go:embed templates static
var embedFS embed.FS

const cookieName = "pulse_token"

// nodeTrafficSample 记录某次采样时节点的累计字节数，用于计算实时速率。
type nodeTrafficSample struct {
	UploadTotal   int64
	DownloadTotal int64
	SampledAt     time.Time
}

// nodeMetrics 节点实时运行指标，用于 Overview 仪表盘。
type nodeMetrics struct {
	Node          nodes.Node
	Status        string // "online" / "idle" / "offline"
	SingboxVer    string
	NodeVer       string
	PingMs        int64    // 往返延迟（ms），-1 表示不可达
	Connections   int      // 活跃连接数
	OnlineUsers   int      // 有活跃连接的用户数
	OnlineDevices int      // 在线设备数
	UploadSpeed   int64    // bytes/s（前后采样估算）
	DownloadSpeed int64    // bytes/s
	DailyTraffic        []usage.DailyTrafficPoint // 近 7 天趋势
	PeriodUploadBytes   int64    // 近 7 天上传字节（DailyTraffic 汇总）
	PeriodDownloadBytes int64    // 近 7 天下载字节（DailyTraffic 汇总）
	Protocols           []string // 该节点支持的协议列表
	UserConns           []nodes.UserUsage // per-user 连接数据，仅用于内部缓存聚合，不送入 SSE 模板
	LastSyncAt          time.Time // 最后一次成功与节点通信的时间
}

// metricsSubscriber SSE 订阅者。
type metricsSubscriber struct {
	ch      chan string
	nodeIDs map[string]struct{} // nil = 管理端（全部节点）
}

// metricsHub SSE fan-out 广播中心。
type metricsHub struct {
	mu   sync.RWMutex
	subs map[*metricsSubscriber]struct{}
}

func (hub *metricsHub) subscribe(nodeIDs map[string]struct{}) *metricsSubscriber {
	sub := &metricsSubscriber{ch: make(chan string, 4), nodeIDs: nodeIDs}
	hub.mu.Lock()
	hub.subs[sub] = struct{}{}
	hub.mu.Unlock()
	return sub
}

func (hub *metricsHub) unsubscribe(sub *metricsSubscriber) {
	hub.mu.Lock()
	delete(hub.subs, sub)
	hub.mu.Unlock()
}

// broadcast 向所有订阅者按需渲染并推送 HTML。
func (hub *metricsHub) broadcast(h *Handler, allMetrics []nodeMetrics) {
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	for sub := range hub.subs {
		var filtered []nodeMetrics
		if sub.nodeIDs == nil {
			filtered = allMetrics
		} else {
			for _, m := range allMetrics {
				if _, ok := sub.nodeIDs[m.Node.ID]; ok {
					filtered = append(filtered, m)
				}
			}
		}
		var html string
		if sub.nodeIDs == nil {
			html = h.renderToString("partial-node-metrics", filtered)
		} else {
			html = h.renderToString("partial-user-node-status", metricsToUserNodeInfos(filtered))
		}
		if html == "" {
			continue
		}
		select {
		case sub.ch <- html:
		default:
		}
	}
}

// nodeIDsKey 将 nodeID 集合序列化为唯一字符串 key。
func nodeIDsKey(ids map[string]struct{}) string {
	keys := make([]string, 0, len(ids))
	for k := range ids {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

// Handler 面板 HTTP 处理器，持有所有依赖。
type Handler struct {
	auth           *auth.Manager
	discourse      *auth.DiscourseConfig // 可为 nil，表示未启用
	userStore      users.Store
	nodeStore      nodes.Store
	ibStore        inbounds.InboundStore
	outboundStore  outbounds.Store
	routeRuleStore routerules.Store
	dial           jobs.NodeDialer
	applyOpts      jobs.ApplyOptions
	tmpl           *template.Template
	serverAddr     string
	clientCertFile string // 面板客户端证书路径，用于 node 安装时粘贴
	settingsStore  SettingsStore
	csrfSecret     []byte   // HMAC key for deriving per-session CSRF tokens
	speedCache     sync.Map // key=nodeID, value=nodeTrafficSample；重启丢失可接受
	hub            metricsHub
	lastAccessAt   atomic.Int64 // unix nano，有人访问时更新
	polling        atomic.Bool  // 防止并发轮询堆积
	metricsCache   struct {
		mu   sync.RWMutex
		data []nodeMetrics
		at   time.Time
	}
	// userConnsCache 缓存各节点实时 per-user 连接数聚合结果（每秒更新）。
	// key=username，value=[connections, devices]，供用户列表叠加真实数据用。
	userConnsCache struct {
		mu   sync.RWMutex
		data map[string][2]int
	}
	planStore   plans.Store
	orderStore  orders.Store
	shopEnabled bool
}

// pageData 传入完整页面模板的数据结构。
type pageData struct {
	Page        string // "dashboard", "users", "nodes"
	Username    string
	Version     string
	CSRFToken   string
	ShopEnabled bool
	Data        any
}

// nodeWithStatus 节点及其运行状态。
type nodeWithStatus struct {
	Node           nodes.Node
	Status         string // "online" / "offline" / "idle"
	SingboxVer     string // sing-box 版本
	NodeVer        string // pulse-node 编译版本
	DirectChecks   []nodes.CheckResult
	ProxiedChecks  []nodes.CheckResult
	SpeedTest      *nodes.SpeedTestResult
	Uptime         nodes.UptimeSummary
}

// checkResultsPartialData 传给解锁检测结果局部模板的数据。
type checkResultsPartialData struct {
	NodeID        string
	DirectChecks  []nodes.CheckResult
	ProxiedChecks []nodes.CheckResult
}

// statNodeEntry 公开状态页中单个节点的数据。
type statNodeEntry struct {
	Name          string
	DirectChecks  []nodes.CheckResult
	ProxiedChecks []nodes.CheckResult
	SpeedTest     *nodes.SpeedTestResult
	Uptime        nodes.UptimeSummary
	HasData       bool
	CheckedAt     time.Time
	UnlockedCount int
	TotalCount    int
	UnlockPct     int // 0-100
}

// statData 公开状态页的完整数据。
type statData struct {
	Nodes        []statNodeEntry
	NodeCount    int
	UnlockRate   int // 所有节点平均解锁率（%）
	ServiceCount int // 检测的服务种类数
	UpdatedAt    time.Time
}

// speedTestPartialData 传给测速结果局部模板的数据。
type speedTestPartialData struct {
	NodeID string
	Result nodes.SpeedTestResult
}

// inboundHostsData 传给 Host 相关模板的数据结构。
type inboundHostsData struct {
	Inbound inbounds.Inbound
	Hosts   []inbounds.Host
}

// New 创建 Handler 实例并解析模板。
func New(
	authMgr *auth.Manager,
	userStore users.Store,
	nodeStore nodes.Store,
	ibStore inbounds.InboundStore,
	outboundStore outbounds.Store,
	routeRuleStore routerules.Store,
	dial jobs.NodeDialer,
	applyOpts jobs.ApplyOptions,
	serverAddr string,
	clientCertFile string,
	settingsStore SettingsStore,
	discourse *auth.DiscourseConfig,
) (*Handler, error) {
	h := &Handler{
		auth:           authMgr,
		discourse:      discourse,
		userStore:      userStore,
		nodeStore:      nodeStore,
		ibStore:        ibStore,
		outboundStore:  outboundStore,
		routeRuleStore: routeRuleStore,
		dial:           dial,
		applyOpts:      applyOpts,
		serverAddr:     serverAddr,
		clientCertFile: clientCertFile,
		settingsStore:  settingsStore,
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generate csrf secret: %w", err)
	}
	h.csrfSecret = secret
	h.hub.subs = make(map[*metricsSubscriber]struct{})

	tmpl, err := template.New("").Funcs(templateFuncs()).ParseFS(embedFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
	}
	h.tmpl = tmpl
	return h, nil
}

// panelPort 从 serverAddr 解析面板监听端口，解析失败时返回 8080。
func (h *Handler) panelPort() int {
	if _, portStr, err := net.SplitHostPort(h.serverAddr); err == nil {
		if p, err := strconv.Atoi(portStr); err == nil && p > 0 {
			return p
		}
	}
	return 8080
}

// Register 将所有路由注册到 mux。
func (h *Handler) Register(mux *http.ServeMux) {
	// 公开路由
	mux.HandleFunc("GET /stat", h.statPage)
	mux.HandleFunc("GET /login", h.loginPage)
	mux.HandleFunc("POST /login", h.processLogin)
	mux.HandleFunc("POST /logout", h.processLogout)
	mux.HandleFunc("GET /auth/discourse", h.discourseRedirect)
	mux.HandleFunc("GET /auth/discourse/callback", h.discourseCallback)

	// 用户自助门户（以 sub_token 鉴权，无需管理员登录）
	mux.HandleFunc("GET /user/{sub_token}", h.userPortalPage)
	mux.HandleFunc("GET /api/me", h.apiMe)
	mux.HandleFunc("POST /api/me/reset-token", h.apiResetToken)

	// 页面路由（需要认证）
	mux.HandleFunc("/", h.requireAuth(h.redirectDashboard))
	mux.HandleFunc("GET /dashboard", h.requireAuth(h.dashboardPage))
	mux.HandleFunc("GET /users", h.requireAuth(h.usersPage))
	mux.HandleFunc("GET /nodes", h.requireAuth(h.nodesPage))
	mux.HandleFunc("GET /settings", h.requireAuth(h.settingsPage))

	// HTMX partials（需要认证）
	mux.HandleFunc("GET /panel/stats", h.requireAuth(h.statsPartial))

	mux.HandleFunc("GET /panel/users/list", h.requireAuth(h.usersListPartial))
	mux.HandleFunc("GET /panel/users/new", h.requireAuth(h.userNewForm))
	mux.HandleFunc("POST /panel/users", h.requireAuth(h.createUser))
	mux.HandleFunc("GET /panel/users/{id}/edit", h.requireAuth(h.userEditForm))
	mux.HandleFunc("PUT /panel/users/{id}", h.requireAuth(h.updateUser))
	mux.HandleFunc("DELETE /panel/users/{id}", h.requireAuth(h.deleteUser))
	mux.HandleFunc("POST /panel/users/{id}/reset-traffic", h.requireAuth(h.resetUserTraffic))
	mux.HandleFunc("POST /panel/settings/announcement", h.requireAuth(h.saveAnnouncement))
	mux.HandleFunc("POST /panel/settings/alert", h.requireAuth(h.saveAlertSettings))
	mux.HandleFunc("POST /panel/settings/alert/test", h.requireAuth(h.testAlertSettings))

	mux.HandleFunc("GET /inbounds", h.requireAuth(h.inboundsPage))
	mux.HandleFunc("GET /outbounds", h.requireAuth(h.outboundsPage))
	mux.HandleFunc("GET /panel/inbounds/list", h.requireAuth(h.inboundsListPartial))
	mux.HandleFunc("GET /panel/inbounds/new", h.requireAuth(h.inboundNewForm))
	mux.HandleFunc("POST /panel/inbounds", h.requireAuth(h.createInbound))
	mux.HandleFunc("GET /panel/inbounds/{id}/edit", h.requireAuth(h.inboundEditForm))
	mux.HandleFunc("PUT /panel/inbounds/{id}", h.requireAuth(h.updateInbound))
	mux.HandleFunc("DELETE /panel/inbounds/{id}", h.requireAuth(h.deleteInbound))

	mux.HandleFunc("GET /panel/outbounds/list", h.requireAuth(h.outboundsListPartial))
	mux.HandleFunc("GET /panel/outbounds/new", h.requireAuth(h.outboundNewForm))
	mux.HandleFunc("GET /panel/outbounds/import", h.requireAuth(h.outboundImportForm))
	mux.HandleFunc("POST /panel/outbounds/import", h.requireAuth(h.importOutbound))
	mux.HandleFunc("POST /panel/outbounds", h.requireAuth(h.createOutbound))
	mux.HandleFunc("GET /panel/outbounds/{id}/edit", h.requireAuth(h.outboundEditForm))
	mux.HandleFunc("PUT /panel/outbounds/{id}", h.requireAuth(h.updateOutbound))
	mux.HandleFunc("DELETE /panel/outbounds/{id}", h.requireAuth(h.deleteOutbound))

	mux.HandleFunc("GET /routerules", h.requireAuth(h.routeRulesPage))
	mux.HandleFunc("GET /panel/routerules/list", h.requireAuth(h.routeRulesListPartial))
	mux.HandleFunc("GET /panel/routerules/new", h.requireAuth(h.routeRuleNewForm))
	mux.HandleFunc("POST /panel/routerules", h.requireAuth(h.createRouteRule))
	mux.HandleFunc("GET /panel/routerules/{id}/edit", h.requireAuth(h.routeRuleEditForm))
	mux.HandleFunc("PUT /panel/routerules/{id}", h.requireAuth(h.updateRouteRule))
	mux.HandleFunc("DELETE /panel/routerules/{id}", h.requireAuth(h.deleteRouteRule))


	mux.HandleFunc("GET /panel/tools/reality-keypair", h.requireAuth(h.realityKeypair))

	mux.HandleFunc("GET /panel/users/{id}/sub-logs", h.requireAuth(h.subLogsModal))
	mux.HandleFunc("GET /panel/users/{id}/node-usage", h.requireAuth(h.nodeUsageModal))
	mux.HandleFunc("GET /panel/inbounds/{id}/hosts", h.requireAuth(h.hostsModal))
	mux.HandleFunc("POST /panel/inbounds/{id}/hosts", h.requireAuth(h.createHost))
	mux.HandleFunc("GET /panel/inbounds/{id}/users", h.requireAuth(h.inboundUsersModal))
	mux.HandleFunc("POST /panel/inbounds/{id}/users", h.requireAuth(h.updateInboundUsers))
	mux.HandleFunc("GET /panel/hosts/{id}/edit", h.requireAuth(h.hostEditForm))
	mux.HandleFunc("PUT /panel/hosts/{id}", h.requireAuth(h.updateHost))
	mux.HandleFunc("DELETE /panel/hosts/{id}", h.requireAuth(h.deleteHost))

	mux.HandleFunc("GET /panel/nodes/list", h.requireAuth(h.nodesListPartial))
	mux.HandleFunc("GET /panel/node-metrics", h.requireAuth(h.nodeMetricsPartial))
	mux.HandleFunc("GET /panel/node-metrics/stream", h.requireAuth(h.nodeMetricsStreamHandler))
	// 用户主页节点状态（以 sub_token 鉴权，无需 cookie）
	mux.HandleFunc("GET /panel/user-node-status/{sub_token}", h.userNodeStatusPartial)
	mux.HandleFunc("GET /panel/user-node-status/{sub_token}/stream", h.userNodeStatusStreamHandler)
	mux.HandleFunc("GET /panel/nodes/new", h.requireAuth(h.nodeNewForm))
	mux.HandleFunc("POST /panel/nodes", h.requireAuth(h.createNode))
	mux.HandleFunc("GET /panel/nodes/{id}/edit", h.requireAuth(h.nodeEditForm))
	mux.HandleFunc("PUT /panel/nodes/{id}", h.requireAuth(h.updateNode))
	mux.HandleFunc("DELETE /panel/nodes/{id}", h.requireAuth(h.deleteNode))
	mux.HandleFunc("POST /panel/nodes/{id}/restart", h.requireAuth(h.restartNode))
	mux.HandleFunc("POST /panel/nodes/{id}/start", h.requireAuth(h.startNode))
	mux.HandleFunc("POST /panel/nodes/{id}/stop", h.requireAuth(h.stopNode))
	mux.HandleFunc("GET /panel/nodes/{id}/config", h.requireAuth(h.nodeConfigModal))
	mux.HandleFunc("POST /panel/nodes/{id}/apply-config", h.requireAuth(h.applyNodeConfig))
	mux.HandleFunc("GET /panel/nodes/{id}/logs", h.requireAuth(h.nodeLogsModal))
	mux.HandleFunc("GET /panel/nodes/{id}/logs/stream", h.requireAuth(h.nodeLogsStream))

	mux.HandleFunc("POST /panel/nodes/{id}/check", h.requireAuth(h.nodeCheckUnlock))
	mux.HandleFunc("POST /panel/nodes/{id}/speedtest", h.requireAuth(h.nodeSpeedTest))

	mux.HandleFunc("GET /caddy", h.requireAuth(h.caddyPage))
	mux.HandleFunc("GET /panel/caddy/list", h.requireAuth(h.caddyListPartial))
	mux.HandleFunc("POST /panel/caddy/{nodeID}/sync", h.requireAuth(h.caddySyncNode))
	mux.HandleFunc("GET /panel/caddy/{nodeID}/config-form", h.requireAuth(h.caddyConfigForm))
	mux.HandleFunc("POST /panel/caddy/{nodeID}/config", h.requireAuth(h.caddySaveConfig))
	mux.HandleFunc("GET /panel/caddy/{nodeID}/caddyfile", h.requireAuth(h.caddyfileModal))

	// 公开商店页面（仅在 ShopEnabled 时注册）
	if h.shopEnabled {
		mux.HandleFunc("GET /shop", h.shopPage)
		mux.HandleFunc("GET /shop/success", h.shopSuccessPage)

		mux.HandleFunc("GET /plans", h.requireAuth(h.plansPage))
		mux.HandleFunc("GET /panel/plans/list", h.requireAuth(h.plansListPartial))
		mux.HandleFunc("GET /panel/plans/new", h.requireAuth(h.planNewForm))
		mux.HandleFunc("POST /panel/plans", h.requireAuth(h.createPlan))
		mux.HandleFunc("GET /panel/plans/{id}/edit", h.requireAuth(h.planEditForm))
		mux.HandleFunc("PUT /panel/plans/{id}", h.requireAuth(h.updatePlan))
		mux.HandleFunc("DELETE /panel/plans/{id}", h.requireAuth(h.deletePlan))
	}
}

// ─── 认证中间件 ──────────────────────────────────────────────────────────────

// SetShopEnabled configures the shop page for the panel handler.
func (h *Handler) SetShopEnabled(ps plans.Store, os orders.Store) {
	h.planStore = ps
	h.orderStore = os
	h.shopEnabled = true
}

func (h *Handler) shopPage(w http.ResponseWriter, r *http.Request) {
	list, _ := h.planStore.ListEnabledPlans()
	type shopData struct {
		Plans []plans.Plan
		Error string
	}
	h.tmpl.ExecuteTemplate(w, "shop", pageData{Data: shopData{Plans: list}})
}

func (h *Handler) shopSuccessPage(w http.ResponseWriter, r *http.Request) {
	type successData struct {
		Email  string
		SubURL string
	}
	var data successData
	if sessionID := r.URL.Query().Get("session_id"); sessionID != "" && h.orderStore != nil {
		if order, err := h.orderStore.GetOrderByStripeSession(sessionID); err == nil {
			data.Email = order.Email
			if order.UserID != "" {
				if user, err := h.userStore.GetUser(order.UserID); err == nil && user.SubToken != "" {
					data.SubURL = subURL(r, user.SubToken)
				}
			}
		}
	}
	h.tmpl.ExecuteTemplate(w, "shop-success", pageData{Data: data})
}

// ─── 套餐管理 ─────────────────────────────────────────────────────────────────

func (h *Handler) plansPage(w http.ResponseWriter, r *http.Request) {
	h.renderPage(w, r, "plans", pageData{
		Page:     "plans",
		Username: h.currentUsername(r),
	})
}

func (h *Handler) plansListPartial(w http.ResponseWriter, r *http.Request) {
	list, err := h.planStore.ListPlans()
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to list plans: "+err.Error())
		return
	}
	h.renderPartial(w, "plans-list", list)
}

func (h *Handler) planNewForm(w http.ResponseWriter, r *http.Request) {
	h.renderPartial(w, "plans-new-form", nil)
}

func (h *Handler) createPlan(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		htmxError(w, http.StatusBadRequest, "invalid form data")
		return
	}
	trafficGB, _ := strconv.ParseFloat(r.FormValue("traffic_limit_gb"), 64)
	priceCents, _ := strconv.Atoi(r.FormValue("price_cents"))
	durationDays, _ := strconv.Atoi(r.FormValue("duration_days"))
	sortOrder, _ := strconv.Atoi(r.FormValue("sort_order"))

	plan := plans.Plan{
		ID:                     idgen.NextString(),
		Name:                   r.FormValue("name"),
		Description:            r.FormValue("description"),
		Type:                   r.FormValue("type"),
		PriceCents:             priceCents,
		Currency:               r.FormValue("currency"),
		StripePriceID:          r.FormValue("stripe_price_id"),
		TrafficLimit:           int64(trafficGB * 1e9),
		DurationDays:           durationDays,
		DataLimitResetStrategy: r.FormValue("data_limit_reset_strategy"),
		InboundIDs:             r.FormValue("inbound_ids"),
		SortOrder:              sortOrder,
		Enabled:                r.FormValue("enabled") == "on",
	}
	if _, err := h.planStore.UpsertPlan(plan); err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to create plan: "+err.Error())
		return
	}
	h.renderPlansListFromStore(w)
}

func (h *Handler) planEditForm(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	plan, err := h.planStore.GetPlan(id)
	if err != nil {
		htmxError(w, http.StatusNotFound, "plan not found")
		return
	}
	h.renderPartial(w, "plans-edit-form", plan)
}

func (h *Handler) updatePlan(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	plan, err := h.planStore.GetPlan(id)
	if err != nil {
		htmxError(w, http.StatusNotFound, "plan not found")
		return
	}
	if err := r.ParseForm(); err != nil {
		htmxError(w, http.StatusBadRequest, "invalid form data")
		return
	}
	trafficGB, _ := strconv.ParseFloat(r.FormValue("traffic_limit_gb"), 64)
	priceCents, _ := strconv.Atoi(r.FormValue("price_cents"))
	durationDays, _ := strconv.Atoi(r.FormValue("duration_days"))
	sortOrder, _ := strconv.Atoi(r.FormValue("sort_order"))

	plan.Name = r.FormValue("name")
	plan.Description = r.FormValue("description")
	plan.Type = r.FormValue("type")
	plan.PriceCents = priceCents
	plan.Currency = r.FormValue("currency")
	plan.StripePriceID = r.FormValue("stripe_price_id")
	plan.TrafficLimit = int64(trafficGB * 1e9)
	plan.DurationDays = durationDays
	plan.DataLimitResetStrategy = r.FormValue("data_limit_reset_strategy")
	plan.InboundIDs = r.FormValue("inbound_ids")
	plan.SortOrder = sortOrder
	plan.Enabled = r.FormValue("enabled") == "on"

	if _, err := h.planStore.UpsertPlan(plan); err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to update plan: "+err.Error())
		return
	}
	h.renderPlansListFromStore(w)
}

func (h *Handler) deletePlan(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.planStore.DeletePlan(id); err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to delete plan: "+err.Error())
		return
	}
	h.renderPlansListFromStore(w)
}

func (h *Handler) renderPlansListFromStore(w http.ResponseWriter) {
	list, err := h.planStore.ListPlans()
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to list plans: "+err.Error())
		return
	}
	h.renderPartial(w, "plans-list", list)
}

// requireAuth 封装需要认证的 handler。
// 对 POST/PUT/DELETE 请求额外校验 CSRF token。
func (h *Handler) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(cookieName)
		if err != nil || !h.auth.ValidateToken(cookie.Value) {
			if isHTMX(r) {
				w.Header().Set("HX-Redirect", "/login")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if !h.validateCSRF(r) {
				if isHTMX(r) {
					htmxError(w, http.StatusForbidden, "CSRF token invalid")
					return
				}
				http.Error(w, "CSRF token invalid", http.StatusForbidden)
				return
			}
		}
		next(w, r)
	}
}

// currentUsername 从 cookie 中获取当前登录用户名。
func (h *Handler) currentUsername(r *http.Request) string {
	cookie, err := r.Cookie(cookieName)
	if err != nil {
		return ""
	}
	return h.auth.GetUsernameByToken(cookie.Value)
}

// ─── 辅助函数 ────────────────────────────────────────────────────────────────

const (
	trafficRateMin = 0.1   // 与前端 min 保持一致
	trafficRateMax = 100.0 // 防止极端倍率写入
)

// parseTrafficRate 解析流量倍率字符串，超出 [0.1, 100] 范围或非法时返回 1.0。
func parseTrafficRate(s string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || v < trafficRateMin || v > trafficRateMax {
		return 1.0
	}
	return v
}

// isHTMX 判断请求是否来自 HTMX。
func isHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

// setSessionCookie 写入 session cookie。
func setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   86400,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// clearSessionCookie 清除 session cookie。
func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// ─── CSRF 防护 ───────────────────────────────────────────────────────────────

// csrfToken 根据 session token 通过 HMAC-SHA256 派生 CSRF token（无状态）。
func (h *Handler) csrfToken(sessionToken string) string {
	mac := hmac.New(sha256.New, h.csrfSecret)
	mac.Write([]byte(sessionToken))
	return hex.EncodeToString(mac.Sum(nil))
}

// csrfTokenFromRequest 从当前请求的 session cookie 派生 CSRF token。
func (h *Handler) csrfTokenFromRequest(r *http.Request) string {
	cookie, err := r.Cookie(cookieName)
	if err != nil {
		return ""
	}
	return h.csrfToken(cookie.Value)
}

// validateCSRF 检查请求中的 CSRF token 是否与 session 匹配。
// 优先检查 X-CSRF-Token header（HTMX 请求），回退到 _csrf 表单字段（普通表单）。
func (h *Handler) validateCSRF(r *http.Request) bool {
	expected := h.csrfTokenFromRequest(r)
	if expected == "" {
		return false
	}
	token := r.Header.Get("X-CSRF-Token")
	if token == "" {
		token = r.FormValue("_csrf")
	}
	return hmac.Equal([]byte(token), []byte(expected))
}

// htmxError 向 HTMX 请求返回错误响应。
func (h *Handler) realityKeypair(w http.ResponseWriter, r *http.Request) {
	priv, pub, err := generateRealityKeypair()
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, `{"error":%q}`, err.Error())
		return
	}
	sid := generateRealityShortID()
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"private_key":%q,"public_key":%q,"short_id":%q}`, priv, pub, sid)
}

func generateRealityKeypair() (priv, pub string, err error) {
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	return base64.RawURLEncoding.EncodeToString(key.Bytes()),
		base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()),
		nil
}

func generateRealityShortID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// generateSSPassword 根据 SS 加密方式生成对应长度的 base64 PSK。
func generateSSPassword(method string) string {
	size := 32
	if method == "2022-blake3-aes-128-gcm" {
		size = 16
	}
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(b)
}

func htmxError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("HX-Reswap", "none")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	b, _ := json.Marshal(map[string]string{"error": msg})
	w.Write(b) //nolint:errcheck
}

// setHXTriggerToast 将 pulseToast 消息写入 HX-Trigger header，
// 用 JSON 编码确保中文字符被转义为 \uXXXX，避免 HTTP header 乱码。
func setHXTriggerToast(w http.ResponseWriter, msg string) {
	b, _ := json.Marshal(map[string]string{"pulseToast": msg})
	w.Header().Set("HX-Trigger", string(b))
}

// renderPage 使用完整 layout 模板渲染页面。
func (h *Handler) renderPage(w http.ResponseWriter, r *http.Request, name string, data pageData) {
	data.Version = buildinfo.Version
	data.CSRFToken = h.csrfTokenFromRequest(r)
	data.ShopEnabled = h.shopEnabled
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, "template render error: "+err.Error(), http.StatusInternalServerError)
	}
}

// renderPartial 直接执行模板片段，数据包装为 pageData{Data: data}。
func (h *Handler) renderPartial(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, name, pageData{Data: data}); err != nil {
		http.Error(w, "template render error: "+err.Error(), http.StatusInternalServerError)
	}
}

// ─── 公开页面 ─────────────────────────────────────────────────────────────────

// loginPageData 登录页面专用数据。
type loginPageData struct {
	Error            string // 登录错误消息
	DiscourseEnabled bool   // 是否显示 Discourse 登录按钮
}

// statPage 公开节点状态探针页，无需认证。
func (h *Handler) statPage(w http.ResponseWriter, r *http.Request) {
	nodeList, _ := h.nodeStore.List()
	checkMap, _ := h.nodeStore.ListAllNodeCheckResults()
	speedMap, _ := h.nodeStore.ListAllNodeSpeedTests()
	uptimeMap, _ := h.nodeStore.ListNodeUptimeSummary(7)

	var (
		totalUnlocked int
		totalServices int
		latestUpdated time.Time
		maxServices   int
		entries       = make([]statNodeEntry, 0, len(nodeList))
	)

	for _, n := range nodeList {
		direct, proxied := splitCheckResults(checkMap[n.ID])
		// 统计数据仅基于直连结果（代表节点本身解锁能力）
		entry := statNodeEntry{
			Name:          n.Name,
			DirectChecks:  direct,
			ProxiedChecks: proxied,
			Uptime:        uptimeMap[n.ID],
			HasData:       len(direct) > 0,
			TotalCount:    len(direct),
		}
		if st, ok := speedMap[n.ID]; ok {
			entry.SpeedTest = &st
		}
		for _, cr := range direct {
			if cr.Unlocked {
				entry.UnlockedCount++
			}
			if !cr.CheckedAt.IsZero() && cr.CheckedAt.After(latestUpdated) {
				latestUpdated = cr.CheckedAt
			}
			if entry.CheckedAt.IsZero() {
				entry.CheckedAt = cr.CheckedAt
			}
		}
		if entry.TotalCount > 0 {
			entry.UnlockPct = entry.UnlockedCount * 100 / entry.TotalCount
		}
		if len(direct) > maxServices {
			maxServices = len(direct)
		}
		totalUnlocked += entry.UnlockedCount
		totalServices += entry.TotalCount
		entries = append(entries, entry)
	}

	avgUnlockRate := 0
	if totalServices > 0 {
		avgUnlockRate = totalUnlocked * 100 / totalServices
	}

	data := statData{
		Nodes:        entries,
		NodeCount:    len(nodeList),
		UnlockRate:   avgUnlockRate,
		ServiceCount: maxServices,
		UpdatedAt:    latestUpdated,
	}
	h.renderPage(w, r, "stat", pageData{Data: data})
}

func (h *Handler) loginPage(w http.ResponseWriter, r *http.Request) {
	var errMsg string
	if r.URL.Query().Get("error") == "1" {
		errMsg = "用户名或密码错误"
	}
	h.renderPage(w, r, "login", pageData{
		Page: "login",
		Data: loginPageData{
			Error:            errMsg,
			DiscourseEnabled: h.discourse.Enabled(),
		},
	})
}

// discourseRedirect 发起 Discourse SSO 认证流程，将用户重定向到 Discourse。
func (h *Handler) discourseRedirect(w http.ResponseWriter, r *http.Request) {
	if !h.discourse.Enabled() {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	scheme := "https"
	if r.TLS == nil {
		scheme = "http"
	}
	returnURL := fmt.Sprintf("%s://%s/auth/discourse/callback", scheme, r.Host)
	redirectURL, err := h.discourse.BuildRedirectURL(returnURL)
	if err != nil {
		http.Error(w, "SSO 初始化失败", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

// discourseCallback 处理 Discourse 认证回调，验证签名后建立 session。
func (h *Handler) discourseCallback(w http.ResponseWriter, r *http.Request) {
	if !h.discourse.Enabled() {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	rawSSO := r.URL.Query().Get("sso")
	sig := r.URL.Query().Get("sig")
	username, err := h.discourse.ParseCallback(rawSSO, sig)
	if err != nil {
		http.Redirect(w, r, "/login?error=1", http.StatusFound)
		return
	}
	if !h.discourse.IsAllowed(username) {
		http.Redirect(w, r, "/login?error=1", http.StatusFound)
		return
	}
	token, err := h.auth.CreateSession(username)
	if err != nil {
		http.Error(w, "创建 session 失败", http.StatusInternalServerError)
		return
	}
	setSessionCookie(w, token)
	http.Redirect(w, r, "/dashboard", http.StatusFound)
}

func (h *Handler) processLogin(w http.ResponseWriter, r *http.Request) {
	username := r.FormValue("username")
	password := r.FormValue("password")

	token, err := h.auth.LoginFromRequest(r, username, password)
	if err != nil {
		if isHTMX(r) {
			htmxError(w, http.StatusUnauthorized, "invalid username or password")
			return
		}
		http.Redirect(w, r, "/login?error=1", http.StatusFound)
		return
	}

	setSessionCookie(w, token)
	if isHTMX(r) {
		w.Header().Set("HX-Redirect", "/dashboard")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/dashboard", http.StatusFound)
}

func (h *Handler) processLogout(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "CSRF token invalid", http.StatusForbidden)
		return
	}
	cookie, err := r.Cookie(cookieName)
	if err == nil {
		_ = h.auth.DeleteToken(cookie.Value)
	}
	clearSessionCookie(w)
	http.Redirect(w, r, "/login", http.StatusFound)
}

// ─── 完整页面 ─────────────────────────────────────────────────────────────────

func (h *Handler) redirectDashboard(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/dashboard", http.StatusFound)
}

func (h *Handler) dashboardPage(w http.ResponseWriter, r *http.Request) {
	h.renderPage(w, r, "dashboard", pageData{
		Page:     "dashboard",
		Username: h.currentUsername(r),
	})
}

func (h *Handler) usersPage(w http.ResponseWriter, r *http.Request) {
	h.renderPage(w, r, "users", pageData{
		Page:     "users",
		Username: h.currentUsername(r),
	})
}

func (h *Handler) nodesPage(w http.ResponseWriter, r *http.Request) {
	h.renderPage(w, r, "nodes", pageData{
		Page:     "nodes",
		Username: h.currentUsername(r),
	})
}

// ─── HTMX Partials ───────────────────────────────────────────────────────────

func (h *Handler) statsPartial(w http.ResponseWriter, r *http.Request) {
	days := 14
	switch r.URL.Query().Get("days") {
	case "7":
		days = 7
	case "30":
		days = 30
	case "90":
		days = 90
	}
	summary, err := usage.Build(h.nodeStore, h.userStore, days)
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to get stats: "+err.Error())
		return
	}
	h.renderPartial(w, "partial-stats", summary)
}

func (h *Handler) usersListPartial(w http.ResponseWriter, r *http.Request) {
	h.lastAccessAt.Store(time.Now().UnixNano()) // 保持后台轮询活跃，以获取实时连接数
	q := strings.ToLower(r.URL.Query().Get("q"))
	statusFilter := r.URL.Query().Get("status")

	allUsers, err := h.userStore.ListUsers()
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to get user list: "+err.Error())
		return
	}

	// 按关键词和状态过滤
	now := time.Now()
	expiringDeadline := now.Add(7 * 24 * time.Hour)
	filtered := make([]users.User, 0, len(allUsers))
	for _, u := range allUsers {
		if q != "" && !strings.Contains(strings.ToLower(u.Username), q) {
			continue
		}
		if statusFilter == "expiring" {
			// 虚拟过滤：7 天内到期的活跃用户
			if u.EffectiveStatus() != users.StatusActive {
				continue
			}
			if u.ExpireAt == nil || !u.ExpireAt.After(now) || !u.ExpireAt.Before(expiringDeadline) {
				continue
			}
		} else if statusFilter != "" && u.EffectiveStatus() != statusFilter {
			continue
		}
		filtered = append(filtered, u)
	}

	h.renderPartial(w, "partial-user-rows", h.overlayLiveConns(filtered))
}

func (h *Handler) userNewForm(w http.ResponseWriter, r *http.Request) {
	ibList, err := h.ibStore.ListInbounds()
	if err != nil {
		ibList = nil
	}
	h.renderPartial(w, "partial-user-new-form", userFormData{
		Inbounds: ibList,
		NodeMap:  h.buildNodeMap(),
	})
}

func (h *Handler) createUser(w http.ResponseWriter, r *http.Request) {
	username := r.FormValue("username")
	trafficLimitGBStr := r.FormValue("traffic_limit_gb")
	resetStrategy := r.FormValue("reset_strategy")
	expireAtStr := r.FormValue("expire_at")
	note := r.FormValue("note")

	if username == "" {
		htmxError(w, http.StatusBadRequest, "username is required")
		return
	}

	var trafficLimit int64
	if trafficLimitGBStr != "" {
		gb, err := strconv.ParseFloat(trafficLimitGBStr, 64)
		if err != nil {
			htmxError(w, http.StatusBadRequest, "invalid traffic limit format")
			return
		}
		trafficLimit = int64(math.Round(gb * 1024 * 1024 * 1024))
	}

	var expireAt *time.Time
	if expireAtStr != "" {
		t, err := time.ParseInLocation("2006-01-02", expireAtStr, time.Local)
		if err != nil {
			htmxError(w, http.StatusBadRequest, "invalid expiry format")
			return
		}
		expireAt = &t
	}

	newUser := users.User{
		ID:                     idgen.NextString(),
		Username:               username,
		Status:                 users.StatusActive,
		Note:                   note,
		TrafficLimit:           trafficLimit,
		DataLimitResetStrategy: resetStrategy,
		ExpireAt:               expireAt,
		CreatedAt:              time.Now(),
		SubToken:               panelRandomToken(16),
	}

	if _, err := h.userStore.UpsertUser(newUser); err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to create user: "+err.Error())
		return
	}

	// 处理 inbound 关联，变更后异步推送配置到受影响节点
	if selectedIDs := r.Form["inbound_ids"]; len(selectedIDs) > 0 {
		affected, err := h.SyncUserInbounds(newUser.ID, selectedIDs)
		if err != nil {
			htmxError(w, http.StatusInternalServerError, "failed to sync inbounds: "+err.Error())
			return
		}
		h.ApplyNodes(affected)
	}

	// 返回更新后的用户列表
	h.renderUsersListFromStore(w)
}

func (h *Handler) userEditForm(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	user, err := h.userStore.GetUser(id)
	if err != nil {
		htmxError(w, http.StatusNotFound, "user not found")
		return
	}
	ibList, err := h.ibStore.ListInbounds()
	if err != nil {
		ibList = nil
	}
	// 加载用户已关联的 inbound，用于表单回显勾选状态
	userAccesses, _ := h.userStore.ListUserInboundsByUser(id)
	selectedInboundIDs := make(map[string]bool, len(userAccesses))
	for _, acc := range userAccesses {
		selectedInboundIDs[acc.InboundID] = true
	}
	h.renderPartial(w, "partial-user-edit-form", userFormData{
		User:               &user,
		Inbounds:           ibList,
		SelectedInboundIDs: selectedInboundIDs,
		NodeMap:            h.buildNodeMap(),
	})
}

func (h *Handler) updateUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	user, err := h.userStore.GetUser(id)
	if err != nil {
		htmxError(w, http.StatusNotFound, "user not found")
		return
	}

	if username := r.FormValue("username"); username != "" {
		user.Username = username
	}
	if status := r.FormValue("status"); status != "" {
		user.Status = status
	}
	if r.Form.Has("note") {
		user.Note = r.FormValue("note")
	}

	if trafficLimitGBStr := r.FormValue("traffic_limit_gb"); trafficLimitGBStr != "" {
		gb, err := strconv.ParseFloat(trafficLimitGBStr, 64)
		if err != nil {
			htmxError(w, http.StatusBadRequest, "invalid traffic limit format")
			return
		}
		user.TrafficLimit = int64(math.Round(gb * 1024 * 1024 * 1024))
	}

	if resetStrategy := r.FormValue("reset_strategy"); resetStrategy != "" {
		user.DataLimitResetStrategy = resetStrategy
	}

	if expireAtStr := r.FormValue("expire_at"); expireAtStr != "" {
		t, err := time.ParseInLocation("2006-01-02", expireAtStr, time.Local)
		if err != nil {
			htmxError(w, http.StatusBadRequest, "invalid expiry format")
			return
		}
		user.ExpireAt = &t
	} else {
		if r.Form.Has("expire_at") {
			user.ExpireAt = nil
		}
	}

	if onHoldExpireStr := r.FormValue("on_hold_expire_at"); onHoldExpireStr != "" {
		t, err := time.ParseInLocation("2006-01-02", onHoldExpireStr, time.Local)
		if err != nil {
			htmxError(w, http.StatusBadRequest, "invalid on-hold expiry format")
			return
		}
		user.OnHoldExpireAt = &t
	} else {
		if r.Form.Has("on_hold_expire_at") {
			user.OnHoldExpireAt = nil
		}
	}

	if subToken := r.FormValue("sub_token"); subToken != "" {
		user.SubToken = subToken
	}

	if _, err := h.userStore.UpsertUser(user); err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to update user: "+err.Error())
		return
	}

	// 有提交 inbound_sync 标记时（无论是否选中），都同步 inbound 关联
	if r.Form.Has("inbound_sync") {
		affected, err := h.SyncUserInbounds(user.ID, r.Form["inbound_ids"])
		if err != nil {
			htmxError(w, http.StatusInternalServerError, "failed to sync inbounds: "+err.Error())
			return
		}
		h.ApplyNodes(affected)
	}

	h.renderUsersListFromStore(w)
}

func (h *Handler) deleteUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.userStore.DeleteUser(id); err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to delete user: "+err.Error())
		return
	}
	h.renderUsersListFromStore(w)
}

func (h *Handler) resetUserTraffic(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	user, err := h.userStore.GetUser(id)
	if err != nil {
		htmxError(w, http.StatusNotFound, "user not found")
		return
	}

	now := time.Now().UTC()
	user.UploadBytes = 0
	user.DownloadBytes = 0
	user.UsedBytes = 0
	user.RawUploadBytes = 0
	user.RawDownloadBytes = 0
	user.LastTrafficResetAt = &now

	if _, err := h.userStore.UpsertUser(user); err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to reset traffic: "+err.Error())
		return
	}

	h.renderUsersListFromStore(w)
}


type settingsData struct {
	ClientCert           string // 面板客户端证书 PEM，用于 node 安装时粘贴
	AnnouncementTitle   string
	AnnouncementContent string
	AnnouncementEnabled bool
	BarkURL             string // Bark 推送 URL
}

// settingsPage 渲染设置页面。
func (h *Handler) settingsPage(w http.ResponseWriter, r *http.Request) {
	var cert string
	if h.clientCertFile != "" {
		if data, err := os.ReadFile(h.clientCertFile); err == nil {
			cert = strings.TrimSpace(string(data))
		}
	}
	d := settingsData{ClientCert: cert}
	if h.settingsStore != nil {
		d.AnnouncementTitle, _ = h.settingsStore.GetSetting("announcement_title")
		d.AnnouncementContent, _ = h.settingsStore.GetSetting("announcement_content")
		enabled, _ := h.settingsStore.GetSetting("announcement_enabled")
		d.AnnouncementEnabled = enabled == "true"
		d.BarkURL, _ = h.settingsStore.GetSetting("alert_bark_url")
	}
	h.renderPage(w, r, "settings", pageData{
		Page:     "settings",
		Username: h.currentUsername(r),
		Data:     d,
	})
}

// saveAnnouncement 保存公告设置。
func (h *Handler) saveAnnouncement(w http.ResponseWriter, r *http.Request) {
	if h.settingsStore == nil {
		htmxError(w, http.StatusInternalServerError, "settings store unavailable")
		return
	}
	title := strings.TrimSpace(r.FormValue("announcement_title"))
	content := strings.TrimSpace(r.FormValue("announcement_content"))
	enabled := r.FormValue("announcement_enabled")
	if enabled != "true" {
		enabled = "false"
	}
	if err := h.settingsStore.SetSetting("announcement_title", title); err != nil {
		htmxError(w, http.StatusInternalServerError, "保存失败")
		return
	}
	if err := h.settingsStore.SetSetting("announcement_content", content); err != nil {
		htmxError(w, http.StatusInternalServerError, "保存失败")
		return
	}
	if err := h.settingsStore.SetSetting("announcement_enabled", enabled); err != nil {
		htmxError(w, http.StatusInternalServerError, "保存失败")
		return
	}
	w.Header().Set("HX-Reswap", "none")
	w.Header().Set("HX-Trigger", `{"showToast":{"msg":"公告已保存","type":"success"}}`)
	w.WriteHeader(http.StatusOK)
}

// saveAlertSettings 保存告警配置。
func (h *Handler) saveAlertSettings(w http.ResponseWriter, r *http.Request) {
	if h.settingsStore == nil {
		htmxError(w, http.StatusInternalServerError, "settings store unavailable")
		return
	}
	barkURL := strings.TrimSpace(r.FormValue("bark_url"))
	if err := h.settingsStore.SetSetting("alert_bark_url", barkURL); err != nil {
		htmxError(w, http.StatusInternalServerError, "保存失败")
		return
	}
	w.Header().Set("HX-Reswap", "none")
	w.Header().Set("HX-Trigger", `{"pulseToast":"告警设置已保存"}`)
	w.WriteHeader(http.StatusOK)
}

// testAlertSettings 发送一条测试推送，验证 Bark URL 是否正确。
func (h *Handler) testAlertSettings(w http.ResponseWriter, r *http.Request) {
	if h.settingsStore == nil {
		htmxError(w, http.StatusInternalServerError, "settings store unavailable")
		return
	}
	barkURL, ok := h.settingsStore.GetSetting("alert_bark_url")
	if !ok || strings.TrimSpace(barkURL) == "" {
		htmxError(w, http.StatusBadRequest, "请先保存 Bark URL")
		return
	}
	// 每次测试创建新实例，跳过去重缓存
	sender := alert.NewBarkSender(h.settingsStore)
	if err := sender.Send(r.Context(), "Pulse", "测试推送，若收到则配置正常"); err != nil {
		htmxError(w, http.StatusBadRequest, "推送失败: "+err.Error())
		return
	}
	w.Header().Set("HX-Reswap", "none")
	w.Header().Set("HX-Trigger", `{"pulseToast":"测试推送已发送"}`)
	w.WriteHeader(http.StatusOK)
}

// overlayLiveConns 用实时缓存的连接数据覆盖用户列表中的 Connections/Devices 字段。
// 缓存为空时原样返回（降级到 DB 值），不影响正常渲染。
func (h *Handler) overlayLiveConns(us []users.User) []users.User {
	h.userConnsCache.mu.RLock()
	conns := h.userConnsCache.data
	h.userConnsCache.mu.RUnlock()
	if len(conns) == 0 {
		return us
	}
	for i := range us {
		if c, ok := conns[us[i].Username]; ok {
			us[i].Connections = c[0]
			us[i].Devices = c[1]
		} else {
			us[i].Connections = 0
			us[i].Devices = 0
		}
	}
	return us
}

// userNodeUsageRow 用于模板展示的用户节点用量行。
type userNodeUsageRow struct {
	NodeName      string
	UploadBytes   int64
	DownloadBytes int64
	TotalBytes    int64
	Pct           int // 占该用户总用量的百分比
}

// nodeUsageModal 返回用户各节点流量分布的 modal 内容。
func (h *Handler) nodeUsageModal(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	user, err := h.userStore.GetUser(id)
	if err != nil {
		htmxError(w, http.StatusNotFound, "user not found")
		return
	}
	raw, err := h.userStore.ListUserNodeUsage(id)
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to get node usage: "+err.Error())
		return
	}

	// 构建 nodeID → name 映射
	nodeList, _ := h.nodeStore.List()
	nodeNames := make(map[string]string, len(nodeList))
	for _, n := range nodeList {
		nodeNames[n.ID] = n.Name
	}

	var total int64
	for _, u := range raw {
		total += u.UploadBytes + u.DownloadBytes
	}

	rows := make([]userNodeUsageRow, 0, len(raw))
	for _, u := range raw {
		t := u.UploadBytes + u.DownloadBytes
		pct := 0
		if total > 0 {
			pct = int(float64(t) / float64(total) * 100)
		}
		name := nodeNames[u.NodeID]
		if name == "" {
			name = u.NodeID
		}
		rows = append(rows, userNodeUsageRow{
			NodeName:      name,
			UploadBytes:   u.UploadBytes,
			DownloadBytes: u.DownloadBytes,
			TotalBytes:    t,
			Pct:           pct,
		})
	}

	h.renderPartial(w, "partial-node-usage-modal", struct {
		Username string
		Rows     []userNodeUsageRow
		Total    int64
	}{Username: user.Username, Rows: rows, Total: total})
}

// subLogsModal 返回用户订阅访问日志的 modal 内容。
func (h *Handler) subLogsModal(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	logs, err := h.userStore.ListSubAccessLogs(id, 50)
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to get access logs: "+err.Error())
		return
	}
	user, err := h.userStore.GetUser(id)
	if err != nil {
		htmxError(w, http.StatusNotFound, "user not found")
		return
	}
	h.renderPartial(w, "partial-sub-logs-modal", struct {
		Username string
		Logs     []users.SubAccessLog
	}{Username: user.Username, Logs: logs})
}

// renderUsersListFromStore 从 store 拉取最新用户列表并渲染 partial。
func (h *Handler) renderUsersListFromStore(w http.ResponseWriter) {
	allUsers, err := h.userStore.ListUsers()
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to get user list: "+err.Error())
		return
	}
	h.renderPartial(w, "partial-user-rows", h.overlayLiveConns(allUsers))
}

// fetchNodeStatus 查询单个节点的运行状态和版本信息。
// Runtime 和 Status 各自独立 3s 超时，避免共享 deadline 时前者慢导致后者超时。
func (h *Handler) fetchNodeStatus(ctx context.Context, n nodes.Node) nodeWithStatus {
	ns := nodeWithStatus{Node: n, Status: "offline"}
	client, err := h.dial(n.ID)
	if err != nil {
		return ns
	}
	rtCtx, rtCancel := context.WithTimeout(ctx, 3*time.Second)
	rt, rtErr := client.Runtime(rtCtx)
	rtCancel()
	if rtErr != nil {
		return ns
	}
	ns.SingboxVer = rt.Version
	ns.NodeVer = rt.NodeVersion
	stCtx, stCancel := context.WithTimeout(ctx, 3*time.Second)
	status, stErr := client.Status(stCtx)
	stCancel()
	if stErr == nil && status.Running {
		ns.Status = "online"
	} else {
		ns.Status = "idle"
	}
	return ns
}

// fetchNodeMetrics 并发查询单个节点的实时指标（Ping、Usage、速率）。
// dailyRaw 为全量日统计原始记录，方法内按节点 ID 过滤后计算 7 天趋势。
func (h *Handler) fetchNodeMetrics(ctx context.Context, node nodes.Node, dailyRaw []nodes.NodeDailyUsage) nodeMetrics {
	m := nodeMetrics{Node: node, Status: "offline", PingMs: -1}

	client, err := h.dial(node.ID)
	if err != nil {
		return m
	}

	// Ping = Runtime() 调用耗时（顺带获取版本信息）
	t0 := time.Now()
	rtCtx, rtCancel := context.WithTimeout(ctx, 3*time.Second)
	rt, rtErr := client.Runtime(rtCtx)
	rtCancel()
	if rtErr != nil {
		return m
	}
	m.PingMs = time.Since(t0).Milliseconds()
	m.SingboxVer = rt.Version
	m.NodeVer = rt.NodeVersion
	m.LastSyncAt = time.Now()

	// 获取 sing-box 运行状态及用量
	uCtx, uCancel := context.WithTimeout(ctx, 3*time.Second)
	stats, uErr := client.Usage(uCtx, false)
	uCancel()
	if uErr != nil {
		m.Status = "idle"
		return m
	}

	if stats.Running {
		m.Status = "online"
	} else {
		m.Status = "idle"
	}
	m.Connections = stats.Connections

	for _, u := range stats.Users {
		if u.Connections > 0 {
			m.OnlineUsers++
			m.OnlineDevices += u.Devices
		}
	}
	m.UserConns = stats.Users

	// 计算实时网速（与上次采样对比）
	now := time.Now()
	cur := nodeTrafficSample{UploadTotal: stats.UploadTotal, DownloadTotal: stats.DownloadTotal, SampledAt: now}
	if prev, ok := h.speedCache.Load(node.ID); ok {
		p := prev.(nodeTrafficSample)
		dt := now.Sub(p.SampledAt).Seconds()
		if dt > 0.1 {
			up := int64(float64(cur.UploadTotal-p.UploadTotal) / dt)
			dl := int64(float64(cur.DownloadTotal-p.DownloadTotal) / dt)
			if up > 0 {
				m.UploadSpeed = up
			}
			if dl > 0 {
				m.DownloadSpeed = dl
			}
		}
	}
	h.speedCache.Store(node.ID, cur)

	// 填充该节点支持的协议列表
	nodeInbounds, _ := h.ibStore.ListInboundsByNode(node.ID)
	seenP := make(map[string]bool)
	for _, ib := range nodeInbounds {
		label := protocolLabel(ib.Protocol)
		if !seenP[label] {
			seenP[label] = true
			m.Protocols = append(m.Protocols, label)
		}
	}

	// 过滤本节点的 7 天日统计，生成迷你柱状图数据
	var filtered []nodes.NodeDailyUsage
	for _, r := range dailyRaw {
		if r.NodeID == node.ID {
			filtered = append(filtered, r)
		}
	}
	m.DailyTraffic = usage.AggregateDailyTraffic(filtered, 7)
	for _, d := range m.DailyTraffic {
		m.PeriodUploadBytes += d.UploadBytes
		m.PeriodDownloadBytes += d.DownloadBytes
	}

	return m
}

const (
	metricsPollInterval = time.Second
	metricsIdleTimeout  = 2 * time.Minute
)

// pollAllNodes 并发查询所有节点指标，更新缓存并广播 SSE。
func (h *Handler) pollAllNodes(ctx context.Context) {
	nodeList, err := h.nodeStore.List()
	if err != nil {
		return
	}
	dailyRaw, _ := h.nodeStore.ListNodeDailyUsage(7)

	results := make([]nodeMetrics, len(nodeList))
	var wg sync.WaitGroup
	for i, node := range nodeList {
		wg.Add(1)
		go func(idx int, n nodes.Node) {
			defer wg.Done()
			results[idx] = h.fetchNodeMetrics(ctx, n, dailyRaw)
		}(i, node)
	}
	wg.Wait()

	// 更新缓存
	h.metricsCache.mu.Lock()
	h.metricsCache.data = results
	h.metricsCache.at = time.Now()
	h.metricsCache.mu.Unlock()

	// 聚合各节点 per-user 连接数到独立缓存，供用户列表实时叠加
	newConns := make(map[string][2]int)
	for _, r := range results {
		for _, u := range r.UserConns {
			if u.Connections > 0 {
				prev := newConns[u.User]
				newConns[u.User] = [2]int{prev[0] + u.Connections, prev[1] + u.Devices}
			}
		}
	}
	h.userConnsCache.mu.Lock()
	h.userConnsCache.data = newConns
	h.userConnsCache.mu.Unlock()

	// 广播给所有 SSE 订阅者
	h.hub.broadcast(h, results)
}

// Start 启动后台指标轮询 goroutine。
func (h *Handler) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(metricsPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// 无人访问时暂停轮询
				last := time.Unix(0, h.lastAccessAt.Load())
				if !last.IsZero() && time.Since(last) > metricsIdleTimeout {
					continue
				}
				// 上轮未完成时跳过
				if !h.polling.CompareAndSwap(false, true) {
					continue
				}
				go func() {
					defer h.polling.Store(false)
					pCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
					defer cancel()
					h.pollAllNodes(pCtx)
				}()
			}
		}
	}()
}

// renderToString 将模板渲染为字符串，失败时返回空字符串。
func (h *Handler) renderToString(tmplName string, data any) string {
	var buf strings.Builder
	pd := pageData{Data: data}
	if err := h.tmpl.ExecuteTemplate(&buf, tmplName, pd); err != nil {
		return ""
	}
	return buf.String()
}

// htmlToSSEData 将 HTML 换行转换为 SSE 多行 data 格式。
func htmlToSSEData(html string) string {
	return strings.ReplaceAll(strings.TrimSpace(html), "\n", "\ndata: ")
}

// getUserNodeIDs 返回用户可访问的 nodeID 集合。
func (h *Handler) getUserNodeIDs(userID string) map[string]struct{} {
	accesses, _ := h.userStore.ListUserInboundsByUser(userID)
	ids := make(map[string]struct{})
	for _, acc := range accesses {
		ids[acc.NodeID] = struct{}{}
	}
	return ids
}

// metricsToUserNodeInfos 将 []nodeMetrics 转为 []userNodeInfo。
func metricsToUserNodeInfos(ms []nodeMetrics) []userNodeInfo {
	infos := make([]userNodeInfo, 0, len(ms))
	for _, m := range ms {
		info := userNodeInfo{
			Name:          m.Node.Name,
			Protocols:     m.Protocols,
			Status:        m.Status,
			OnlineUsers:   m.OnlineUsers,
			UploadSpeed:   m.UploadSpeed,
			DownloadSpeed: m.DownloadSpeed,
		}
		infos = append(infos, info)
	}
	return infos
}

// nodeMetricsPartial 返回所有节点的实时指标区块（读缓存，供首屏加载用）。
func (h *Handler) nodeMetricsPartial(w http.ResponseWriter, r *http.Request) {
	h.lastAccessAt.Store(time.Now().UnixNano())
	h.metricsCache.mu.RLock()
	data := h.metricsCache.data
	h.metricsCache.mu.RUnlock()
	h.renderPartial(w, "partial-node-metrics", data)
}

// nodeMetricsStreamHandler SSE 端点：推送节点指标 HTML 给管理端浏览器。
func (h *Handler) nodeMetricsStreamHandler(w http.ResponseWriter, r *http.Request) {
	h.lastAccessAt.Store(time.Now().UnixNano())
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	sub := h.hub.subscribe(nil) // nil = 管理端，订阅所有节点
	defer h.hub.unsubscribe(sub)

	// 首次立即推送当前缓存
	h.metricsCache.mu.RLock()
	cur := h.metricsCache.data
	h.metricsCache.mu.RUnlock()
	if cur != nil {
		html := h.renderToString("partial-node-metrics", cur)
		fmt.Fprintf(w, "event: update\ndata: %s\n\n", htmlToSSEData(html))
		w.(http.Flusher).Flush()
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case html := <-sub.ch:
			h.lastAccessAt.Store(time.Now().UnixNano())
			fmt.Fprintf(w, "event: update\ndata: %s\n\n", htmlToSSEData(html))
			w.(http.Flusher).Flush()
		}
	}
}

// userNodeStatusPartial 返回用户主页的节点状态区块（读缓存，以 sub_token 鉴权）。
func (h *Handler) userNodeStatusPartial(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("sub_token")
	user, err := h.userStore.GetUserBySubToken(token)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h.lastAccessAt.Store(time.Now().UnixNano())
	nodeIDs := h.getUserNodeIDs(user.ID)

	h.metricsCache.mu.RLock()
	all := h.metricsCache.data
	h.metricsCache.mu.RUnlock()

	var filtered []nodeMetrics
	for _, m := range all {
		if _, ok := nodeIDs[m.Node.ID]; ok {
			filtered = append(filtered, m)
		}
	}
	h.renderPartial(w, "partial-user-node-status", metricsToUserNodeInfos(filtered))
}

// userNodeStatusStreamHandler SSE 端点：推送节点状态 HTML 给用户端浏览器（以 sub_token 鉴权）。
func (h *Handler) userNodeStatusStreamHandler(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("sub_token")
	user, err := h.userStore.GetUserBySubToken(token)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h.lastAccessAt.Store(time.Now().UnixNano())
	nodeIDs := h.getUserNodeIDs(user.ID)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	sub := h.hub.subscribe(nodeIDs)
	defer h.hub.unsubscribe(sub)

	// 首次立即推送当前缓存
	h.metricsCache.mu.RLock()
	all := h.metricsCache.data
	h.metricsCache.mu.RUnlock()
	var filtered []nodeMetrics
	for _, m := range all {
		if _, ok := nodeIDs[m.Node.ID]; ok {
			filtered = append(filtered, m)
		}
	}
	if filtered != nil {
		html := h.renderToString("partial-user-node-status", metricsToUserNodeInfos(filtered))
		fmt.Fprintf(w, "event: update\ndata: %s\n\n", htmlToSSEData(html))
		w.(http.Flusher).Flush()
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case html := <-sub.ch:
			h.lastAccessAt.Store(time.Now().UnixNano())
			fmt.Fprintf(w, "event: update\ndata: %s\n\n", htmlToSSEData(html))
			w.(http.Flusher).Flush()
		}
	}
}

func (h *Handler) nodesListPartial(w http.ResponseWriter, r *http.Request) {
	nodeList, err := h.nodeStore.List()
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to get node list: "+err.Error())
		return
	}
	checkMap, _ := h.nodeStore.ListAllNodeCheckResults()
	speedMap, _ := h.nodeStore.ListAllNodeSpeedTests()
	uptimeMap, _ := h.nodeStore.ListNodeUptimeSummary(7)
	result := make([]nodeWithStatus, 0, len(nodeList))
	for _, n := range nodeList {
		ns := h.fetchNodeStatus(r.Context(), n)
		ns.DirectChecks, ns.ProxiedChecks = splitCheckResults(checkMap[n.ID])
		if st, ok := speedMap[n.ID]; ok {
			ns.SpeedTest = &st
		}
		ns.Uptime = uptimeMap[n.ID]
		result = append(result, ns)
	}
	h.renderPartial(w, "partial-node-rows", result)
}

func (h *Handler) nodeNewForm(w http.ResponseWriter, r *http.Request) {
	h.renderPartial(w, "partial-node-new-form", nil)
}

func (h *Handler) createNode(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	baseURL := r.FormValue("base_url")

	if name == "" || baseURL == "" {
		htmxError(w, http.StatusBadRequest, "name and address are required")
		return
	}

	trafficRate := parseTrafficRate(r.FormValue("traffic_rate"))
	newNode := nodes.Node{
		ID:          idgen.NextString(),
		Name:        name,
		BaseURL:     baseURL,
		TrafficRate: trafficRate,
	}

	if _, err := h.nodeStore.Upsert(newNode); err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to create node: "+err.Error())
		return
	}

	h.renderNodesListFromStore(w, r)
}

func (h *Handler) deleteNode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.nodeStore.Delete(id); err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to delete node: "+err.Error())
		return
	}
	h.renderNodesListFromStore(w, r)
}

// restartNode 从服务端重新生成配置并推送到节点后重启 sing-box。
func (h *Handler) restartNode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	client, err := h.dial(id)
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to connect to node: "+err.Error())
		return
	}

	nodeInbounds, err := h.ibStore.ListInboundsByNode(id)
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to get inbound config: "+err.Error())
		return
	}

	userAccesses, err := h.userStore.ListUserInboundsByNode(id)
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to get user credentials: "+err.Error())
		return
	}

	userIDs := collectUserIDs(userAccesses)
	userMap, err := h.userStore.GetUsersByIDs(userIDs)
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to get user data: "+err.Error())
		return
	}

	node, err := h.nodeStore.Get(id)
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to get node: "+err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	status, _, err := jobs.ApplyNodeUsers(ctx, client, nodeInbounds, userAccesses, userMap, h.ibStore, h.outboundStore, h.applyOpts, node)
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to restart node: "+err.Error())
		return
	}

	msg := "config applied, sing-box is running"
	if !status.Running {
		msg = "config applied, but sing-box failed to start"
	}
	setHXTriggerToast(w, msg)
	h.nodesListPartial(w, r)
}

func (h *Handler) startNode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	client, err := h.dial(id)
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to connect to node: "+err.Error())
		return
	}

	nodeInbounds, err := h.ibStore.ListInboundsByNode(id)
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to get inbound config: "+err.Error())
		return
	}

	userAccesses, err := h.userStore.ListUserInboundsByNode(id)
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to get user credentials: "+err.Error())
		return
	}

	userIDs := collectUserIDs(userAccesses)
	userMap, err := h.userStore.GetUsersByIDs(userIDs)
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to get user data: "+err.Error())
		return
	}

	node, err := h.nodeStore.Get(id)
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to get node: "+err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	if _, _, err := jobs.ApplyNodeUsers(ctx, client, nodeInbounds, userAccesses, userMap, h.ibStore, h.outboundStore, h.applyOpts, node); err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to start node: "+err.Error())
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) stopNode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	client, err := h.dial(id)
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to connect to node: "+err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	if _, err := client.Stop(ctx); err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to stop node: "+err.Error())
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) nodeEditForm(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	node, err := h.nodeStore.Get(id)
	if err != nil {
		htmxError(w, http.StatusNotFound, "node not found")
		return
	}
	h.renderPartial(w, "partial-node-edit-form", node)
}

// nodeConfigData 传给配置弹窗模板的数据。
type nodeConfigData struct {
	NodeID   string
	NodeName string
	Config   string
}

func (h *Handler) nodeConfigModal(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	node, err := h.nodeStore.Get(id)
	if err != nil {
		htmxError(w, http.StatusNotFound, "node not found")
		return
	}
	client, err := h.dial(id)
	if err != nil {
		htmxError(w, http.StatusBadGateway, "failed to connect to node: "+err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	resp, err := client.Config(ctx)
	if err != nil {
		htmxError(w, http.StatusBadGateway, "failed to get config: "+err.Error())
		return
	}
	// 格式化 JSON
	config := resp.Config
	var pretty []byte
	var buf interface{}
	if jsonErr := json.Unmarshal([]byte(config), &buf); jsonErr == nil {
		if pretty, jsonErr = json.MarshalIndent(buf, "", "  "); jsonErr == nil {
			config = string(pretty)
		}
	}
	h.renderPartial(w, "partial-node-config", nodeConfigData{
		NodeID:   id,
		NodeName: node.Name,
		Config:   config,
	})
}

// applyNodeConfig 接收管理员手动编辑的 sing-box JSON 配置并直接下发到节点。
func (h *Handler) applyNodeConfig(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	config := r.FormValue("config")
	if strings.TrimSpace(config) == "" {
		htmxError(w, http.StatusBadRequest, "config is empty")
		return
	}

	client, err := h.dial(id)
	if err != nil {
		htmxError(w, http.StatusBadGateway, "failed to connect to node: "+err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	status, err := client.Restart(ctx, nodes.ConfigRequest{Config: config})
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to apply config: "+err.Error())
		return
	}

	msg := "自定义配置已下发，sing-box 正在运行"
	if !status.Running {
		msg = "配置已下发，但 sing-box 启动失败"
	}
	w.Header().Set("HX-Trigger", `{"toast":"`+msg+`"}`)
	h.renderNodesListFromStore(w, r)
}

func (h *Handler) nodeLogsModal(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	node, err := h.nodeStore.Get(id)
	if err != nil {
		htmxError(w, http.StatusNotFound, "node not found")
		return
	}
	type logsData struct {
		NodeName string
		NodeID   string
	}
	h.renderPartial(w, "partial-node-logs", logsData{
		NodeName: node.Name,
		NodeID:   id,
	})
}

func (h *Handler) nodeLogsStream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	client, err := h.dial(id)
	if err != nil {
		http.Error(w, "failed to connect to node: "+err.Error(), http.StatusBadGateway)
		return
	}
	body, err := client.LogsStream(r.Context())
	if err != nil {
		http.Error(w, "failed to stream logs: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer body.Close()

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")

	buf := make([]byte, 4096)
	for {
		n, readErr := body.Read(buf)
		if n > 0 {
			_, _ = w.Write(buf[:n])
			flusher.Flush()
		}
		if readErr != nil {
			return
		}
	}
}

func (h *Handler) updateNode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	node, err := h.nodeStore.Get(id)
	if err != nil {
		htmxError(w, http.StatusNotFound, "node not found")
		return
	}

	if name := r.FormValue("name"); name != "" {
		node.Name = name
	}
	if baseURL := r.FormValue("base_url"); baseURL != "" {
		node.BaseURL = baseURL
	}
	if rateStr := r.FormValue("traffic_rate"); rateStr != "" {
		node.TrafficRate = parseTrafficRate(rateStr)
	}
	if _, err := h.nodeStore.Upsert(node); err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to update node: "+err.Error())
		return
	}
	h.renderNodesListFromStore(w, r)
}

// ─── 辅助：节点列表渲染 ──────────────────────────────────────────────────────

func (h *Handler) renderNodesListFromStore(w http.ResponseWriter, r *http.Request) {
	nodeList, err := h.nodeStore.List()
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to get node list: "+err.Error())
		return
	}
	checkMap, _ := h.nodeStore.ListAllNodeCheckResults()
	speedMap, _ := h.nodeStore.ListAllNodeSpeedTests()
	uptimeMap, _ := h.nodeStore.ListNodeUptimeSummary(7)
	result := make([]nodeWithStatus, 0, len(nodeList))
	for _, n := range nodeList {
		ns := h.fetchNodeStatus(r.Context(), n)
		ns.DirectChecks, ns.ProxiedChecks = splitCheckResults(checkMap[n.ID])
		if st, ok := speedMap[n.ID]; ok {
			ns.SpeedTest = &st
		}
		ns.Uptime = uptimeMap[n.ID]
		result = append(result, ns)
	}
	h.renderPartial(w, "partial-node-rows", result)
}

// nodeSpeedTest 触发节点测速（下载 + 上传各 10MB），结果写入 store 并返回局部 HTML。
func (h *Handler) nodeSpeedTest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	client, err := h.dial(id)
	if err != nil {
		htmxError(w, http.StatusBadGateway, "无法连接节点: "+err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 70*time.Second)
	defer cancel()

	resp, err := client.SpeedTest(ctx)
	if err != nil {
		htmxError(w, http.StatusBadGateway, "测速失败: "+err.Error())
		return
	}

	result := nodes.SpeedTestResult{
		DownBps:  resp.DownBps,
		UpBps:    resp.UpBps,
		TestedAt: time.Now().UTC(),
	}

	_ = h.nodeStore.UpsertNodeSpeedTest(id, result)

	h.renderPartial(w, "partial-node-speedtest", speedTestPartialData{
		NodeID: id,
		Result: result,
	})
}

// nodeCheckUnlock 触发节点解锁检测，结果写入 store 并返回局部 HTML。
func (h *Handler) nodeCheckUnlock(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	client, err := h.dial(id)
	if err != nil {
		htmxError(w, http.StatusBadGateway, "无法连接节点: "+err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	resp, err := client.CheckUnlock(ctx)
	if err != nil {
		htmxError(w, http.StatusBadGateway, "检测失败: "+err.Error())
		return
	}

	now := time.Now().UTC()
	toCheckResults := func(items []nodes.CheckUnlockResult, checkType string) []nodes.CheckResult {
		out := make([]nodes.CheckResult, 0, len(items))
		for _, cr := range items {
			out = append(out, nodes.CheckResult{
				Service:   cr.Service,
				CheckType: checkType,
				Unlocked:  cr.Unlocked,
				Region:    cr.Region,
				Note:      cr.Note,
				CheckedAt: now,
			})
		}
		return out
	}

	directResults := toCheckResults(resp.Direct, "direct")
	proxiedResults := toCheckResults(resp.Proxied, "proxied")

	all := append(directResults, proxiedResults...)
	_ = h.nodeStore.UpsertNodeCheckResults(id, all)

	h.renderPartial(w, "partial-node-check-results", checkResultsPartialData{
		NodeID:        id,
		DirectChecks:  directResults,
		ProxiedChecks: proxiedResults,
	})
}

// splitCheckResults 将混合结果列表按 check_type 拆分为 direct / proxied 两组。
func splitCheckResults(all []nodes.CheckResult) (direct, proxied []nodes.CheckResult) {
	for _, cr := range all {
		if cr.CheckType == "proxied" {
			proxied = append(proxied, cr)
		} else {
			direct = append(direct, cr)
		}
	}
	return
}

// userFormData 用户表单页面数据，包含 inbound 列表。
type userFormData struct {
	User               *users.User
	Inbounds           []inbounds.Inbound
	SelectedInboundIDs map[string]bool   // inboundID → true，用于编辑表单回显已选中状态
	NodeMap            map[string]string // nodeID → 节点名称，用于 inbound 列表显示
}

// buildNodeMap 返回 nodeID → 节点名称的映射，加载失败时返回空 map。
func (h *Handler) buildNodeMap() map[string]string {
	nodeList, err := h.nodeStore.List()
	m := make(map[string]string, len(nodeList))
	if err != nil {
		return m
	}
	for _, n := range nodeList {
		m[n.ID] = n.Name
	}
	return m
}

// applyNodes 异步将最新用户配置下发到指定节点列表（后台执行，不阻塞请求）。
// ApplyNodes pushes config to the given nodes. Exported for payment webhook use.
func (h *Handler) ApplyNodes(nodeIDs []string) {
	for _, nodeID := range nodeIDs {
		go func(id string) {
			client, err := h.dial(id)
			if err != nil {
				log.Printf("applyNodes: dial %s: %v", id, err)
				return
			}
			nodeInbounds, err := h.ibStore.ListInboundsByNode(id)
			if err != nil {
				log.Printf("applyNodes: list inbounds %s: %v", id, err)
				return
			}
			userAccesses, err := h.userStore.ListUserInboundsByNode(id)
			if err != nil {
				log.Printf("applyNodes: list user accesses %s: %v", id, err)
				return
			}
			userIDs := collectUserIDs(userAccesses)
			userMap, err := h.userStore.GetUsersByIDs(userIDs)
			if err != nil {
				log.Printf("applyNodes: get users %s: %v", id, err)
				return
			}
			n, _ := h.nodeStore.Get(id)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if _, _, err := jobs.ApplyNodeUsers(ctx, client, nodeInbounds, userAccesses, userMap, h.ibStore, h.outboundStore, h.applyOpts, n); err != nil {
				log.Printf("applyNodes: apply %s: %v", id, err)
			}
		}(nodeID)
	}
}

// syncUserInbounds 根据选中的 inbound ID 列表同步用户的 inbound 访问凭据。
// 新增：为未有凭据的 inbound 创建 UserInbound；删除：移除未选中 inbound 的旧记录。
// 返回所有发生变更的 nodeID（新增 + 删除），供调用方触发配置下发。
// SyncUserInbounds reconciles a user's inbound assignments. Exported for payment webhook use.
func (h *Handler) SyncUserInbounds(userID string, selectedInboundIDs []string) ([]string, error) {
	// 收集选中的 inbound → nodeID 映射
	wantedInbounds := make(map[string]inbounds.Inbound) // inboundID → Inbound
	for _, ibID := range selectedInboundIDs {
		ib, err := h.ibStore.GetInbound(ibID)
		if err != nil {
			continue
		}
		wantedInbounds[ibID] = ib
	}

	// 获取该用户现有凭据，按 inbound_id 索引
	existing, err := h.userStore.ListUserInboundsByUser(userID)
	if err != nil {
		return nil, err
	}
	existingByInbound := make(map[string]users.UserInbound, len(existing))
	for _, acc := range existing {
		existingByInbound[acc.InboundID] = acc
	}

	changedNodeIDs := make(map[string]struct{})

	// 创建新增 inbound 的凭据
	for ibID, ib := range wantedInbounds {
		if _, ok := existingByInbound[ibID]; !ok {
			secret := panelRandomToken(12)
			if ib.Protocol == "shadowsocks" && strings.HasPrefix(ib.Method, "2022-") {
				secret = generateSSPassword(ib.Method)
			}
			acc := users.UserInbound{
				ID:        idgen.NextString(),
				UserID:    userID,
				InboundID: ibID,
				NodeID:    ib.NodeID,
				UUID:      panelRandomUUID(),
				Secret:    secret,
			}
			if _, err := h.userStore.UpsertUserInbound(acc); err != nil {
				return nil, err
			}
			changedNodeIDs[ib.NodeID] = struct{}{}
		}
	}

	// 删除不再选中 inbound 的凭据
	for ibID, acc := range existingByInbound {
		if _, wanted := wantedInbounds[ibID]; !wanted {
			if err := h.userStore.DeleteUserInbound(acc.ID); err != nil {
				return nil, err
			}
			changedNodeIDs[acc.NodeID] = struct{}{}
		}
	}

	affected := make([]string, 0, len(changedNodeIDs))
	for id := range changedNodeIDs {
		affected = append(affected, id)
	}
	return affected, nil
}

// collectUserIDs 从 userAccesses 列表中提取去重后的 UserID。
func collectUserIDs(accesses []users.UserInbound) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, acc := range accesses {
		if _, ok := seen[acc.UserID]; !ok {
			seen[acc.UserID] = struct{}{}
			out = append(out, acc.UserID)
		}
	}
	return out
}

// ─── Inbound Handlers ────────────────────────────────────────────────────────

func (h *Handler) inboundsPage(w http.ResponseWriter, r *http.Request) {
	h.renderPage(w, r, "inbounds", pageData{
		Page:     "inbounds",
		Username: h.currentUsername(r),
	})
}

func (h *Handler) outboundsPage(w http.ResponseWriter, r *http.Request) {
	h.renderPage(w, r, "outbounds", pageData{
		Page:     "outbounds",
		Username: h.currentUsername(r),
	})
}

func (h *Handler) inboundsListPartial(w http.ResponseWriter, r *http.Request) {
	list, err := h.ibStore.ListInbounds()
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to get inbound list: "+err.Error())
		return
	}
	h.renderPartial(w, "partial-inbound-rows", inboundListData{Inbounds: list, NodeMap: h.buildNodeMap()})
}

// inboundFormData 传给 Inbound 表单模板的数据。
type inboundFormData struct {
	Inbound   *inbounds.Inbound
	Nodes     []nodes.Node
	Outbounds []outbounds.Outbound
}

func (h *Handler) inboundNewForm(w http.ResponseWriter, r *http.Request) {
	nodeList, err := h.nodeStore.List()
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to get node list: "+err.Error())
		return
	}
	obList, _ := h.outboundStore.List()
	h.renderPartial(w, "partial-inbound-new-form", inboundFormData{
		Nodes:     nodeList,
		Outbounds: obList,
	})
}

func (h *Handler) createInbound(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		htmxError(w, http.StatusBadRequest, "invalid form data")
		return
	}
	nodeIDs := r.Form["node_ids"]
	protocol := r.FormValue("protocol")
	portStr := r.FormValue("port")
	tag := r.FormValue("tag")

	if protocol == "" || portStr == "" || tag == "" || len(nodeIDs) == 0 {
		htmxError(w, http.StatusBadRequest, "node, protocol, port and tag are required")
		return
	}

	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		htmxError(w, http.StatusBadRequest, "invalid port")
		return
	}

	// 在每个选中节点上分别创建 inbound（密钥独立生成）
	for _, nodeID := range nodeIDs {
		ib := inbounds.Inbound{
			ID:                   idgen.NextString(),
			NodeID:               nodeID,
			Protocol:             protocol,
			Tag:                  tag,
			Port:                 port,
			Method:               r.FormValue("method"),
			Security:             r.FormValue("security"),
			RealityHandshakeAddr: r.FormValue("reality_handshake_addr"),
			OutboundID:           r.FormValue("outbound_id"),
		}

		// VLESS Reality：每个节点独立生成密钥对和 Short ID
		if protocol == "vless" && ib.Security == "reality" {
			priv, pub, err := generateRealityKeypair()
			if err != nil {
				htmxError(w, http.StatusInternalServerError, "failed to generate Reality keypair: "+err.Error())
				return
			}
			ib.RealityPrivateKey = priv
			ib.RealityPublicKey = pub
			ib.RealityShortID = generateRealityShortID()
		}

		// Shadowsocks：每个节点独立生成服务端 PSK
		if protocol == "shadowsocks" {
			ib.Password = generateSSPassword(r.FormValue("method"))
		}

		if _, err := h.ibStore.UpsertInbound(ib); err != nil {
			htmxError(w, http.StatusInternalServerError, "failed to create inbound: "+err.Error())
			return
		}
		h.applyInboundNode(nodeID)

		// 自动创建默认 host，从节点 BaseURL 提取地址
		if node, err := h.nodeStore.Get(nodeID); err == nil {
			hostAddr := nodeID
			if parsed, err := url.Parse(node.BaseURL); err == nil {
				hostAddr = parsed.Hostname()
			}
			defaultHost := inbounds.Host{
				ID:        idgen.NextString(),
				InboundID: ib.ID,
				Remark:    tag,
				Address:   hostAddr,
				Port:      port,
			}
			_, _ = h.ibStore.UpsertHost(defaultHost)
		}
	}

	h.renderInboundsListFromStore(w)
}

func (h *Handler) inboundEditForm(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ib, err := h.ibStore.GetInbound(id)
	if err != nil {
		htmxError(w, http.StatusNotFound, "inbound not found")
		return
	}
	obList, _ := h.outboundStore.List()
	h.renderPartial(w, "partial-inbound-edit-form", inboundFormData{
		Inbound:   &ib,
		Outbounds: obList,
	})
}

func (h *Handler) updateInbound(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ib, err := h.ibStore.GetInbound(id)
	if err != nil {
		htmxError(w, http.StatusNotFound, "inbound not found")
		return
	}

	if protocol := r.FormValue("protocol"); protocol != "" {
		ib.Protocol = protocol
	}
	if portStr := r.FormValue("port"); portStr != "" {
		port, err := strconv.Atoi(portStr)
		if err != nil || port <= 0 || port > 65535 {
			htmxError(w, http.StatusBadRequest, "invalid port")
			return
		}
		ib.Port = port
	}
	if tag := r.FormValue("tag"); tag != "" {
		ib.Tag = tag
	}
	ib.Security = r.FormValue("security")
	ib.Method = r.FormValue("method")
	if pw := r.FormValue("ss_password"); pw != "" {
		ib.Password = pw
	}
	ib.RealityPrivateKey = r.FormValue("reality_private_key")
	ib.RealityPublicKey = r.FormValue("reality_public_key")
	ib.RealityHandshakeAddr = r.FormValue("reality_handshake_addr")
	ib.RealityShortID = r.FormValue("reality_short_id")
	ib.OutboundID = r.FormValue("outbound_id")

	if _, err := h.ibStore.UpsertInbound(ib); err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to update inbound: "+err.Error())
		return
	}
	h.applyInboundNode(ib.NodeID)
	h.renderInboundsListFromStore(w)
}

func (h *Handler) deleteInbound(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ib, err := h.ibStore.GetInbound(id)
	if err != nil {
		htmxError(w, http.StatusNotFound, "inbound not found")
		return
	}
	if err := h.ibStore.DeleteInbound(id); err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to delete inbound: "+err.Error())
		return
	}
	h.applyInboundNode(ib.NodeID)
	h.renderInboundsListFromStore(w)
}

// inboundUsersModalData 传给「分配用户」弹窗模板的数据。
type inboundUsersModalData struct {
	Inbound         inbounds.Inbound
	Users           []users.User
	AssignedUserIDs map[string]bool // userID → 已分配
}

// inboundUsersModal 展示某个 inbound 的用户分配弹窗（GET）。
func (h *Handler) inboundUsersModal(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ib, err := h.ibStore.GetInbound(id)
	if err != nil {
		htmxError(w, http.StatusNotFound, "inbound not found")
		return
	}
	allUsers, err := h.userStore.ListUsers()
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to get users: "+err.Error())
		return
	}
	existing, err := h.userStore.ListUserInboundsByInbound(id)
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to get assignments: "+err.Error())
		return
	}
	assigned := make(map[string]bool, len(existing))
	for _, acc := range existing {
		assigned[acc.UserID] = true
	}
	h.renderPartial(w, "partial-inbound-users-modal", inboundUsersModalData{
		Inbound:         ib,
		Users:           allUsers,
		AssignedUserIDs: assigned,
	})
}

// updateInboundUsers 根据提交的用户 ID 列表更新 inbound 的用户分配（POST）。
func (h *Handler) updateInboundUsers(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ib, err := h.ibStore.GetInbound(id)
	if err != nil {
		htmxError(w, http.StatusNotFound, "inbound not found")
		return
	}
	if err := r.ParseForm(); err != nil {
		htmxError(w, http.StatusBadRequest, "invalid form data")
		return
	}
	selectedUserIDs := r.Form["user_ids"]

	wanted := make(map[string]struct{}, len(selectedUserIDs))
	for _, uid := range selectedUserIDs {
		wanted[uid] = struct{}{}
	}

	existing, err := h.userStore.ListUserInboundsByInbound(id)
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to get assignments: "+err.Error())
		return
	}
	existingByUser := make(map[string]users.UserInbound, len(existing))
	for _, acc := range existing {
		existingByUser[acc.UserID] = acc
	}

	// 新增未分配的用户凭据
	for _, uid := range selectedUserIDs {
		if _, ok := existingByUser[uid]; !ok {
			secret := panelRandomToken(12)
			if ib.Protocol == "shadowsocks" && strings.HasPrefix(ib.Method, "2022-") {
				secret = generateSSPassword(ib.Method)
			}
			acc := users.UserInbound{
				ID:        idgen.NextString(),
				UserID:    uid,
				InboundID: id,
				NodeID:    ib.NodeID,
				UUID:      panelRandomUUID(),
				Secret:    secret,
			}
			if _, err := h.userStore.UpsertUserInbound(acc); err != nil {
				htmxError(w, http.StatusInternalServerError, "failed to add user: "+err.Error())
				return
			}
		}
	}

	// 移除已取消选中的用户凭据
	for uid, acc := range existingByUser {
		if _, ok := wanted[uid]; !ok {
			if err := h.userStore.DeleteUserInbound(acc.ID); err != nil {
				htmxError(w, http.StatusInternalServerError, "failed to remove user: "+err.Error())
				return
			}
		}
	}

	h.applyInboundNode(ib.NodeID)
	w.Header().Set("HX-Trigger", `{"pulseToast":"用户分配已保存"}`)
	w.WriteHeader(http.StatusOK)
}

type inboundListData struct {
	Inbounds []inbounds.Inbound
	NodeMap  map[string]string // nodeID → 节点名称
}

func (h *Handler) renderInboundsListFromStore(w http.ResponseWriter) {
	list, err := h.ibStore.ListInbounds()
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to get inbound list: "+err.Error())
		return
	}
	h.renderPartial(w, "partial-inbound-rows", inboundListData{Inbounds: list, NodeMap: h.buildNodeMap()})
}

// applyInboundNode 在后台异步将指定节点的最新配置下发到节点（inbound 变更后调用）。
func (h *Handler) applyInboundNode(nodeID string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := jobs.ApplyNode(ctx, nodeID, h.nodeStore, h.userStore, h.ibStore, h.outboundStore, h.dial, h.applyOpts); err != nil {
			log.Printf("warn: apply node %s after inbound change: %v", nodeID, err)
		}
	}()
}

// ─── Host Handlers ────────────────────────────────────────────────────────────

func (h *Handler) hostsModal(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ib, err := h.ibStore.GetInbound(id)
	if err != nil {
		htmxError(w, http.StatusNotFound, "inbound not found")
		return
	}
	hosts, err := h.ibStore.ListHostsByInbound(id)
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to get host list: "+err.Error())
		return
	}
	h.renderPartial(w, "partial-hosts-modal", inboundHostsData{Inbound: ib, Hosts: hosts})
}

func (h *Handler) createHost(w http.ResponseWriter, r *http.Request) {
	ibID := r.PathValue("id")
	ib, err := h.ibStore.GetInbound(ibID)
	if err != nil {
		htmxError(w, http.StatusNotFound, "inbound not found")
		return
	}
	address := r.FormValue("address")
	if address == "" {
		htmxError(w, http.StatusBadRequest, "address is required")
		return
	}
	port := 0
	if portStr := r.FormValue("port"); portStr != "" {
		p, err := strconv.Atoi(portStr)
		if err != nil || p < 0 || p > 65535 {
			htmxError(w, http.StatusBadRequest, "invalid port")
			return
		}
		port = p
	}
	host := inbounds.Host{
		ID:            idgen.NextString(),
		InboundID:     ibID,
		Remark:        r.FormValue("remark"),
		Address:       address,
		Port:          port,
		SNI:           r.FormValue("sni"),
		Security:      r.FormValue("security"),
		Path:          r.FormValue("path"),
		AllowInsecure: r.FormValue("allow_insecure") == "1",
		MuxEnable:     r.FormValue("mux_enable") == "1",
		Fingerprint:   r.FormValue("fingerprint"),
	}
	if _, err := h.ibStore.UpsertHost(host); err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to create host: "+err.Error())
		return
	}
	hosts, _ := h.ibStore.ListHostsByInbound(ibID)
	h.renderPartial(w, "partial-host-rows", inboundHostsData{Inbound: ib, Hosts: hosts})
}

func (h *Handler) hostEditForm(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	host, err := h.ibStore.GetHost(id)
	if err != nil {
		htmxError(w, http.StatusNotFound, "host not found")
		return
	}
	ib, _ := h.ibStore.GetInbound(host.InboundID)
	h.renderPartial(w, "partial-host-edit-form", inboundHostsData{Inbound: ib, Hosts: []inbounds.Host{host}})
}

func (h *Handler) updateHost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	host, err := h.ibStore.GetHost(id)
	if err != nil {
		htmxError(w, http.StatusNotFound, "host not found")
		return
	}
	if address := r.FormValue("address"); address != "" {
		host.Address = address
	}
	host.Remark = r.FormValue("remark")
	host.SNI = r.FormValue("sni")
	host.Security = r.FormValue("security")
	host.Path = r.FormValue("path")
	host.AllowInsecure = r.FormValue("allow_insecure") == "1"
	host.MuxEnable = r.FormValue("mux_enable") == "1"
	host.Fingerprint = r.FormValue("fingerprint")
	if portStr := r.FormValue("port"); portStr != "" {
		if p, err := strconv.Atoi(portStr); err == nil && p >= 0 && p <= 65535 {
			host.Port = p
		}
	} else {
		host.Port = 0
	}
	if _, err := h.ibStore.UpsertHost(host); err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to update host: "+err.Error())
		return
	}
	ib, _ := h.ibStore.GetInbound(host.InboundID)
	hosts, _ := h.ibStore.ListHostsByInbound(host.InboundID)
	h.renderPartial(w, "partial-hosts-modal", inboundHostsData{Inbound: ib, Hosts: hosts})
}

func (h *Handler) deleteHost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	host, err := h.ibStore.GetHost(id)
	if err != nil {
		htmxError(w, http.StatusNotFound, "host not found")
		return
	}
	ibID := host.InboundID
	ib, _ := h.ibStore.GetInbound(ibID)
	if err := h.ibStore.DeleteHost(id); err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to delete host: "+err.Error())
		return
	}
	hosts, _ := h.ibStore.ListHostsByInbound(ibID)
	h.renderPartial(w, "partial-host-rows", inboundHostsData{Inbound: ib, Hosts: hosts})
}

// ─── Outbound Handlers ────────────────────────────────────────────────────────

func (h *Handler) outboundsListPartial(w http.ResponseWriter, r *http.Request) {
	list, err := h.outboundStore.List()
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to get outbound list: "+err.Error())
		return
	}
	h.renderPartial(w, "partial-outbound-rows", list)
}

func (h *Handler) outboundNewForm(w http.ResponseWriter, r *http.Request) {
	h.renderPartial(w, "partial-outbound-new-form", nil)
}

func (h *Handler) createOutbound(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	server := r.FormValue("server")
	if name == "" || server == "" {
		htmxError(w, http.StatusBadRequest, "name and server are required")
		return
	}
	protocol := r.FormValue("protocol")
	if protocol == "" {
		protocol = "ss"
	}
	ob := outbounds.Outbound{
		ID:          idgen.NextString(),
		Name:        name,
		Protocol:    protocol,
		Server:      server,
		Username:    r.FormValue("username"),
		Password:    r.FormValue("password"),
		Method:      r.FormValue("method"),
		UUID:        r.FormValue("uuid"),
		SNI:         r.FormValue("sni"),
		PublicKey:   r.FormValue("public_key"),
		ShortID:     r.FormValue("short_id"),
		Fingerprint: r.FormValue("fingerprint"),
	}
	if _, err := h.outboundStore.Upsert(ob); err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to create outbound: "+err.Error())
		return
	}
	h.renderOutboundsListFromStore(w)
}

func (h *Handler) outboundEditForm(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ob, err := h.outboundStore.Get(id)
	if err != nil {
		htmxError(w, http.StatusNotFound, "outbound not found")
		return
	}
	h.renderPartial(w, "partial-outbound-edit-form", ob)
}

func (h *Handler) updateOutbound(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ob, err := h.outboundStore.Get(id)
	if err != nil {
		htmxError(w, http.StatusNotFound, "outbound not found")
		return
	}
	if name := r.FormValue("name"); name != "" {
		ob.Name = name
	}
	if server := r.FormValue("server"); server != "" {
		ob.Server = server
	}
	if protocol := r.FormValue("protocol"); protocol != "" {
		ob.Protocol = protocol
	}
	ob.Username = r.FormValue("username")
	ob.Password = r.FormValue("password")
	ob.Method = r.FormValue("method")
	ob.UUID = r.FormValue("uuid")
	ob.SNI = r.FormValue("sni")
	ob.PublicKey = r.FormValue("public_key")
	ob.ShortID = r.FormValue("short_id")
	ob.Fingerprint = r.FormValue("fingerprint")
	if _, err := h.outboundStore.Upsert(ob); err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to update outbound: "+err.Error())
		return
	}
	h.renderOutboundsListFromStore(w)
}

func (h *Handler) deleteOutbound(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.outboundStore.Delete(id); err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to delete outbound: "+err.Error())
		return
	}
	h.renderOutboundsListFromStore(w)
}

func (h *Handler) outboundImportForm(w http.ResponseWriter, r *http.Request) {
	h.renderPartial(w, "partial-outbound-import-form", nil)
}

func (h *Handler) importOutbound(w http.ResponseWriter, r *http.Request) {
	rawURL := strings.TrimSpace(r.FormValue("proxy_url"))
	var (
		ob  outbounds.Outbound
		err error
	)
	switch {
	case strings.HasPrefix(rawURL, "ss://"):
		ob, err = parseShadowsocksURL(rawURL)
	case strings.HasPrefix(rawURL, "vless://"):
		ob, err = parseVlessURL(rawURL)
	default:
		htmxError(w, http.StatusBadRequest, "不支持的链接格式，仅支持 ss:// 和 vless://")
		return
	}
	if err != nil {
		htmxError(w, http.StatusBadRequest, "链接解析失败: "+err.Error())
		return
	}
	ob.ID = idgen.NextString()
	if _, err := h.outboundStore.Upsert(ob); err != nil {
		htmxError(w, http.StatusInternalServerError, "保存失败: "+err.Error())
		return
	}
	h.renderOutboundsListFromStore(w)
}

// parseShadowsocksURL 解析 ss:// 链接，支持 SIP002 和 legacy base64 格式。
// ss://BASE64(method:password)@host:port#name
// ss://method:password@host:port#name
func parseShadowsocksURL(raw string) (outbounds.Outbound, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return outbounds.Outbound{}, fmt.Errorf("URL 解析失败: %w", err)
	}

	name := u.Fragment
	if name == "" {
		name = u.Hostname()
	}

	host := u.Hostname()
	portStr := u.Port()
	if host == "" || portStr == "" {
		return outbounds.Outbound{}, fmt.Errorf("缺少 host 或 port")
	}
	server := net.JoinHostPort(host, portStr)

	var method, password string
	if u.User == nil {
		return outbounds.Outbound{}, fmt.Errorf("缺少认证信息")
	}
	userinfo := u.User.Username()
	// 尝试 base64 解码（standard 和 URL-safe 两种）
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.RawURLEncoding} {
		if decoded, decErr := enc.DecodeString(userinfo); decErr == nil && strings.Contains(string(decoded), ":") {
			userinfo = string(decoded)
			break
		}
	}
	if idx := strings.Index(userinfo, ":"); idx > 0 {
		method = userinfo[:idx]
		password = userinfo[idx+1:]
	} else if pw, ok := u.User.Password(); ok {
		method = userinfo
		password = pw
	} else {
		return outbounds.Outbound{}, fmt.Errorf("无法解析 method:password")
	}

	return outbounds.Outbound{
		Name:     name,
		Protocol: "ss",
		Server:   server,
		Method:   method,
		Password: password,
	}, nil
}

// parseVlessURL 解析 vless:// 链接（Reality 格式）。
// vless://uuid@host:port?security=reality&pbk=公钥&sid=shortid&sni=域名&fp=指纹#名称
func parseVlessURL(raw string) (outbounds.Outbound, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return outbounds.Outbound{}, fmt.Errorf("URL 解析失败: %w", err)
	}

	name := u.Fragment
	if name == "" {
		name = u.Hostname()
	}

	host := u.Hostname()
	portStr := u.Port()
	if host == "" || portStr == "" {
		return outbounds.Outbound{}, fmt.Errorf("缺少 host 或 port")
	}

	uuid := u.User.Username()
	if uuid == "" {
		return outbounds.Outbound{}, fmt.Errorf("缺少 UUID")
	}

	q := u.Query()
	return outbounds.Outbound{
		Name:        name,
		Protocol:    "vless",
		Server:      net.JoinHostPort(host, portStr),
		UUID:        uuid,
		SNI:         q.Get("sni"),
		PublicKey:   q.Get("pbk"),
		ShortID:     q.Get("sid"),
		Fingerprint: q.Get("fp"),
	}, nil
}

func (h *Handler) renderOutboundsListFromStore(w http.ResponseWriter) {
	list, err := h.outboundStore.List()
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to get outbound list: "+err.Error())
		return
	}
	h.renderPartial(w, "partial-outbound-rows", list)
}

// ─── 路由规则 ─────────────────────────────────────────────────────────────────

func (h *Handler) routeRulesPage(w http.ResponseWriter, r *http.Request) {
	h.renderPage(w, r, "routerules", pageData{
		Page:     "routerules",
		Username: h.currentUsername(r),
	})
}

func (h *Handler) routeRulesListPartial(w http.ResponseWriter, r *http.Request) {
	h.renderRouteRulesListFromStore(w)
}

// nodeSSInboundOption 表示一个可用作分流出口的节点 SS Inbound 选项。
type nodeSSInboundOption struct {
	// ID 用作 outbound_id 表单值，格式为 "nodeib:<inbound_id>"
	ID    string
	Label string // 显示标签，如 "香港节点 - SS:8388"
}

// routeRuleFormData 传给路由规则表单模板的数据。
type routeRuleFormData struct {
	Rule            *routerules.RouteRule
	Outbounds       []outbounds.Outbound
	Nodes           []nodes.Node
	NodeSSInbounds  []nodeSSInboundOption
}

// buildNodeSSInboundOptions 从 ibStore + nodeStore + userStore 中提取所有 shadowsocks inbound
// 的用户选项，每个 (inbound × 用户) 组合一条，ID 格式为 "nodeib:<ibID>:<userInboundID>"。
func (h *Handler) buildNodeSSInboundOptions() []nodeSSInboundOption {
	allIbs, _ := h.ibStore.ListInbounds()
	nodeList, _ := h.nodeStore.List()
	nodeMap := make(map[string]nodes.Node, len(nodeList))
	for _, n := range nodeList {
		nodeMap[n.ID] = n
	}
	allUsers, _ := h.userStore.ListUsers()
	userMap := make(map[string]string, len(allUsers)) // userID → username
	for _, u := range allUsers {
		userMap[u.ID] = u.Username
	}
	var opts []nodeSSInboundOption
	for _, ib := range allIbs {
		if ib.Protocol != "shadowsocks" {
			continue
		}
		nodeName := ib.NodeID
		if n, ok := nodeMap[ib.NodeID]; ok {
			nodeName = n.Name
		}
		accs, _ := h.userStore.ListUserInboundsByInbound(ib.ID)
		for _, acc := range accs {
			username := acc.UserID
			if name, ok := userMap[acc.UserID]; ok {
				username = name
			}
			opts = append(opts, nodeSSInboundOption{
				ID:    fmt.Sprintf("nodeib:%s:%s", ib.ID, acc.ID),
				Label: fmt.Sprintf("%s - SS:%d (%s)", nodeName, ib.Port, username),
			})
		}
	}
	return opts
}

func (h *Handler) routeRuleNewForm(w http.ResponseWriter, r *http.Request) {
	obList, _ := h.outboundStore.List()
	nodeList, _ := h.nodeStore.List()
	h.renderPartial(w, "partial-routerule-new-form", routeRuleFormData{
		Outbounds:      obList,
		Nodes:          nodeList,
		NodeSSInbounds: h.buildNodeSSInboundOptions(),
	})
}

func (h *Handler) createRouteRule(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		htmxError(w, http.StatusBadRequest, "invalid form data")
		return
	}
	name := r.FormValue("name")
	ruleType := r.FormValue("rule_type")
	patterns := r.FormValue("patterns")
	outboundID := r.FormValue("outbound_id")
	priorityStr := r.FormValue("priority")
	if name == "" || ruleType == "" || patterns == "" {
		htmxError(w, http.StatusBadRequest, "name, rule_type and patterns are required")
		return
	}
	priority := 100
	if v, err := strconv.Atoi(priorityStr); err == nil {
		priority = v
	}
	rule := routerules.RouteRule{
		ID:            idgen.NextString(),
		Name:          name,
		RuleType:      ruleType,
		Patterns:      patterns,
		OutboundID:    outboundID,
		Priority:      priority,
		RuleSetURL:    r.FormValue("rule_set_url"),
		RuleSetFormat: r.FormValue("rule_set_format"),
		NodeIDs:       strings.Join(r.Form["node_ids"], ","),
	}
	if _, err := h.routeRuleStore.Upsert(rule); err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to create route rule: "+err.Error())
		return
	}
	h.renderRouteRulesListFromStore(w)
}

func (h *Handler) routeRuleEditForm(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rule, err := h.routeRuleStore.Get(id)
	if err != nil {
		htmxError(w, http.StatusNotFound, "route rule not found")
		return
	}
	obList, _ := h.outboundStore.List()
	nodeList, _ := h.nodeStore.List()
	h.renderPartial(w, "partial-routerule-edit-form", routeRuleFormData{
		Rule:           &rule,
		Outbounds:      obList,
		Nodes:          nodeList,
		NodeSSInbounds: h.buildNodeSSInboundOptions(),
	})
}

func (h *Handler) updateRouteRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		htmxError(w, http.StatusBadRequest, "invalid form data")
		return
	}
	rule, err := h.routeRuleStore.Get(id)
	if err != nil {
		htmxError(w, http.StatusNotFound, "route rule not found")
		return
	}
	rule.Name = r.FormValue("name")
	rule.RuleType = r.FormValue("rule_type")
	rule.Patterns = r.FormValue("patterns")
	rule.OutboundID = r.FormValue("outbound_id")
	rule.RuleSetURL = r.FormValue("rule_set_url")
	rule.RuleSetFormat = r.FormValue("rule_set_format")
	rule.NodeIDs = strings.Join(r.Form["node_ids"], ",")
	if v, err := strconv.Atoi(r.FormValue("priority")); err == nil {
		rule.Priority = v
	}
	if _, err := h.routeRuleStore.Upsert(rule); err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to update route rule: "+err.Error())
		return
	}
	h.renderRouteRulesListFromStore(w)
}

func (h *Handler) deleteRouteRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.routeRuleStore.Delete(id); err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to delete route rule: "+err.Error())
		return
	}
	h.renderRouteRulesListFromStore(w)
}

func (h *Handler) renderRouteRulesListFromStore(w http.ResponseWriter) {
	list, err := h.routeRuleStore.List()
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to get route rule list: "+err.Error())
		return
	}
	obList, _ := h.outboundStore.List()
	obMap := make(map[string]outbounds.Outbound, len(obList))
	// 统一出口标签 map（自定义出口 + nodeib: 节点 SS 出口）
	outboundLabels := make(map[string]string)
	for _, ob := range obList {
		obMap[ob.ID] = ob
		outboundLabels[ob.ID] = ob.Name
	}
	nodeList, _ := h.nodeStore.List()
	nodeMap := make(map[string]nodes.Node, len(nodeList))
	for _, n := range nodeList {
		nodeMap[n.ID] = n
	}
	// 将节点 SS inbound 的每个用户选项加入标签 map
	if allIbs, err := h.ibStore.ListInbounds(); err == nil {
		allUsers, _ := h.userStore.ListUsers()
		userNameMap := make(map[string]string, len(allUsers))
		for _, u := range allUsers {
			userNameMap[u.ID] = u.Username
		}
		for _, ib := range allIbs {
			if ib.Protocol != "shadowsocks" {
				continue
			}
			nodeName := ib.NodeID
			if n, ok := nodeMap[ib.NodeID]; ok {
				nodeName = n.Name
			}
			accs, _ := h.userStore.ListUserInboundsByInbound(ib.ID)
			for _, acc := range accs {
				username := acc.UserID
				if name, ok := userNameMap[acc.UserID]; ok {
					username = name
				}
				key := fmt.Sprintf("nodeib:%s:%s", ib.ID, acc.ID)
				outboundLabels[key] = fmt.Sprintf("%s - SS:%d (%s)", nodeName, ib.Port, username)
			}
		}
	}
	h.renderPartial(w, "partial-routerule-rows", struct {
		Rules          []routerules.RouteRule
		OutboundMap    map[string]outbounds.Outbound
		OutboundLabels map[string]string
		NodeMap        map[string]nodes.Node
	}{Rules: list, OutboundMap: obMap, OutboundLabels: outboundLabels, NodeMap: nodeMap})
}


// ─── 模板函数 ─────────────────────────────────────────────────────────────────

// templateFuncs 返回模板函数映射。
func templateFuncs() template.FuncMap {
	return template.FuncMap{
		// containsID 检查逗号分隔的 ID 列表中是否包含指定 ID。
		"containsID": func(ids, id string) bool {
			if ids == "" {
				return false
			}
			for _, s := range strings.Split(ids, ",") {
				if strings.TrimSpace(s) == id {
					return true
				}
			}
			return false
		},
		// formatBytes 将字节数格式化为可读字符串（如 "1.23 GB"）。
		"formatBytes": func(n int64) string {
			const (
				kb = 1024
				mb = kb * 1024
				gb = mb * 1024
				tb = gb * 1024
			)
			switch {
			case n >= tb:
				return fmt.Sprintf("%.2f TB", float64(n)/float64(tb))
			case n >= gb:
				return fmt.Sprintf("%.2f GB", float64(n)/float64(gb))
			case n >= mb:
				return fmt.Sprintf("%.2f MB", float64(n)/float64(mb))
			case n >= kb:
				return fmt.Sprintf("%.2f KB", float64(n)/float64(kb))
			default:
				return fmt.Sprintf("%d B", n)
			}
		},
		// formatSpeed 将 bytes/s 格式化为可读速率字符串（如 "1.23 MB/s"）。
		"formatSpeed": func(n int64) string {
			const (
				kb = int64(1024)
				mb = kb * 1024
				gb = mb * 1024
			)
			switch {
			case n >= gb:
				return fmt.Sprintf("%.2f GB/s", float64(n)/float64(gb))
			case n >= mb:
				return fmt.Sprintf("%.1f MB/s", float64(n)/float64(mb))
			case n >= kb:
				return fmt.Sprintf("%.0f KB/s", float64(n)/float64(kb))
			default:
				return fmt.Sprintf("%d B/s", n)
			}
		},
		// formatGB 将字节数转换为 GB 数值字符串（两位小数）。
		"formatGB": func(n int64) string {
			return fmt.Sprintf("%.2f", float64(n)/float64(1024*1024*1024))
		},
		// trafficPct 计算流量使用百分比，limit=0 时返回 0。
		"trafficPct": func(used, limit int64) int {
			if limit == 0 {
				return 0
			}
			pct := int(float64(used) / float64(limit) * 100)
			if pct > 100 {
				return 100
			}
			return pct
		},
		// dict 构造 map[string]any，供模板中传参给子模板使用（如 {{template "foo" (dict "Data" .)}}）。
		"dict": func(args ...any) map[string]any {
			m := make(map[string]any, len(args)/2)
			for i := 0; i+1 < len(args); i += 2 {
				if key, ok := args[i].(string); ok {
					m[key] = args[i+1]
				}
			}
			return m
		},
		// formatCheckTime 格式化检测时间，零值返回空字符串。
		"formatCheckTime": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return t.Local().Format("01-02 15:04")
		},
		// formatExpire 格式化过期时间，nil 或零值返回 "永不"。
		"formatExpire": func(t *time.Time) string {
			if t == nil || t.IsZero() {
				return "never"
			}
			return t.Format("2006-01-02")
		},
		// formatOnlineAt 格式化最后在线时间，nil 表示从未在线。
		"formatOnlineAt": func(t *time.Time) string {
			if t == nil || t.IsZero() {
				return ""
			}
			d := time.Since(*t)
			switch {
			case d < time.Minute:
				return "just now"
			case d < time.Hour:
				return fmt.Sprintf("%d min ago", int(d.Minutes()))
			case d < 24*time.Hour:
				return fmt.Sprintf("%d hr ago", int(d.Hours()))
			default:
				return t.Format("01-02 15:04")
			}
		},
		// statusClass 根据用户状态返回对应 Tailwind CSS class。
		"statusClass": func(s string) string {
			switch s {
			case users.StatusActive:
				return "bg-emerald-500/15 text-emerald-400"
			case users.StatusDisabled:
				return "bg-gray-500/15 text-gray-400"
			case users.StatusLimited:
				return "bg-yellow-500/15 text-yellow-400"
			case users.StatusExpired:
				return "bg-red-500/15 text-red-400"
			case users.StatusOnHold:
				return "bg-sky-500/15 text-sky-400"
			default:
				return "bg-gray-500/15 text-gray-400"
			}
		},
		// statusLabel 根据用户状态返回显示标签。
		"statusLabel": func(s string) string {
			switch s {
			case users.StatusActive:
				return "active"
			case users.StatusDisabled:
				return "disabled"
			case users.StatusLimited:
				return "throttled"
			case users.StatusExpired:
				return "expired"
			case users.StatusOnHold:
				return "on hold"
			default:
				return s
			}
		},
		// syncStale 判断最后同步时间是否超过 2 分钟（节点可能离线）。
		"syncStale": func(t time.Time) bool {
			return !t.IsZero() && time.Since(t) > 2*time.Minute
		},
		// formatSyncAge 格式化最后同步时间为相对描述。
		"formatSyncAge": func(t time.Time) string {
			if t.IsZero() {
				return "从未"
			}
			d := time.Since(t)
			if d < time.Minute {
				return fmt.Sprintf("%ds 前", int(d.Seconds()))
			}
			return fmt.Sprintf("%dm 前", int(d.Minutes()))
		},
		// formatDateTime 格式化时间为 "2006-01-02 15:04:05"。
		"formatDateTime": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return t.Format("2006-01-02 15:04:05")
		},
		// truncateUA 将 User-Agent 截断到指定长度，超出部分用 "…" 替换。
		"truncateUA": func(ua string, n int) string {
			if len(ua) <= n {
				return ua
			}
			return ua[:n] + "…"
		},
		// sub 整数减法。
		"sub": func(a, b int) int {
			return a - b
		},
		// gt0 判断 int64 是否大于 0。
		"gt0": func(n int64) bool {
			return n > 0
		},
		// addInt64 两个 int64 相加。
		"addInt64": func(a, b int64) int64 {
			return a + b
		},
		// domainListDisplay 将逗号分隔的域名字符串转为每行一个的显示格式。
		"domainListDisplay": func(s string) string {
			var parts []string
			for _, d := range strings.Split(s, ",") {
				if d = strings.TrimSpace(d); d != "" {
					parts = append(parts, d)
				}
			}
			return strings.Join(parts, "\n")
		},
	}
}

func panelRandomUUID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("pulse-%d", time.Now().UnixNano())
	}
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16])
}

// ─── Caddy 管理 ──────────────────────────────────────────────────────────────

type nodeCaddyStatus struct {
	Node  nodes.Node
	Caddy nodes.CaddyStatusResponse
	Error string
}

func (h *Handler) caddyPage(w http.ResponseWriter, r *http.Request) {
	h.renderPage(w, r, "caddy", pageData{
		Page:     "caddy",
		Username: h.currentUsername(r),
	})
}

func (h *Handler) caddyListPartial(w http.ResponseWriter, r *http.Request) {
	nodeList, err := h.nodeStore.List()
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to get node list: "+err.Error())
		return
	}
	result := make([]nodeCaddyStatus, 0, len(nodeList))
	for _, n := range nodeList {
		item := nodeCaddyStatus{Node: n}
		client, dialErr := h.dial(n.ID)
		if dialErr != nil {
			item.Error = dialErr.Error()
			result = append(result, item)
			continue
		}
		status, statusErr := client.CaddyStatus(r.Context())
		if statusErr != nil {
			item.Error = statusErr.Error()
		} else {
			item.Caddy = status
		}
		result = append(result, item)
	}
	h.renderPartial(w, "partial-caddy-rows", result)
}

func (h *Handler) caddySyncNode(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("nodeID")

	if _, err := h.nodeStore.Get(nodeID); err != nil {
		htmxError(w, http.StatusNotFound, "节点不存在")
		return
	}
	client, err := h.dial(nodeID)
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to connect to node: "+err.Error())
		return
	}

	nodeInbounds, err := h.ibStore.ListInboundsByNode(nodeID)
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to get inbounds: "+err.Error())
		return
	}

	seen := make(map[string]struct{})
	var routes []nodes.TrojanRoute
	for _, ib := range nodeInbounds {
		if ib.Protocol != "trojan" {
			continue
		}
		hosts, hErr := h.ibStore.ListHostsByInbound(ib.ID)
		if hErr != nil {
			continue
		}
		for _, host := range hosts {
			if host.Address != "" {
				if _, ok := seen[host.Address]; !ok {
					seen[host.Address] = struct{}{}
					routes = append(routes, nodes.TrojanRoute{Domain: host.Address, Port: ib.Port})
				}
			}
		}
	}

	if err := client.SyncCaddyRoutes(r.Context(), routes); err != nil {
		htmxError(w, http.StatusInternalServerError, "sync failed: "+err.Error())
		return
	}

	setHXTriggerToast(w, "Caddy 路由同步成功")
	h.caddyListPartial(w, r)
}

func (h *Handler) caddyfileModal(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("nodeID")
	node, err := h.nodeStore.Get(nodeID)
	if err != nil {
		htmxError(w, http.StatusNotFound, "节点不存在")
		return
	}
	client, err := h.dial(nodeID)
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "连接节点失败: "+err.Error())
		return
	}
	status, err := client.CaddyStatus(r.Context())
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "获取 Caddy 状态失败: "+err.Error())
		return
	}
	h.renderPartial(w, "partial-caddy-caddyfile", struct {
		NodeName string
		Content  string
	}{NodeName: node.Name, Content: status.Caddyfile})
}

func (h *Handler) caddyConfigForm(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("nodeID")
	node, err := h.nodeStore.Get(nodeID)
	if err != nil {
		htmxError(w, http.StatusNotFound, "节点不存在")
		return
	}
	h.renderPartial(w, "partial-caddy-config-form", node)
}

func (h *Handler) caddySaveConfig(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("nodeID")
	acmeEmail := strings.TrimSpace(r.FormValue("acme_email"))
	// 面板域名：将换行/逗号分隔的输入标准化为逗号分隔存储
	var domainList []string
	for _, d := range strings.FieldsFunc(r.FormValue("panel_domain"), func(r rune) bool { return r == ',' || r == '\n' }) {
		if d = strings.TrimSpace(d); d != "" {
			domainList = append(domainList, d)
		}
	}
	panelDomain := strings.Join(domainList, ",")
	// 额外反代：规范化换行，去除首尾空白行
	extraProxies := normalizeExtraProxies(r.FormValue("extra_proxies"))
	if err := h.nodeStore.UpdateCaddyConfig(nodeID, acmeEmail, panelDomain, extraProxies, true); err != nil {
		htmxError(w, http.StatusInternalServerError, "保存配置失败: "+err.Error())
		return
	}

	client, err := h.dial(nodeID)
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "连接节点失败: "+err.Error())
		return
	}

	if err := client.UpdateCaddyConfig(r.Context(), nodes.CaddyConfig{
		ACMEEmail:    acmeEmail,
		PanelDomain:  panelDomain,
		PanelPort:    h.panelPort(),
		ExtraProxies: extraProxies,
	}); err != nil {
		htmxError(w, http.StatusInternalServerError, "推送配置失败: "+err.Error())
		return
	}

	// 顺带触发一次 Trojan 路由同步
	nodeInbounds, ibErr := h.ibStore.ListInboundsByNode(nodeID)
	if ibErr == nil {
		seen := make(map[string]struct{})
		var routes []nodes.TrojanRoute
		for _, ib := range nodeInbounds {
			if ib.Protocol != "trojan" {
				continue
			}
			hosts, hErr := h.ibStore.ListHostsByInbound(ib.ID)
			if hErr != nil {
				continue
			}
			for _, host := range hosts {
				if host.Address != "" {
					if _, ok := seen[host.Address]; !ok {
						seen[host.Address] = struct{}{}
						routes = append(routes, nodes.TrojanRoute{Domain: host.Address, Port: ib.Port})
					}
				}
			}
		}
		if syncErr := client.SyncCaddyRoutes(r.Context(), routes); syncErr != nil {
			log.Printf("warn: caddy sync after save config: %v", syncErr)
		}
	}

	setHXTriggerToast(w, "Caddy 配置已保存")
	h.caddyListPartial(w, r)
}

// normalizeExtraProxies 规范化额外反代输入：去除空行和首尾空白，返回换行分隔的字符串。
func normalizeExtraProxies(raw string) string {
	var lines []string
	for _, line := range strings.Split(raw, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func panelRandomToken(size int) string {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("pulse-secret-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", buf)
}

// ─── 用户自助门户 ─────────────────────────────────────────────────────────────

// userNodeInfo 用户主页展示的节点信息。
type userNodeInfo struct {
	Name          string
	Protocols     []string // e.g. ["VLESS", "Trojan", "Shadowsocks"]
	Status        string   // "online" / "idle" / "offline"
	PingMs        int64    // 管理后台专用，用户主页不使用
	OnlineUsers   int      // 当前在线用户数
	Connections   int      // 当前活跃连接数
	UploadSpeed   int64    // bytes/s
	DownloadSpeed int64    // bytes/s
}

// userPortalData 传入用户主页模板的数据。
type userPortalData struct {
	User                users.User
	SubURL              string
	Nodes               []userNodeInfo
	AnnouncementTitle   string
	AnnouncementContent string
	HasAnnouncement     bool
	DailyTraffic        []users.UserDailyUsage // 近7天每日流量
	NodeUsage           []userNodeUsageRow      // 各节点流量分布
	// Billing
	PlanName    string // empty if no plan
	ShopEnabled bool
}

// subURL 根据请求构造完整的订阅链接。
func subURL(r *http.Request, token string) string {
	scheme := "https"
	if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") != "https" {
		scheme = "http"
	}
	host := r.Host
	if fwdHost := r.Header.Get("X-Forwarded-Host"); fwdHost != "" {
		host = fwdHost
	}
	return scheme + "://" + host + "/sub/" + token
}

// userPortalPage 渲染用户主页（无需管理员认证，以 sub_token 鉴权）。
func (h *Handler) userPortalPage(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("sub_token")
	user, err := h.userStore.GetUserBySubToken(token)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// 从指标缓存直接读取节点运行状态，避免用户主页首次渲染时出现占位符闪动。
	nodeIDs := h.getUserNodeIDs(user.ID)
	h.metricsCache.mu.RLock()
	allMetrics := h.metricsCache.data
	h.metricsCache.mu.RUnlock()

	var filteredMetrics []nodeMetrics
	for _, m := range allMetrics {
		if _, ok := nodeIDs[m.Node.ID]; ok {
			filteredMetrics = append(filteredMetrics, m)
		}
	}

	var nodeInfos []userNodeInfo
	if len(filteredMetrics) > 0 {
		// 缓存有数据：携带完整运行状态
		nodeInfos = metricsToUserNodeInfos(filteredMetrics)
	} else {
		// 缓存为空（服务器刚启动）：回退到节点基本信息，状态设为 offline
		accesses, _ := h.userStore.ListUserInboundsByUser(user.ID)
		for _, acc := range accesses {
			node, err := h.nodeStore.Get(acc.NodeID)
			if err != nil {
				continue
			}
			nodeInbounds, err := h.ibStore.ListInboundsByNode(acc.NodeID)
			if err != nil {
				continue
			}
			var protocols []string
			seen := make(map[string]bool)
			for _, ib := range nodeInbounds {
				label := protocolLabel(ib.Protocol)
				if !seen[label] {
					seen[label] = true
					protocols = append(protocols, label)
				}
			}
			nodeInfos = append(nodeInfos, userNodeInfo{Name: node.Name, Protocols: protocols, Status: "offline"})
		}
	}

	portalData := userPortalData{User: user, SubURL: subURL(r, user.SubToken), Nodes: nodeInfos}

	// 近7天每日流量
	if daily, err := h.userStore.ListUserDailyUsage(user.ID, 7); err == nil {
		portalData.DailyTraffic = daily
	}

	// 各节点流量分布
	if rawUsage, err := h.userStore.ListUserNodeUsage(user.ID); err == nil {
		nodeList, _ := h.nodeStore.List()
		nodeNames := make(map[string]string, len(nodeList))
		for _, n := range nodeList {
			nodeNames[n.ID] = n.Name
		}
		var totalBytes int64
		for _, u := range rawUsage {
			totalBytes += u.UploadBytes + u.DownloadBytes
		}
		for _, u := range rawUsage {
			t := u.UploadBytes + u.DownloadBytes
			if t == 0 {
				continue
			}
			pct := 0
			if totalBytes > 0 {
				pct = int(float64(t) / float64(totalBytes) * 100)
			}
			name := nodeNames[u.NodeID]
			if name == "" {
				name = u.NodeID
			}
			portalData.NodeUsage = append(portalData.NodeUsage, userNodeUsageRow{
				NodeName:      name,
				UploadBytes:   u.UploadBytes,
				DownloadBytes: u.DownloadBytes,
				TotalBytes:    t,
				Pct:           pct,
			})
		}
	}

	if h.settingsStore != nil {
		enabled, _ := h.settingsStore.GetSetting("announcement_enabled")
		if enabled == "true" {
			title, _ := h.settingsStore.GetSetting("announcement_title")
			content, _ := h.settingsStore.GetSetting("announcement_content")
			if title != "" || content != "" {
				portalData.HasAnnouncement = true
				portalData.AnnouncementTitle = title
				portalData.AnnouncementContent = content
			}
		}
	}

	// Billing info
	portalData.ShopEnabled = h.shopEnabled
	if h.shopEnabled && h.planStore != nil && user.CurrentPlanID != "" {
		if plan, err := h.planStore.GetPlan(user.CurrentPlanID); err == nil {
			portalData.PlanName = plan.Name
		}
	}

	w.Header().Set("X-Robots-Tag", "noindex")
	h.renderPage(w, r, "user_portal", pageData{
		Page: "user_portal",
		Data: portalData,
	})
}

// protocolLabel 将协议内部名转为展示标签。
func protocolLabel(p string) string {
	switch p {
	case "vless":
		return "VLESS"
	case "vmess":
		return "VMess"
	case "trojan":
		return "Trojan"
	case "shadowsocks":
		return "Shadowsocks"
	default:
		return p
	}
}

// apiMe 返回用户自身信息（JSON），以 ?token= 参数鉴权。
func (h *Handler) apiMe(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	user, err := h.userStore.GetUserBySubToken(token)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"user not found"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"username":            user.Username,
		"status":              user.EffectiveStatus(),
		"upload_bytes":        user.UploadBytes,
		"download_bytes":      user.DownloadBytes,
		"used_bytes":          user.UsedBytes,
		"traffic_limit_bytes": user.TrafficLimit,
		"expire_at":           user.ExpireAt,
		"sub_url":             subURL(r, user.SubToken),
	})
}

// apiResetToken 重新生成用户订阅 token 及所有 inbound 凭据，以 ?token= 参数鉴权。
func (h *Handler) apiResetToken(w http.ResponseWriter, r *http.Request) {
	jsonErr := func(status int, msg string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = fmt.Fprintf(w, `{"error":%q}`, msg)
	}

	token := r.URL.Query().Get("token")
	user, err := h.userStore.GetUserBySubToken(token)
	if err != nil {
		jsonErr(http.StatusNotFound, "user not found")
		return
	}

	// 重置订阅 token
	user.SubToken = panelRandomToken(16)
	if _, err := h.userStore.UpsertUser(user); err != nil {
		jsonErr(http.StatusInternalServerError, "failed to reset token")
		return
	}

	// 重置该用户所有 inbound 的代理凭据，并收集受影响的节点
	accesses, err := h.userStore.ListUserInboundsByUser(user.ID)
	if err != nil {
		jsonErr(http.StatusInternalServerError, "failed to list user inbounds")
		return
	}
	affectedNodeIDs := make(map[string]struct{})
	for _, acc := range accesses {
		ib, err := h.ibStore.GetInbound(acc.InboundID)
		if err != nil {
			// inbound 已删除则跳过
			continue
		}
		secret := panelRandomToken(12)
		if ib.Protocol == "shadowsocks" && strings.HasPrefix(ib.Method, "2022-") {
			secret = generateSSPassword(ib.Method)
		}
		acc.UUID = panelRandomUUID()
		acc.Secret = secret
		if _, err := h.userStore.UpsertUserInbound(acc); err != nil {
			jsonErr(http.StatusInternalServerError, "failed to reset inbound credentials")
			return
		}
		affectedNodeIDs[acc.NodeID] = struct{}{}
	}
	affected := make([]string, 0, len(affectedNodeIDs))
	for id := range affectedNodeIDs {
		affected = append(affected, id)
	}
	h.ApplyNodes(affected)

	// 若是 HTMX 请求，重定向到新门户页面
	if isHTMX(r) {
		w.Header().Set("HX-Redirect", "/user/"+user.SubToken)
		w.WriteHeader(http.StatusOK)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"token":   user.SubToken,
		"sub_url": subURL(r, user.SubToken),
	})
}
