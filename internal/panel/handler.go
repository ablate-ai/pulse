package panel

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
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
	"strconv"
	"strings"
	"time"

	"pulse/internal/auth"
	"pulse/internal/buildinfo"
	"pulse/internal/idgen"
	"pulse/internal/inbounds"
	"pulse/internal/jobs"
	"pulse/internal/nodes"
	"pulse/internal/outbounds"
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

// Handler 面板 HTTP 处理器，持有所有依赖。
type Handler struct {
	auth           *auth.Manager
	userStore      users.Store
	nodeStore      nodes.Store
	ibStore        inbounds.InboundStore
	outboundStore  outbounds.Store
	dial           jobs.NodeDialer
	applyOpts      jobs.ApplyOptions
	tmpl           *template.Template
	serverAddr     string
	clientCertFile string // 面板客户端证书路径，用于 node 安装时粘贴
	settingsStore  SettingsStore
}

// pageData 传入完整页面模板的数据结构。
type pageData struct {
	Page     string // "dashboard", "users", "nodes"
	Username string
	Version  string
	Data     any
}

// nodeWithStatus 节点及其运行状态。
type nodeWithStatus struct {
	Node        nodes.Node
	Status      string // "online" / "offline" / "idle"
	SingboxVer  string // sing-box 版本
	NodeVer     string // pulse-node 编译版本
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
	dial jobs.NodeDialer,
	applyOpts jobs.ApplyOptions,
	serverAddr string,
	clientCertFile string,
	settingsStore SettingsStore,
) (*Handler, error) {
	h := &Handler{
		auth:           authMgr,
		userStore:      userStore,
		nodeStore:      nodeStore,
		ibStore:        ibStore,
		outboundStore:  outboundStore,
		dial:           dial,
		applyOpts:      applyOpts,
		serverAddr:     serverAddr,
		clientCertFile: clientCertFile,
		settingsStore:  settingsStore,
	}

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
	mux.HandleFunc("GET /login", h.loginPage)
	mux.HandleFunc("POST /login", h.processLogin)
	mux.HandleFunc("POST /logout", h.processLogout)

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
	mux.HandleFunc("POST /panel/users/batch", h.requireAuth(h.batchUsers))
	mux.HandleFunc("POST /panel/settings/password", h.requireAuth(h.changePassword))
	mux.HandleFunc("POST /panel/settings/announcement", h.requireAuth(h.saveAnnouncement))

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
	mux.HandleFunc("POST /panel/outbounds", h.requireAuth(h.createOutbound))
	mux.HandleFunc("GET /panel/outbounds/{id}/edit", h.requireAuth(h.outboundEditForm))
	mux.HandleFunc("PUT /panel/outbounds/{id}", h.requireAuth(h.updateOutbound))
	mux.HandleFunc("DELETE /panel/outbounds/{id}", h.requireAuth(h.deleteOutbound))

	mux.HandleFunc("GET /panel/tools/reality-keypair", h.requireAuth(h.realityKeypair))

	mux.HandleFunc("GET /panel/inbounds/{id}/hosts", h.requireAuth(h.hostsModal))
	mux.HandleFunc("POST /panel/inbounds/{id}/hosts", h.requireAuth(h.createHost))
	mux.HandleFunc("GET /panel/hosts/{id}/edit", h.requireAuth(h.hostEditForm))
	mux.HandleFunc("PUT /panel/hosts/{id}", h.requireAuth(h.updateHost))
	mux.HandleFunc("DELETE /panel/hosts/{id}", h.requireAuth(h.deleteHost))

	mux.HandleFunc("GET /panel/nodes/list", h.requireAuth(h.nodesListPartial))
	mux.HandleFunc("GET /panel/nodes/new", h.requireAuth(h.nodeNewForm))
	mux.HandleFunc("POST /panel/nodes", h.requireAuth(h.createNode))
	mux.HandleFunc("GET /panel/nodes/{id}/edit", h.requireAuth(h.nodeEditForm))
	mux.HandleFunc("PUT /panel/nodes/{id}", h.requireAuth(h.updateNode))
	mux.HandleFunc("DELETE /panel/nodes/{id}", h.requireAuth(h.deleteNode))
	mux.HandleFunc("POST /panel/nodes/{id}/restart", h.requireAuth(h.restartNode))
	mux.HandleFunc("POST /panel/nodes/{id}/start", h.requireAuth(h.startNode))
	mux.HandleFunc("POST /panel/nodes/{id}/stop", h.requireAuth(h.stopNode))
	mux.HandleFunc("GET /panel/nodes/{id}/config", h.requireAuth(h.nodeConfigModal))
	mux.HandleFunc("GET /panel/nodes/{id}/logs", h.requireAuth(h.nodeLogsModal))
	mux.HandleFunc("GET /panel/nodes/{id}/logs/stream", h.requireAuth(h.nodeLogsStream))

	mux.HandleFunc("GET /caddy", h.requireAuth(h.caddyPage))
	mux.HandleFunc("GET /panel/caddy/list", h.requireAuth(h.caddyListPartial))
	mux.HandleFunc("POST /panel/caddy/{nodeID}/sync", h.requireAuth(h.caddySyncNode))
	mux.HandleFunc("GET /panel/caddy/{nodeID}/config-form", h.requireAuth(h.caddyConfigForm))
	mux.HandleFunc("POST /panel/caddy/{nodeID}/config", h.requireAuth(h.caddySaveConfig))
	mux.HandleFunc("GET /panel/caddy/{nodeID}/caddyfile", h.requireAuth(h.caddyfileModal))
}

