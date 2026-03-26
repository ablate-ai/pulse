package panel

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"html/template"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"pulse/internal/auth"
	"pulse/internal/idgen"
	"pulse/internal/inbounds"
	"pulse/internal/jobs"
	"pulse/internal/nodes"
	"pulse/internal/usage"
	"pulse/internal/users"
)

//go:embed templates static
var embedFS embed.FS

const cookieName = "pulse_token"

// Handler 面板 HTTP 处理器，持有所有依赖。
type Handler struct {
	auth      *auth.Manager
	userStore users.Store
	nodeStore nodes.Store
	ibStore   inbounds.InboundStore
	dial      jobs.NodeDialer
	applyOpts jobs.ApplyOptions
	tmpl      *template.Template
}

// pageData 传入完整页面模板的数据结构。
type pageData struct {
	Page     string // "dashboard", "users", "nodes"
	Username string
	Data     any
}

// nodeWithStatus 节点及其运行状态。
type nodeWithStatus struct {
	Node   nodes.Node
	Status string // "online" / "offline"
}

// New 创建 Handler 实例并解析模板。
func New(
	authMgr *auth.Manager,
	userStore users.Store,
	nodeStore nodes.Store,
	ibStore inbounds.InboundStore,
	dial jobs.NodeDialer,
	applyOpts jobs.ApplyOptions,
) (*Handler, error) {
	h := &Handler{
		auth:      authMgr,
		userStore: userStore,
		nodeStore: nodeStore,
		ibStore:   ibStore,
		dial:      dial,
		applyOpts: applyOpts,
	}

	tmpl, err := template.New("").Funcs(templateFuncs()).ParseFS(embedFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("解析模板: %w", err)
	}
	h.tmpl = tmpl
	return h, nil
}

// Register 将所有路由注册到 mux。
func (h *Handler) Register(mux *http.ServeMux) {
	// 公开路由
	mux.HandleFunc("GET /login", h.loginPage)
	mux.HandleFunc("POST /login", h.processLogin)
	mux.HandleFunc("POST /logout", h.processLogout)

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

	mux.HandleFunc("GET /inbounds", h.requireAuth(h.inboundsPage))
	mux.HandleFunc("GET /panel/inbounds/list", h.requireAuth(h.inboundsListPartial))
	mux.HandleFunc("GET /panel/inbounds/new", h.requireAuth(h.inboundNewForm))
	mux.HandleFunc("POST /panel/inbounds", h.requireAuth(h.createInbound))
	mux.HandleFunc("GET /panel/inbounds/{id}/edit", h.requireAuth(h.inboundEditForm))
	mux.HandleFunc("PUT /panel/inbounds/{id}", h.requireAuth(h.updateInbound))
	mux.HandleFunc("DELETE /panel/inbounds/{id}", h.requireAuth(h.deleteInbound))

	mux.HandleFunc("GET /panel/tools/reality-keypair", h.requireAuth(h.realityKeypair))

	mux.HandleFunc("GET /panel/nodes/list", h.requireAuth(h.nodesListPartial))
	mux.HandleFunc("GET /panel/nodes/new", h.requireAuth(h.nodeNewForm))
	mux.HandleFunc("POST /panel/nodes", h.requireAuth(h.createNode))
	mux.HandleFunc("GET /panel/nodes/{id}/edit", h.requireAuth(h.nodeEditForm))
	mux.HandleFunc("PUT /panel/nodes/{id}", h.requireAuth(h.updateNode))
	mux.HandleFunc("DELETE /panel/nodes/{id}", h.requireAuth(h.deleteNode))
	mux.HandleFunc("POST /panel/nodes/{id}/restart", h.requireAuth(h.restartNode))
	mux.HandleFunc("POST /panel/nodes/{id}/start", h.requireAuth(h.startNode))
	mux.HandleFunc("POST /panel/nodes/{id}/stop", h.requireAuth(h.stopNode))
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

func htmxError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("HX-Reswap", "none")
	http.Error(w, msg, status)
}

