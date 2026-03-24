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
	"pulse/internal/nodes"
	"pulse/internal/proxycfg"
	"pulse/internal/subscription"
	"pulse/internal/users"
)

type userAPI struct {
	users users.Store
	nodes nodes.Store
	base  *API
}

type createUserRequest struct {
	ID                     string     `json:"id"`
	Username               string     `json:"username"`
	UUID                   string     `json:"uuid"`
	Protocol               string     `json:"protocol"`
	Secret                 string     `json:"secret"`
	Method                 string     `json:"method"`
	Security               string     `json:"security"`
	Flow                   string     `json:"flow"`
	SNI                    string     `json:"sni"`
	Fingerprint            string     `json:"fingerprint"`
	RealityPublicKey       string     `json:"reality_public_key"`
	RealityShortID         string     `json:"reality_short_id"`
	RealitySpiderX         string     `json:"reality_spider_x"`
	RealityPrivateKey      string     `json:"reality_private_key"`
	RealityHandshakeAddr   string     `json:"reality_handshake_addr"`
	NodeID                 string     `json:"node_id"`
	Domain                 string     `json:"domain"`
	Port                   int        `json:"port"`
	TrafficLimit           int64      `json:"traffic_limit_bytes"`
	ExpireAt               *time.Time `json:"expire_at,omitempty"`
	DataLimitResetStrategy string     `json:"data_limit_reset_strategy"`
}

type updateUserRequest struct {
	Status                 string     `json:"status"`
	ExpireAt               *time.Time `json:"expire_at,omitempty"`
	DataLimitResetStrategy string     `json:"data_limit_reset_strategy"`
	TrafficLimit           int64      `json:"traffic_limit_bytes"`
	NodeID                 string     `json:"node_id"`
	Domain                 string     `json:"domain"`
	Port                   int        `json:"port"`
	Security               string     `json:"security"`
	Flow                   string     `json:"flow"`
	SNI                    string     `json:"sni"`
	Fingerprint            string     `json:"fingerprint"`
	RealityPublicKey       string     `json:"reality_public_key"`
	RealityShortID         string     `json:"reality_short_id"`
	RealitySpiderX         string     `json:"reality_spider_x"`
	RealityPrivateKey      string     `json:"reality_private_key"`
	RealityHandshakeAddr   string     `json:"reality_handshake_addr"`
}

func newUserAPI(usersStore users.Store, nodesStore nodes.Store, base *API) *userAPI {
	return &userAPI{
		users: usersStore,
		nodes: nodesStore,
		base:  base,
	}
}

func (a *userAPI) Register(mux *http.ServeMux) {
	mux.HandleFunc("/v1/users", a.handleUsers)
	mux.HandleFunc("/v1/users/", a.handleUserRoutes)
}

func (a *userAPI) handleUsers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := a.users.List()
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
		if req.Username == "" || req.NodeID == "" || req.Domain == "" || req.Port == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "username, node_id, domain and port are required"})
			return
		}
		if strings.TrimSpace(req.ID) == "" {
			req.ID = idgen.NextString()
		}
		if _, err := a.nodes.Get(req.NodeID); err != nil {
			writeNodeError(w, err)
			return
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
		if req.DataLimitResetStrategy == "" {
			req.DataLimitResetStrategy = users.ResetStrategyNoReset
		}
		user, err := a.users.Upsert(users.User{
			ID:                     req.ID,
			Username:               req.Username,
			UUID:                   req.UUID,
			Protocol:               protocol,
			Secret:                 req.Secret,
			Method:                 req.Method,
			Security:               req.Security,
			Flow:                   req.Flow,
			SNI:                    req.SNI,
			Fingerprint:            req.Fingerprint,
			RealityPublicKey:       req.RealityPublicKey,
			RealityShortID:         req.RealityShortID,
			RealitySpiderX:         req.RealitySpiderX,
			RealityPrivateKey:      req.RealityPrivateKey,
			RealityHandshakeAddr:   req.RealityHandshakeAddr,
			Status:                 users.StatusActive,
			ExpireAt:               req.ExpireAt,
			DataLimitResetStrategy: req.DataLimitResetStrategy,
			NodeID:                 req.NodeID,
			Domain:                 req.Domain,
			Port:                   req.Port,
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
	if len(parts) == 1 {
		a.handleUser(w, r, userID)
		return
	}

	switch strings.Join(parts[1:], "/") {
	case "subscription":
		a.handleSubscription(w, r, userID)
	case "apply":
		a.handleApply(w, r, userID)
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "route not found"})
	}
}

func (a *userAPI) handleUser(w http.ResponseWriter, r *http.Request, userID string) {
	switch r.Method {
	case http.MethodGet:
		user, err := a.users.Get(userID)
		if err != nil {
			writeUserError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, user)
	case http.MethodPut:
		a.handleUpdateUser(w, r, userID)
	case http.MethodDelete:
		if err := a.users.Delete(userID); err != nil {
			writeUserError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPut+", "+http.MethodDelete)
	}
}

