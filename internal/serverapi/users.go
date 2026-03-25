package serverapi

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"pulse/internal/idgen"
	"pulse/internal/inbounds"
	"pulse/internal/jobs"
	"pulse/internal/nodes"
	"pulse/internal/subscription"
	"pulse/internal/users"
)

type userAPI struct {
	users        users.Store
	nodes        nodes.Store
	inboundStore inbounds.InboundStore
	base         *API
	applyOpts    jobs.ApplyOptions
}

type createUserRequest struct {
	ID                     string     `json:"id"`
	Username               string     `json:"username"`
	TrafficLimit           int64      `json:"traffic_limit_bytes"`
	ExpireAt               *time.Time `json:"expire_at,omitempty"`
	DataLimitResetStrategy string     `json:"data_limit_reset_strategy"`
}

type updateUserRequest struct {
	Status                 string     `json:"status"`
	ExpireAt               *time.Time `json:"expire_at,omitempty"`
	DataLimitResetStrategy string     `json:"data_limit_reset_strategy"`
	TrafficLimit           int64      `json:"traffic_limit_bytes"`
}

// createAccessRequest 添加用户到节点的请求（只需指定节点 ID）。
type createAccessRequest struct {
	ID     string `json:"id"`
	NodeID string `json:"node_id"`
	UUID   string `json:"uuid,omitempty"`   // 可留空自动生成
	Secret string `json:"secret,omitempty"` // 可留空自动生成
}

func newUserAPI(usersStore users.Store, nodesStore nodes.Store, ibStore inbounds.InboundStore, base *API, applyOpts jobs.ApplyOptions) *userAPI {
	return &userAPI{
		users:        usersStore,
		nodes:        nodesStore,
		inboundStore: ibStore,
		base:         base,
		applyOpts:    applyOpts,
	}
}

func (a *userAPI) Register(mux *http.ServeMux) {
	mux.HandleFunc("/v1/users", a.handleUsers)
	mux.HandleFunc("/v1/users/", a.handleUserRoutes)
}

func (a *userAPI) handleUsers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := a.users.ListUsers()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		items = filterUsersByQuery(items, r)
		writeJSON(w, http.StatusOK, map[string]any{"users": items, "total": len(items)})
	case http.MethodPost:
		var req createUserRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
			return
		}
		if req.Username == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "username is required"})
			return
		}
		if strings.TrimSpace(req.ID) == "" {
			req.ID = idgen.NextString()
		}
		if req.DataLimitResetStrategy == "" {
			req.DataLimitResetStrategy = users.ResetStrategyNoReset
		}
		user, err := a.users.UpsertUser(users.User{
			ID:                     req.ID,
			Username:               req.Username,
			Status:                 users.StatusActive,
			ExpireAt:               req.ExpireAt,
			DataLimitResetStrategy: req.DataLimitResetStrategy,
			TrafficLimit:           req.TrafficLimit,
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, user)
	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}

func (a *userAPI) handleUserRoutes(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/users/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "user id is required"})
		return
	}

	userID := parts[0]
	switch len(parts) {
	case 1:
		a.handleUser(w, r, userID)
	case 2:
		switch parts[1] {
		case "inbounds":
			a.handleUserInbounds(w, r, userID)
		default:
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "route not found"})
		}
	case 3:
		if parts[1] == "inbounds" {
			a.handleUserInbound(w, r, userID, parts[2])
		} else {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "route not found"})
		}
	case 4:
		if parts[1] == "inbounds" {
			ibID := parts[2]
			switch parts[3] {
			case "apply":
				a.handleAccessApply(w, r, userID, ibID)
			case "subscription":
				a.handleAccessSubscription(w, r, userID, ibID)
			default:
				writeJSON(w, http.StatusNotFound, map[string]any{"error": "route not found"})
			}
		} else {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "route not found"})
		}
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "route not found"})
	}
}