// renderPage 使用完整 layout 模板渲染页面。
func (h *Handler) renderPage(w http.ResponseWriter, name string, data pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, "模板渲染错误: "+err.Error(), http.StatusInternalServerError)
	}
}

// renderPartial 直接执行模板片段，数据包装为 pageData{Data: data}。
func (h *Handler) renderPartial(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, name, pageData{Data: data}); err != nil {
		http.Error(w, "模板渲染错误: "+err.Error(), http.StatusInternalServerError)
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
			htmxError(w, http.StatusUnauthorized, "用户名或密码错误")
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
	summary, err := usage.Build(h.nodeStore, h.userStore)
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "获取统计数据失败: "+err.Error())
		return
	}
	h.renderPartial(w, "partial-stats", summary)
}

func (h *Handler) usersListPartial(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(r.URL.Query().Get("q"))
	statusFilter := r.URL.Query().Get("status")

	allUsers, err := h.userStore.ListUsers()
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "获取用户列表失败: "+err.Error())
		return
	}

	// 按关键词和状态过滤
	filtered := make([]users.User, 0, len(allUsers))
	for _, u := range allUsers {
		if q != "" && !strings.Contains(strings.ToLower(u.Username), q) {
			continue
		}
		if statusFilter != "" && u.EffectiveStatus() != statusFilter {
			continue
		}
		filtered = append(filtered, u)
	}

	h.renderPartial(w, "partial-user-rows", filtered)
}

func (h *Handler) userNewForm(w http.ResponseWriter, r *http.Request) {
	ibList, err := h.ibStore.ListInbounds()
	if err != nil {
		ibList = nil // 加载失败时继续显示表单，inbound 列表为空
	}
	h.renderPartial(w, "partial-user-new-form", userFormData{Inbounds: ibList})
}

func (h *Handler) createUser(w http.ResponseWriter, r *http.Request) {
	username := r.FormValue("username")
	trafficLimitGBStr := r.FormValue("traffic_limit_gb")
	resetStrategy := r.FormValue("reset_strategy")
	expireAtStr := r.FormValue("expire_at")
	note := r.FormValue("note")

	if username == "" {
		htmxError(w, http.StatusBadRequest, "用户名不能为空")
		return
	}

	var trafficLimit int64
	if trafficLimitGBStr != "" {
		gb, err := strconv.ParseFloat(trafficLimitGBStr, 64)
		if err != nil {
			htmxError(w, http.StatusBadRequest, "流量限制格式无效")
			return
		}
		trafficLimit = int64(math.Round(gb * 1024 * 1024 * 1024))
	}

	var expireAt *time.Time
	if expireAtStr != "" {
		t, err := time.ParseInLocation("2006-01-02T15:04", expireAtStr, time.Local)
		if err != nil {
			htmxError(w, http.StatusBadRequest, "过期时间格式无效")
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
	}

	if _, err := h.userStore.UpsertUser(newUser); err != nil {
		htmxError(w, http.StatusInternalServerError, "创建用户失败: "+err.Error())
		return
	}

	// 处理 inbound 关联
	if selectedIDs := r.Form["inbound_ids"]; len(selectedIDs) > 0 {
		if err := h.syncUserInbounds(newUser.ID, selectedIDs); err != nil {
			htmxError(w, http.StatusInternalServerError, "关联 Inbound 失败: "+err.Error())
			return
		}
	}

	// 返回更新后的用户列表
	h.renderUsersListFromStore(w)
}

func (h *Handler) userEditForm(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	user, err := h.userStore.GetUser(id)
	if err != nil {
		htmxError(w, http.StatusNotFound, "用户不存在")
		return
	}
	ibList, err := h.ibStore.ListInbounds()
	if err != nil {
		ibList = nil
	}
	// 加载用户已关联的节点，用于表单回显勾选状态
	userAccesses, _ := h.userStore.ListUserInboundsByUser(id)
	userNodeIDs := make(map[string]bool, len(userAccesses))
	for _, acc := range userAccesses {
		userNodeIDs[acc.NodeID] = true
	}
	h.renderPartial(w, "partial-user-edit-form", userFormData{
		User:        &user,
		Inbounds:    ibList,
		UserNodeIDs: userNodeIDs,
	})
}

func (h *Handler) updateUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	user, err := h.userStore.GetUser(id)
	if err != nil {
		htmxError(w, http.StatusNotFound, "用户不存在")
		return
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
			htmxError(w, http.StatusBadRequest, "流量限制格式无效")
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
			htmxError(w, http.StatusBadRequest, "过期时间格式无效")
			return
		}
		user.ExpireAt = &t
	} else {
		// 明确清空过期时间
		if r.Form.Has("expire_at") {
			user.ExpireAt = nil
		}
	}

	if _, err := h.userStore.UpsertUser(user); err != nil {
		htmxError(w, http.StatusInternalServerError, "更新用户失败: "+err.Error())
		return
	}

	// 有提交 inbound_sync 标记时（无论是否选中），都同步 inbound 关联
	if r.Form.Has("inbound_sync") {
		if err := h.syncUserInbounds(user.ID, r.Form["inbound_ids"]); err != nil {
			htmxError(w, http.StatusInternalServerError, "关联 Inbound 失败: "+err.Error())
			return
		}
	}

	h.renderUsersListFromStore(w)
}