func (a *userAPI) handleUpdateUser(w http.ResponseWriter, r *http.Request, userID string) {
	user, err := a.users.Get(userID)
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
	// expire_at：显式传 null 清除，传具体值则更新
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
	if req.NodeID != "" {
		if _, err := a.nodes.Get(req.NodeID); err != nil {
			writeNodeError(w, err)
			return
		}
		user.NodeID = req.NodeID
	}
	if req.Domain != "" {
		user.Domain = req.Domain
	}
	if req.Port > 0 {
		user.Port = req.Port
	}
	if req.Security != "" {
		user.Security = req.Security
	}
	if req.Flow != "" {
		user.Flow = req.Flow
	}
	if req.SNI != "" {
		user.SNI = req.SNI
	}
	if req.Fingerprint != "" {
		user.Fingerprint = req.Fingerprint
	}
	if req.RealityPublicKey != "" {
		user.RealityPublicKey = req.RealityPublicKey
	}
	if req.RealityShortID != "" {
		user.RealityShortID = req.RealityShortID
	}
	if req.RealitySpiderX != "" {
		user.RealitySpiderX = req.RealitySpiderX
	}
	if req.RealityPrivateKey != "" {
		user.RealityPrivateKey = req.RealityPrivateKey
	}
	if req.RealityHandshakeAddr != "" {
		user.RealityHandshakeAddr = req.RealityHandshakeAddr
	}

	updated, err := a.users.Upsert(user)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (a *userAPI) handleSubscription(w http.ResponseWriter, r *http.Request, userID string) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	user, err := a.users.Get(userID)
	if err != nil {
		writeUserError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"protocol": user.Protocol,
		"link":     subscription.Link(user),
	})
}

func (a *userAPI) handleApply(w http.ResponseWriter, r *http.Request, userID string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	user, err := a.users.Get(userID)
	if err != nil {
		writeUserError(w, err)
		return
	}
	nodeUsers, err := a.users.ListByNode(user.NodeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	client, err := a.base.clientFor(user.NodeID)
	if err != nil {
		writeNodeError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	status, config, err := applyNodeUsers(ctx, client, nodeUsers)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	user.ApplyCount++
	user.LastAppliedAt = time.Now().UTC()
	user, err = a.users.Upsert(user)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user":         user,
		"users_count":  len(nodeUsers),
		"active_users": len(filterEnabledUsers(nodeUsers)),
		"subscription": subscription.Link(user),
		"node_status":  status,
		"node_config":  json.RawMessage(config),
	})
}

func writeUserError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, users.ErrUserNotFound) {
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
// 支持 search / status / protocol / offset / limit 参数。
func filterUsersByQuery(items []users.User, r *http.Request) []users.User {
	q := r.URL.Query()
	search := strings.ToLower(strings.TrimSpace(q.Get("search")))
	statusFilter := strings.ToLower(strings.TrimSpace(q.Get("status")))
	protoFilter := strings.ToLower(strings.TrimSpace(q.Get("protocol")))

	out := make([]users.User, 0, len(items))
	for _, u := range items {
		if protoFilter != "" && strings.ToLower(u.Protocol) != protoFilter {
			continue
		}
		if statusFilter != "" && u.EffectiveStatus() != statusFilter {
			continue
		}
		if search != "" {
			hay := strings.ToLower(u.Username + " " + u.Domain + " " + u.NodeID)
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

func filterEnabledUsers(items []users.User) []users.User {
	out := make([]users.User, 0, len(items))
	for _, user := range items {
		if user.EffectiveEnabled() {
			out = append(out, user)
		}
	}
	return out
}

func applyNodeUsers(ctx context.Context, client *nodes.Client, nodeUsers []users.User) (nodes.Status, string, error) {
	activeUsers := filterEnabledUsers(nodeUsers)
	if len(activeUsers) == 0 {
		status, err := client.Stop(ctx)
		return status, "", err
	}
	// 为 Trojan 用户申请/确认 TLS 证书，将路径回填到 user 中
	if err := ensureTrojanCerts(ctx, client, activeUsers); err != nil {
		return nodes.Status{}, "", err
	}
	config, err := proxycfg.BuildSingboxConfig(activeUsers)
	if err != nil {
		return nodes.Status{}, "", err
	}
	status, err := client.Restart(ctx, nodes.ConfigRequest{Config: config})
	return status, config, err
}

// ensureTrojanCerts 对 Trojan 用户按域名去重调用节点 EnsureCert，并回填证书路径。
func ensureTrojanCerts(ctx context.Context, client *nodes.Client, activeUsers []users.User) error {
	certsByDomain := make(map[string]nodes.CertPaths)
	for i, u := range activeUsers {
		if u.Protocol != "trojan" {
			continue
		}
		domain := u.Domain
		if _, ok := certsByDomain[domain]; !ok {
			paths, err := client.EnsureCert(ctx, domain)
			if err != nil {
				return fmt.Errorf("ensure cert for %s: %w", domain, err)
			}
			certsByDomain[domain] = paths
		}
		activeUsers[i].TLSCertPath = certsByDomain[domain].CertPath
		activeUsers[i].TLSKeyPath = certsByDomain[domain].KeyPath
	}
	return nil
}