// ─── 认证中间件 ──────────────────────────────────────────────────────────────

// requireAuth 封装需要认证的 handler。
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
func (h *Handler) renderPage(w http.ResponseWriter, name string, data pageData) {
	data.Version = buildinfo.Version
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

func (h *Handler) loginPage(w http.ResponseWriter, r *http.Request) {
	h.renderPage(w, "login", pageData{Page: "login"})
}

func (h *Handler) processLogin(w http.ResponseWriter, r *http.Request) {
	username := r.FormValue("username")
	password := r.FormValue("password")

	token, err := h.auth.Login(username, password)
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
	h.renderPage(w, "dashboard", pageData{
		Page:     "dashboard",
		Username: h.currentUsername(r),
	})
}

func (h *Handler) usersPage(w http.ResponseWriter, r *http.Request) {
	h.renderPage(w, "users", pageData{
		Page:     "users",
		Username: h.currentUsername(r),
	})
}

func (h *Handler) nodesPage(w http.ResponseWriter, r *http.Request) {
	h.renderPage(w, "nodes", pageData{
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

	h.renderPartial(w, "partial-user-rows", filtered)
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
		t, err := time.ParseInLocation("2006-01-02T15:04", expireAtStr, time.Local)
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
		affected, err := h.syncUserInbounds(newUser.ID, selectedIDs)
		if err != nil {
			htmxError(w, http.StatusInternalServerError, "failed to sync inbounds: "+err.Error())
			return
		}
		h.applyNodes(affected)
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
		t, err := time.ParseInLocation("2006-01-02T15:04", expireAtStr, time.Local)
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
		t, err := time.ParseInLocation("2006-01-02T15:04", onHoldExpireStr, time.Local)
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

	if _, err := h.userStore.UpsertUser(user); err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to update user: "+err.Error())
		return
	}

	// 有提交 inbound_sync 标记时（无论是否选中），都同步 inbound 关联
	if r.Form.Has("inbound_sync") {
		affected, err := h.syncUserInbounds(user.ID, r.Form["inbound_ids"])
		if err != nil {
			htmxError(w, http.StatusInternalServerError, "failed to sync inbounds: "+err.Error())
			return
		}
		h.applyNodes(affected)
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

	now := time.Now()
	user.UploadBytes = 0
	user.DownloadBytes = 0
	user.UsedBytes = 0
	user.LastTrafficResetAt = &now

	if _, err := h.userStore.UpsertUser(user); err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to reset traffic: "+err.Error())
		return
	}

	// 清空该用户所有凭据的流量同步游标，否则下次 SyncUsage 会把旧增量重新计入
	accesses, err := h.userStore.ListUserInboundsByUser(id)
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to list user inbounds: "+err.Error())
		return
	}
	for _, acc := range accesses {
		acc.SyncedUploadBytes = 0
		acc.SyncedDownloadBytes = 0
		if _, err := h.userStore.UpsertUserInbound(acc); err != nil {
			htmxError(w, http.StatusInternalServerError, "failed to reset cursor: "+err.Error())
			return
		}
	}

	h.renderUsersListFromStore(w)
}

// batchUsers 批量操作用户（enable/disable/delete）。
func (h *Handler) batchUsers(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		htmxError(w, http.StatusBadRequest, "failed to parse form")
		return
	}
	action := r.FormValue("action")
	ids := r.Form["ids"]
	if len(ids) == 0 {
		htmxError(w, http.StatusBadRequest, "no users selected")
		return
	}
	for _, id := range ids {
		switch action {
		case "delete":
			_ = h.userStore.DeleteUser(id)
		case "enable":
			if u, err := h.userStore.GetUser(id); err == nil {
				u.Status = users.StatusActive
				_, _ = h.userStore.UpsertUser(u)
			}
		case "disable":
			if u, err := h.userStore.GetUser(id); err == nil {
				u.Status = users.StatusDisabled
				_, _ = h.userStore.UpsertUser(u)
			}
		default:
			htmxError(w, http.StatusBadRequest, "unknown action: "+action)
			return
		}
	}
	h.renderUsersListFromStore(w)
}