func (h *Handler) deleteUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.userStore.DeleteUser(id); err != nil {
		htmxError(w, http.StatusInternalServerError, "删除用户失败: "+err.Error())
		return
	}
	h.renderUsersListFromStore(w)
}

func (h *Handler) resetUserTraffic(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	user, err := h.userStore.GetUser(id)
	if err != nil {
		htmxError(w, http.StatusNotFound, "用户不存在")
		return
	}

	now := time.Now()
	user.UploadBytes = 0
	user.DownloadBytes = 0
	user.UsedBytes = 0
	user.LastTrafficResetAt = &now

	if _, err := h.userStore.UpsertUser(user); err != nil {
		htmxError(w, http.StatusInternalServerError, "重置流量失败: "+err.Error())
		return
	}
	h.renderUsersListFromStore(w)
}

// batchUsers 批量操作用户（enable/disable/delete）。
func (h *Handler) batchUsers(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		htmxError(w, http.StatusBadRequest, "参数解析失败")
		return
	}
	action := r.FormValue("action")
	ids := r.Form["ids"]
	if len(ids) == 0 {
		htmxError(w, http.StatusBadRequest, "未选择任何用户")
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
			htmxError(w, http.StatusBadRequest, "未知操作: "+action)
			return
		}
	}
	h.renderUsersListFromStore(w)
}

// settingsPage 渲染设置页面。
func (h *Handler) settingsPage(w http.ResponseWriter, r *http.Request) {
	h.renderPage(w, "settings", pageData{
		Page:     "settings",
		Username: h.currentUsername(r),
	})
}

// changePassword 修改管理员密码。
func (h *Handler) changePassword(w http.ResponseWriter, r *http.Request) {
	current := r.FormValue("current_password")
	newPw := r.FormValue("new_password")
	confirm := r.FormValue("confirm_password")
	if newPw != confirm {
		htmxError(w, http.StatusBadRequest, "两次输入的新密码不一致")
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
		htmxError(w, http.StatusInternalServerError, "获取用户列表失败: "+err.Error())
		return
	}
	h.renderPartial(w, "partial-user-rows", allUsers)
}

