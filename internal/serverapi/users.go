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
	"pulse/internal/jobs"
	"pulse/internal/nodes"
	"pulse/internal/subscription"
	"pulse/internal/users"
)

type userAPI struct {
	users     users.Store
	nodes     nodes.Store
	base      *API
	applyOpts jobs.ApplyOptions
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

type createInboundRequest struct {
	ID                   string `json:"id"`
	NodeID               string `json:"node_id"`
	Protocol             string `json:"protocol"`
	UUID                 string `json:"uuid"`
	Secret               string `json:"secret"`
	Method               string `json:"method"`
	Security             string `json:"security"`
	Flow                 string `json:"flow"`
	SNI                  string `json:"sni"`
	Fingerprint          string `json:"fingerprint"`
	RealityPublicKey     string `json:"reality_public_key"`
	RealityShortID       string `json:"reality_short_id"`
	RealitySpiderX       string `json:"reality_spider_x"`
	RealityPrivateKey    string `json:"reality_private_key"`
	RealityHandshakeAddr string `json:"reality_handshake_addr"`
	Domain               string `json:"domain"`
	Port                 int    `json:"port"`
}

func newUserAPI(usersStore users.Store, nodesStore nodes.Store, base *API, applyOpts jobs.ApplyOptions) *userAPI {
	return &userAPI{
		users:     usersStore,
		nodes:     nodesStore,
		base:      base,
		applyOpts: applyOpts,
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
				a.handleInboundApply(w, r, userID, ibID)
			case "subscription":
				a.handleInboundSubscription(w, r, userID, ibID)
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

func (a *userAPI) handleUserInbounds(w http.ResponseWriter, r *http.Request, userID string) {
	switch r.Method {
	case http.MethodGet:
		inbounds, err := a.users.ListUserInboundsByUser(userID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"inbounds": inbounds, "total": len(inbounds)})
	case http.MethodPost:
		if _, err := a.users.GetUser(userID); err != nil {
			writeUserError(w, err)
			return
		}
		var req createInboundRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
			return
		}
		if req.NodeID == "" || req.Domain == "" || req.Port == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "node_id, domain and port are required"})
			return
		}
		if _, err := a.nodes.Get(req.NodeID); err != nil {
			writeNodeError(w, err)
			return
		}
		if strings.TrimSpace(req.ID) == "" {
			req.ID = idgen.NextString()
		}
		protocol := strings.ToLower(strings.TrimSpace(req.Protocol))
		if protocol == "" {
			protocol = "vless"
		}
		if !supportedProtocol(protocol) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "unsupported protocol"})
			return
		}
		if req.UUID == "" {
			req.UUID = randomUUID()
		}
		if req.Secret == "" {
			req.Secret = randomToken(12)
		}
		if req.Method == "" {
			req.Method = "aes-128-gcm"
		}
		ib, err := a.users.UpsertUserInbound(users.UserInbound{
			ID:                   req.ID,
			UserID:               userID,
			NodeID:               req.NodeID,
			Protocol:             protocol,
			UUID:                 req.UUID,
			Secret:               req.Secret,
			Method:               req.Method,
			Security:             req.Security,
			Flow:                 req.Flow,
			SNI:                  req.SNI,
			Fingerprint:          req.Fingerprint,
			RealityPublicKey:     req.RealityPublicKey,
			RealityShortID:       req.RealityShortID,
			RealitySpiderX:       req.RealitySpiderX,
			RealityPrivateKey:    req.RealityPrivateKey,
			RealityHandshakeAddr: req.RealityHandshakeAddr,
			Domain:               req.Domain,
			Port:                 req.Port,
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, ib)
	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}