func (a *userAPI) handleUser(w http.ResponseWriter, r *http.Request, userID string) {
	switch r.Method {
	case http.MethodGet:
		user, err := a.users.GetUser(userID)
		if err != nil {
			writeUserError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, user)
	case http.MethodPut:
		a.handleUpdateUser(w, r, userID)
	case http.MethodDelete:
		if err := a.users.DeleteUser(userID); err != nil {
			writeUserError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPut+", "+http.MethodDelete)
	}
}

func (a *userAPI) handleUpdateUser(w http.ResponseWriter, r *http.Request, userID string) {
	user, err := a.users.GetUser(userID)
	if err != nil {
		writeUserError(w, err)
		return
	}

	var req updateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
		return
	}

	if req.Status != "" {
		if !validStatus(req.Status) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid status"})
			return
		}
		user.Status = req.Status
	}
	if req.ExpireAt != nil {
		user.ExpireAt = req.ExpireAt
	}
	if req.DataLimitResetStrategy != "" {
		if !validResetStrategy(req.DataLimitResetStrategy) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid data_limit_reset_strategy"})
			return
		}
		user.DataLimitResetStrategy = req.DataLimitResetStrategy
	}
	if req.TrafficLimit >= 0 && req.TrafficLimit != user.TrafficLimit {
		user.TrafficLimit = req.TrafficLimit
	}

	updated, err := a.users.UpsertUser(user)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// handleUserInbounds 处理用户的节点访问凭据列表（GET / POST）。
func (a *userAPI) handleUserInbounds(w http.ResponseWriter, r *http.Request, userID string) {
	switch r.Method {
	case http.MethodGet:
		accesses, err := a.users.ListUserInboundsByUser(userID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"inbounds": accesses, "total": len(accesses)})
	case http.MethodPost:
		if _, err := a.users.GetUser(userID); err != nil {
			writeUserError(w, err)
			return
		}
		var req createAccessRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
			return
		}
		if req.NodeID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "node_id is required"})
			return
		}
		if _, err := a.nodes.Get(req.NodeID); err != nil {
			writeNodeError(w, err)
			return
		}
		if strings.TrimSpace(req.ID) == "" {
			req.ID = idgen.NextString()
		}
		if req.UUID == "" {
			req.UUID = randomUUID()
		}
		if req.Secret == "" {
			req.Secret = randomToken(12)
		}
		acc, err := a.users.UpsertUserInbound(users.UserInbound{
			ID:     req.ID,
			UserID: userID,
			NodeID: req.NodeID,
			UUID:   req.UUID,
			Secret: req.Secret,
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, acc)
	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}

func (a *userAPI) handleUserInbound(w http.ResponseWriter, r *http.Request, userID, ibID string) {
	switch r.Method {
	case http.MethodGet:
		acc, err := a.users.GetUserInbound(ibID)
		if err != nil {
			writeUserInboundError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, acc)
	case http.MethodDelete:
		if err := a.users.DeleteUserInbound(ibID); err != nil {
			writeUserInboundError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodDelete)
	}
}