func (h *Handler) nodesListPartial(w http.ResponseWriter, r *http.Request) {
	nodeList, err := h.nodeStore.List()
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "获取节点列表失败: "+err.Error())
		return
	}

	result := make([]nodeWithStatus, 0, len(nodeList))
	for _, n := range nodeList {
		ns := nodeWithStatus{Node: n, Status: "offline"}
		client, dialErr := h.dial(n.ID)
		if dialErr == nil {
			ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
			status, statusErr := client.Status(ctx)
			cancel()
			if statusErr == nil && status.Running {
				ns.Status = "online"
			}
		}
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
		htmxError(w, http.StatusBadRequest, "名称和地址不能为空")
		return
	}

	newNode := nodes.Node{
		ID:      idgen.NextString(),
		Name:    name,
		BaseURL: baseURL,
	}

	if _, err := h.nodeStore.Upsert(newNode); err != nil {
		htmxError(w, http.StatusInternalServerError, "创建节点失败: "+err.Error())
		return
	}

	h.renderNodesListFromStore(w, r)
}

func (h *Handler) deleteNode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.nodeStore.Delete(id); err != nil {
		htmxError(w, http.StatusInternalServerError, "删除节点失败: "+err.Error())
		return
	}
	h.renderNodesListFromStore(w, r)
}

func (h *Handler) restartNode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	client, err := h.dial(id)
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "连接节点失败: "+err.Error())
		return
	}

	// 获取节点 inbound 列表
	nodeInbounds, err := h.ibStore.ListInboundsByNode(id)
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "获取 inbound 配置失败: "+err.Error())
		return
	}

	// 获取节点用户凭据
	userAccesses, err := h.userStore.ListUserInboundsByNode(id)
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "获取用户凭据失败: "+err.Error())
		return
	}

	// 批量获取用户
	userIDs := collectUserIDs(userAccesses)
	userMap, err := h.userStore.GetUsersByIDs(userIDs)
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "获取用户数据失败: "+err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	if _, _, err := jobs.ApplyNodeUsers(ctx, client, nodeInbounds, userAccesses, userMap, h.ibStore, h.applyOpts); err != nil {
		htmxError(w, http.StatusInternalServerError, "重启节点失败: "+err.Error())
		return
	}

	// 返回更新后的完整节点列表
	h.nodesListPartial(w, r)
}

func (h *Handler) startNode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	client, err := h.dial(id)
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "连接节点失败: "+err.Error())
		return
	}

	nodeInbounds, err := h.ibStore.ListInboundsByNode(id)
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "获取 inbound 配置失败: "+err.Error())
		return
	}

	userAccesses, err := h.userStore.ListUserInboundsByNode(id)
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "获取用户凭据失败: "+err.Error())
		return
	}

	userIDs := collectUserIDs(userAccesses)
	userMap, err := h.userStore.GetUsersByIDs(userIDs)
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "获取用户数据失败: "+err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	if _, _, err := jobs.ApplyNodeUsers(ctx, client, nodeInbounds, userAccesses, userMap, h.ibStore, h.applyOpts); err != nil {
		htmxError(w, http.StatusInternalServerError, "启动节点失败: "+err.Error())
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) stopNode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	client, err := h.dial(id)
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "连接节点失败: "+err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	if _, err := client.Stop(ctx); err != nil {
		htmxError(w, http.StatusInternalServerError, "停止节点失败: "+err.Error())
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) nodeEditForm(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	node, err := h.nodeStore.Get(id)
	if err != nil {
		htmxError(w, http.StatusNotFound, "节点不存在")
		return
	}
	h.renderPartial(w, "partial-node-edit-form", node)
}

func (h *Handler) updateNode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	node, err := h.nodeStore.Get(id)
	if err != nil {
		htmxError(w, http.StatusNotFound, "节点不存在")
		return
	}

	if name := r.FormValue("name"); name != "" {
		node.Name = name
	}
	if baseURL := r.FormValue("base_url"); baseURL != "" {
		node.BaseURL = baseURL
	}

	if _, err := h.nodeStore.Upsert(node); err != nil {
		htmxError(w, http.StatusInternalServerError, "更新节点失败: "+err.Error())
		return
	}
	h.renderNodesListFromStore(w, r)
}

// ─── 辅助：节点列表渲染 ──────────────────────────────────────────────────────