func (a *userAPI) handleUserInbound(w http.ResponseWriter, r *http.Request, userID, ibID string) {
	switch r.Method {
	case http.MethodGet:
		ib, err := a.users.GetUserInbound(ibID)
		if err != nil {
			writeUserInboundError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, ib)
	case http.MethodPut:
		ib, err := a.users.GetUserInbound(ibID)
		if err != nil {
			writeUserInboundError(w, err)
			return
		}
		var req createInboundRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
			return
		}
		if req.NodeID != "" {
			if _, err := a.nodes.Get(req.NodeID); err != nil {
				writeNodeError(w, err)
				return
			}
			ib.NodeID = req.NodeID
		}
		if req.Domain != "" {
			ib.Domain = req.Domain
		}
		if req.Port > 0 {
			ib.Port = req.Port
		}
		if req.Security != "" {
			ib.Security = req.Security
		}
		if req.Flow != "" {
			ib.Flow = req.Flow
		}
		if req.SNI != "" {
			ib.SNI = req.SNI
		}
		if req.Fingerprint != "" {
			ib.Fingerprint = req.Fingerprint
		}
		if req.RealityPublicKey != "" {
			ib.RealityPublicKey = req.RealityPublicKey
		}
		if req.RealityShortID != "" {
			ib.RealityShortID = req.RealityShortID
		}
		if req.RealitySpiderX != "" {
			ib.RealitySpiderX = req.RealitySpiderX
		}
		if req.RealityPrivateKey != "" {
			ib.RealityPrivateKey = req.RealityPrivateKey
		}
		if req.RealityHandshakeAddr != "" {
			ib.RealityHandshakeAddr = req.RealityHandshakeAddr
		}
		updated, err := a.users.UpsertUserInbound(ib)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, updated)
	case http.MethodDelete:
		if err := a.users.DeleteUserInbound(ibID); err != nil {
			writeUserInboundError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPut+", "+http.MethodDelete)
	}
}

func (a *userAPI) handleInboundApply(w http.ResponseWriter, r *http.Request, userID, ibID string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	inbound, err := a.users.GetUserInbound(ibID)
	if err != nil {
		writeUserInboundError(w, err)
		return
	}

	allInbounds, err := a.users.ListUserInboundsByNode(inbound.NodeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	userIDs := collectUserIDs(allInbounds)
	userMap, err := a.users.GetUsersByIDs(userIDs)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	client, err := a.base.clientFor(inbound.NodeID)
	if err != nil {
		writeNodeError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	status, config, err := jobs.ApplyNodeUsers(ctx, client, allInbounds, userMap, a.applyOpts)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}

	// 更新 inbound 的 ApplyCount 和 LastAppliedAt
	inbound.ApplyCount++
	inbound.LastAppliedAt = time.Now().UTC()
	inbound, err = a.users.UpsertUserInbound(inbound)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"inbound":      inbound,
		"users_count":  len(allInbounds),
		"active_users": len(filterEnabledInbounds(allInbounds, userMap)),
		"node_status":  status,
		"node_config":  json.RawMessage(config),
	})
}

func (a *userAPI) handleInboundSubscription(w http.ResponseWriter, r *http.Request, userID, ibID string) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	inbound, err := a.users.GetUserInbound(ibID)
	if err != nil {
		writeUserInboundError(w, err)
		return
	}
	user, err := a.users.GetUser(userID)
	if err != nil {
		writeUserError(w, err)
		return
	}
	link := subscription.Link(inbound, user)
	writeJSON(w, http.StatusOK, map[string]any{"protocol": inbound.Protocol, "link": link})
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
		// limited/expired 由系统自动判断，不允许手动设置
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
// 支持 search / status / offset / limit 参数。
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

	// 分页
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

func filterEnabledInbounds(inbounds []users.UserInbound, userMap map[string]users.User) []users.UserInbound {
	out := make([]users.UserInbound, 0, len(inbounds))
	for _, ib := range inbounds {
		u, ok := userMap[ib.UserID]
		if !ok {
			continue
		}
		if u.EffectiveEnabled() {
			out = append(out, ib)
		}
	}
	return out
}

// collectUserIDs 从入站列表中提取去重后的 UserID 列表（serverapi 内部使用）。
func collectUserIDs(inbounds []users.UserInbound) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, ib := range inbounds {
		if _, ok := seen[ib.UserID]; !ok {
			seen[ib.UserID] = struct{}{}
			out = append(out, ib.UserID)
		}
	}
	return out
}
