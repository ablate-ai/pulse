package serverapi

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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

type upsertUserRequest struct {
	ID           string `json:"id"`
	Username     string `json:"username"`
	UUID         string `json:"uuid"`
	Protocol     string `json:"protocol"`
	Secret       string `json:"secret"`
	Method       string `json:"method"`
	NodeID       string `json:"node_id"`
	Domain       string `json:"domain"`
	Port         int    `json:"port"`
	TrafficLimit int64  `json:"traffic_limit_bytes"`
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
		writeJSON(w, http.StatusOK, map[string]any{"users": items})
	case http.MethodPost:
		var req upsertUserRequest
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
		user, err := a.users.Upsert(users.User{
			ID:           req.ID,
			Username:     req.Username,
			UUID:         req.UUID,
			Protocol:     protocol,
			Secret:       req.Secret,
			Method:       req.Method,
			Enabled:      true,
			NodeID:       req.NodeID,
			Domain:       req.Domain,
			Port:         req.Port,
			TrafficLimit: req.TrafficLimit,
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
	case http.MethodDelete:
		if err := a.users.Delete(userID); err != nil {
			writeUserError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodDelete)
	}
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
	case "vless", "trojan", "shadowsocks":
		return true
	default:
		return false
	}
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
	config, err := proxycfg.BuildVLESSSingboxConfig(activeUsers)
	if err != nil {
		return nodes.Status{}, "", err
	}
	status, err := client.Restart(ctx, nodes.ConfigRequest{Config: config})
	return status, config, err
}