func (h *Handler) renderNodesListFromStore(w http.ResponseWriter, r *http.Request) {
	nodeList, err := h.nodeStore.List()
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "获取节点列表失败: "+err.Error())
		return
	}

	result := make([]nodeWithStatus, 0, len(nodeList))
	for _, n := range nodeList {
		ns := nodeWithStatus{Node: n, Status: "offline"}
		client, dialErr := h.dial(n.ID)
		if dialErr == nil {
			ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
			status, statusErr := client.Status(ctx)
			cancel()
			if statusErr == nil && status.Running {
				ns.Status = "online"
			}
		}
		result = append(result, ns)
	}

	h.renderPartial(w, "partial-node-rows", result)
}

// userFormData 用户表单页面数据，包含 inbound 列表。
type userFormData struct {
	User        *users.User
	Inbounds    []inbounds.Inbound
	UserNodeIDs map[string]bool // nodeID → true，用于编辑表单回显已选中状态
}

// syncUserInbounds 根据选中的 inbound ID 列表同步用户的节点关联记录。
// 新增：为没有凭据的节点创建 UserInbound；删除：移除未选中节点的旧记录。
func (h *Handler) syncUserInbounds(userID string, selectedInboundIDs []string) error {
	// 收集选中 inbound 对应的 nodeID（去重）
	wantedNodeIDs := make(map[string]struct{})
	for _, ibID := range selectedInboundIDs {
		ib, err := h.ibStore.GetInbound(ibID)
		if err != nil {
			continue
		}
		wantedNodeIDs[ib.NodeID] = struct{}{}
	}

	// 获取该用户现有凭据
	existing, err := h.userStore.ListUserInboundsByUser(userID)
	if err != nil {
		return err
	}
	existingByNode := make(map[string]users.UserInbound, len(existing))
	for _, acc := range existing {
		existingByNode[acc.NodeID] = acc
	}

	// 创建新增节点的凭据
	for nodeID := range wantedNodeIDs {
		if _, ok := existingByNode[nodeID]; !ok {
			acc := users.UserInbound{
				ID:     idgen.NextString(),
				UserID: userID,
				NodeID: nodeID,
				UUID:   panelRandomUUID(),
				Secret: panelRandomToken(12),
			}
			if _, err := h.userStore.UpsertUserInbound(acc); err != nil {
				return err
			}
		}
	}

	// 删除不再选中节点的凭据
	for nodeID, acc := range existingByNode {
		if _, wanted := wantedNodeIDs[nodeID]; !wanted {
			if err := h.userStore.DeleteUserInbound(acc.ID); err != nil {
				return err
			}
		}
	}

	return nil
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

func (h *Handler) inboundsListPartial(w http.ResponseWriter, r *http.Request) {
	list, err := h.ibStore.ListInbounds()
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "获取 Inbound 列表失败: "+err.Error())
		return
	}
	h.renderPartial(w, "partial-inbound-rows", list)
}

func (h *Handler) inboundNewForm(w http.ResponseWriter, r *http.Request) {
	// 获取节点列表供选择
	nodeList, err := h.nodeStore.List()
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "获取节点列表失败: "+err.Error())
		return
	}
	h.renderPartial(w, "partial-inbound-new-form", nodeList)
}