type settingsData struct {
	ClientCert           string // 面板客户端证书 PEM，用于 node 安装时粘贴
	AnnouncementTitle   string
	AnnouncementContent string
	AnnouncementEnabled bool
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
	}
	h.renderPage(w, "settings", pageData{
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

// changePassword 修改管理员密码。
func (h *Handler) changePassword(w http.ResponseWriter, r *http.Request) {
	current := r.FormValue("current_password")
	newPw := r.FormValue("new_password")
	confirm := r.FormValue("confirm_password")
	if newPw != confirm {
		htmxError(w, http.StatusBadRequest, "passwords do not match")
		return
	}
	if err := h.auth.ChangePassword(current, newPw); err != nil {
		htmxError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<div class="text-sm text-emerald-400 py-2">密码已更新并持久化</div>`)
}

// renderUsersListFromStore 从 store 拉取最新用户列表并渲染 partial。
func (h *Handler) renderUsersListFromStore(w http.ResponseWriter) {
	allUsers, err := h.userStore.ListUsers()
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to get user list: "+err.Error())
		return
	}
	h.renderPartial(w, "partial-user-rows", allUsers)
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

func (h *Handler) nodesListPartial(w http.ResponseWriter, r *http.Request) {
	nodeList, err := h.nodeStore.List()
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to get node list: "+err.Error())
		return
	}
	result := make([]nodeWithStatus, 0, len(nodeList))
	for _, n := range nodeList {
		result = append(result, h.fetchNodeStatus(r.Context(), n))
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

	newNode := nodes.Node{
		ID:      idgen.NextString(),
		Name:    name,
		BaseURL: baseURL,
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
		NodeName: node.Name,
		Config:   config,
	})
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
	result := make([]nodeWithStatus, 0, len(nodeList))
	for _, n := range nodeList {
		result = append(result, h.fetchNodeStatus(r.Context(), n))
	}
	h.renderPartial(w, "partial-node-rows", result)
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
func (h *Handler) applyNodes(nodeIDs []string) {
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
func (h *Handler) syncUserInbounds(userID string, selectedInboundIDs []string) ([]string, error) {
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
	h.renderPage(w, "inbounds", pageData{
		Page:     "inbounds",
		Username: h.currentUsername(r),
	})
}

func (h *Handler) outboundsPage(w http.ResponseWriter, r *http.Request) {
	h.renderPage(w, "outbounds", pageData{
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
	h.renderInboundsListFromStore(w)
}

func (h *Handler) deleteInbound(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.ibStore.DeleteInbound(id); err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to delete inbound: "+err.Error())
		return
	}
	h.renderInboundsListFromStore(w)
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

func (h *Handler) renderOutboundsListFromStore(w http.ResponseWriter) {
	list, err := h.outboundStore.List()
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "failed to get outbound list: "+err.Error())
		return
	}
	h.renderPartial(w, "partial-outbound-rows", list)
}

// ─── 模板函数 ─────────────────────────────────────────────────────────────────

// templateFuncs 返回模板函数映射。
func templateFuncs() template.FuncMap {
	return template.FuncMap{
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
	h.renderPage(w, "caddy", pageData{
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
	panelDomain := strings.TrimSpace(r.FormValue("panel_domain"))
	if err := h.nodeStore.UpdateCaddyConfig(nodeID, acmeEmail, panelDomain, true); err != nil {
		htmxError(w, http.StatusInternalServerError, "保存配置失败: "+err.Error())
		return
	}

	client, err := h.dial(nodeID)
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "连接节点失败: "+err.Error())
		return
	}

	if err := client.UpdateCaddyConfig(r.Context(), nodes.CaddyConfig{
		ACMEEmail:   acmeEmail,
		PanelDomain: panelDomain,
		PanelPort:   h.panelPort(),
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
	Name      string
	Protocols []string // e.g. ["VLESS", "Trojan", "Shadowsocks"]
}

// userPortalData 传入用户主页模板的数据。
type userPortalData struct {
	User                 users.User
	SubURL               string
	Nodes                []userNodeInfo
	AnnouncementTitle   string
	AnnouncementContent string
	HasAnnouncement     bool
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

	// 收集该用户有权访问的节点及协议列表
	accesses, _ := h.userStore.ListUserInboundsByUser(user.ID)
	var nodeInfos []userNodeInfo
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
		nodeInfos = append(nodeInfos, userNodeInfo{Name: node.Name, Protocols: protocols})
	}

	portalData := userPortalData{User: user, SubURL: subURL(r, user.SubToken), Nodes: nodeInfos}
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
	w.Header().Set("X-Robots-Tag", "noindex")
	h.renderPage(w, "user_portal", pageData{
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
	h.applyNodes(affected)

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