// handleAccessApply 将该节点的完整配置重新下发。
func (a *userAPI) handleAccessApply(w http.ResponseWriter, r *http.Request, userID, ibID string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	acc, err := a.users.GetUserInbound(ibID)
	if err != nil {
		writeUserInboundError(w, err)
		return
	}

	nodeInbounds, err := a.inboundStore.ListInboundsByNode(acc.NodeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	allAccesses, err := a.users.ListUserInboundsByNode(acc.NodeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	userIDs := collectUserIDs(allAccesses)
	userMap, err := a.users.GetUsersByIDs(userIDs)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	client, err := a.base.clientFor(acc.NodeID)
	if err != nil {
		writeNodeError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	status, config, err := jobs.ApplyNodeUsers(ctx, client, nodeInbounds, allAccesses, userMap, a.inboundStore, a.applyOpts)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"access":       acc,
		"users_count":  len(allAccesses),
		"active_users": len(filterEnabledAccesses(allAccesses, userMap)),
		"node_status":  status,
		"node_config":  json.RawMessage(config),
	})
}

// handleAccessSubscription 返回该节点访问凭据对应的所有订阅链接。
func (a *userAPI) handleAccessSubscription(w http.ResponseWriter, r *http.Request, userID, ibID string) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	acc, err := a.users.GetUserInbound(ibID)
	if err != nil {
		writeUserInboundError(w, err)
		return
	}
	user, err := a.users.GetUser(userID)
	if err != nil {
		writeUserError(w, err)
		return
	}

	nodeInbounds, err := a.inboundStore.ListInboundsByNode(acc.NodeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	type linkItem struct {
		Protocol string `json:"protocol"`
		Remark   string `json:"remark"`
		Link     string `json:"link"`
	}

	links := make([]linkItem, 0)
	for _, ib := range nodeInbounds {
		hosts, err := a.inboundStore.ListHostsByInbound(ib.ID)
		if err != nil {
			continue
		}
		for _, h := range hosts {
			link := subscription.Link(ib, h, acc, user)
			remark := h.Remark
			if remark == "" {
				remark = h.Address
			}
			links = append(links, linkItem{
				Protocol: ib.Protocol,
				Remark:   remark,
				Link:     link,
			})
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"links": links})
}

func writeUserError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, users.ErrUserNotFound) {
		status = http.StatusNotFound
	}
	writeJSON(w, status, map[string]any{"error": err.Error()})
}

func writeUserInboundError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, users.ErrUserInboundNotFound) {
		status = http.StatusNotFound
	}
	writeJSON(w, status, map[string]any{"error": err.Error()})
}

func randomUUID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("pulse-%d", time.Now().UnixNano())
	}
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16])
}

func randomToken(size int) string {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("pulse-secret-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", buf)
}

func supportedProtocol(value string) bool {
	switch value {
	case "vless", "vmess", "trojan", "shadowsocks":
		return true
	default:
		return false
	}
}

func validStatus(value string) bool {
	switch value {
	case users.StatusActive, users.StatusDisabled, users.StatusOnHold:
		return true
	default:
		return false
	}
}

func validResetStrategy(value string) bool {
	switch value {
	case users.ResetStrategyNoReset, users.ResetStrategyDay, users.ResetStrategyWeek,
		users.ResetStrategyMonth, users.ResetStrategyYear:
		return true
	default:
		return false
	}
}

// filterUsersByQuery 根据 URL 查询参数过滤用户列表。
func filterUsersByQuery(items []users.User, r *http.Request) []users.User {
	q := r.URL.Query()
	search := strings.ToLower(strings.TrimSpace(q.Get("search")))
	statusFilter := strings.ToLower(strings.TrimSpace(q.Get("status")))

	out := make([]users.User, 0, len(items))
	for _, u := range items {
		if statusFilter != "" && u.EffectiveStatus() != statusFilter {
			continue
		}
		if search != "" {
			hay := strings.ToLower(u.Username)
			if !strings.Contains(hay, search) {
				continue
			}
		}
		out = append(out, u)
	}

	offset := 0
	limit := 0
	if v := q.Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			offset = n
		}
	}
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if offset >= len(out) {
		return []users.User{}
	}
	out = out[offset:]
	if limit > 0 && limit < len(out) {
		out = out[:limit]
	}
	return out
}

func filterEnabledAccesses(accesses []users.UserInbound, userMap map[string]users.User) []users.UserInbound {
	out := make([]users.UserInbound, 0, len(accesses))
	for _, acc := range accesses {
		u, ok := userMap[acc.UserID]
		if ok && u.EffectiveEnabled() {
			out = append(out, acc)
		}
	}
	return out
}

// collectUserIDs 从 accesses 中提取去重后的 UserID（serverapi 内部使用）。
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