func (h *Handler) createInbound(w http.ResponseWriter, r *http.Request) {
	nodeID := r.FormValue("node_id")
	remark := r.FormValue("remark")
	protocol := r.FormValue("protocol")
	portStr := r.FormValue("port")
	tag := r.FormValue("tag")

	if protocol == "" || portStr == "" || tag == "" || nodeID == "" {
		htmxError(w, http.StatusBadRequest, "节点、协议、端口和标签不能为空")
		return
	}

	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		htmxError(w, http.StatusBadRequest, "端口格式无效")
		return
	}

	ib := inbounds.Inbound{
		ID:                   idgen.NextString(),
		NodeID:               nodeID,
		Protocol:             protocol,
		Tag:                  tag,
		Port:                 port,
		Method:               r.FormValue("method"),
		Security:             r.FormValue("security"),
		TLSCertPath:          r.FormValue("tls_cert_path"),
		TLSKeyPath:           r.FormValue("tls_key_path"),
		RealityPrivateKey:    r.FormValue("reality_private_key"),
		RealityPublicKey:     r.FormValue("reality_public_key"),
		RealityHandshakeAddr: r.FormValue("reality_handshake_addr"),
		RealityShortID:       r.FormValue("reality_short_id"),
	}
	_ = remark

	// VLESS Reality：服务端兜底生成密钥对和 Short ID
	if protocol == "vless" && ib.Security == "reality" {
		if ib.RealityPrivateKey == "" || ib.RealityPublicKey == "" {
			priv, pub, err := generateRealityKeypair()
			if err != nil {
				htmxError(w, http.StatusInternalServerError, "生成 Reality 密钥对失败: "+err.Error())
				return
			}
			ib.RealityPrivateKey = priv
			ib.RealityPublicKey = pub
		}
		if ib.RealityShortID == "" {
			ib.RealityShortID = generateRealityShortID()
		}
	}

	if _, err := h.ibStore.UpsertInbound(ib); err != nil {
		htmxError(w, http.StatusInternalServerError, "创建 Inbound 失败: "+err.Error())
		return
	}
	h.renderInboundsListFromStore(w)
}

func (h *Handler) inboundEditForm(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ib, err := h.ibStore.GetInbound(id)
	if err != nil {
		htmxError(w, http.StatusNotFound, "Inbound 不存在")
		return
	}
	h.renderPartial(w, "partial-inbound-edit-form", ib)
}

func (h *Handler) updateInbound(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ib, err := h.ibStore.GetInbound(id)
	if err != nil {
		htmxError(w, http.StatusNotFound, "Inbound 不存在")
		return
	}

	if protocol := r.FormValue("protocol"); protocol != "" {
		ib.Protocol = protocol
	}
	if portStr := r.FormValue("port"); portStr != "" {
		port, err := strconv.Atoi(portStr)
		if err != nil || port <= 0 || port > 65535 {
			htmxError(w, http.StatusBadRequest, "端口格式无效")
			return
		}
		ib.Port = port
	}
	if tag := r.FormValue("tag"); tag != "" {
		ib.Tag = tag
	}
	ib.Security = r.FormValue("security")
	ib.Method = r.FormValue("method")
	ib.TLSCertPath = r.FormValue("tls_cert_path")
	ib.TLSKeyPath = r.FormValue("tls_key_path")
	ib.RealityPrivateKey = r.FormValue("reality_private_key")
	ib.RealityPublicKey = r.FormValue("reality_public_key")
	ib.RealityHandshakeAddr = r.FormValue("reality_handshake_addr")
	ib.RealityShortID = r.FormValue("reality_short_id")

	if _, err := h.ibStore.UpsertInbound(ib); err != nil {
		htmxError(w, http.StatusInternalServerError, "更新 Inbound 失败: "+err.Error())
		return
	}
	h.renderInboundsListFromStore(w)
}

func (h *Handler) deleteInbound(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.ibStore.DeleteInbound(id); err != nil {
		htmxError(w, http.StatusInternalServerError, "删除 Inbound 失败: "+err.Error())
		return
	}
	h.renderInboundsListFromStore(w)
}

func (h *Handler) renderInboundsListFromStore(w http.ResponseWriter) {
	list, err := h.ibStore.ListInbounds()
	if err != nil {
		htmxError(w, http.StatusInternalServerError, "获取 Inbound 列表失败: "+err.Error())
		return
	}
	h.renderPartial(w, "partial-inbound-rows", list)
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
				return "永不"
			}
			return t.Format("2006-01-02")
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
				return "活跃"
			case users.StatusDisabled:
				return "已禁用"
			case users.StatusLimited:
				return "已限速"
			case users.StatusExpired:
				return "已过期"
			case users.StatusOnHold:
				return "暂停"
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

func panelRandomToken(size int) string {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("pulse-secret-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", buf)
}
