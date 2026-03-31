package serverapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"pulse/internal/idgen"
	"pulse/internal/inbounds"
	"pulse/internal/jobs"
	"pulse/internal/nodes"
	"pulse/internal/outbounds"
	"pulse/internal/users"
)

type API struct {
	store         nodes.Store
	usersStore    users.Store
	inboundStore  inbounds.InboundStore
	outboundStore outbounds.Store
	clientOptions nodes.ClientOptions
	clientFactory func(node nodes.Node) *nodes.Client
	applyOpts     jobs.ApplyOptions
}

type upsertNodeRequest struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
}

type singboxConfigRequest struct {
	Config string `json:"config"`
}

func New(store nodes.Store, clientOptions nodes.ClientOptions) *API {
	return &API{
		store:         store,
		clientOptions: clientOptions,
		clientFactory: func(node nodes.Node) *nodes.Client {
			return nodes.NewClient(node, clientOptions)
		},
	}
}

func NewWithUsers(nodesStore nodes.Store, usersStore users.Store, inboundStore inbounds.InboundStore, outboundStore outbounds.Store, clientOptions nodes.ClientOptions, applyOpts jobs.ApplyOptions) *API {
	api := New(nodesStore, clientOptions)
	api.usersStore = usersStore
	api.inboundStore = inboundStore
	api.outboundStore = outboundStore
	api.applyOpts = applyOpts
	return api
}

func RegisterUsersAPI(mux *http.ServeMux, usersStore users.Store, nodesStore nodes.Store, inboundStore inbounds.InboundStore, outboundStore outbounds.Store, clientOptions nodes.ClientOptions, applyOpts jobs.ApplyOptions) {
	base := New(nodesStore, clientOptions)
	base.inboundStore = inboundStore
	base.outboundStore = outboundStore
	newUserAPI(usersStore, nodesStore, inboundStore, outboundStore, base, applyOpts).Register(mux)
}

func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("/v1/nodes", a.handleNodes)
	mux.HandleFunc("/v1/nodes/", a.handleNodeRoutes)
}

func (a *API) handleNodes(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := a.store.List()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"nodes": items})
	case http.MethodPost:
		var req upsertNodeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
			return
		}
		if req.Name == "" || req.BaseURL == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "name and base_url are required"})
			return
		}
		if strings.TrimSpace(req.ID) == "" {
			req.ID = idgen.NextString()
		}
		node, err := a.store.Upsert(nodes.Node{
			ID:      req.ID,
			Name:    req.Name,
			BaseURL: strings.TrimRight(req.BaseURL, "/"),
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, node)
	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}

func (a *API) handleNodeRoutes(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/nodes/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "node id is required"})
		return
	}

	nodeID := parts[0]
	if len(parts) == 1 {
		a.handleNode(w, r, nodeID)
		return
	}

	switch strings.Join(parts[1:], "/") {
	case "runtime":
		a.handleNodeRuntime(w, r, nodeID)
	case "runtime/status":
		a.handleNodeStatus(w, r, nodeID)
	case "runtime/usage":
		a.handleNodeUsage(w, r, nodeID)
	case "runtime/config":
		a.handleNodeConfig(w, r, nodeID)
	case "runtime/logs":
		a.handleNodeLogs(w, r, nodeID)
	case "runtime/start":
		a.handleNodeStart(w, r, nodeID)
	case "runtime/stop":
		a.handleNodeStop(w, r, nodeID)
	case "runtime/restart":
		a.handleNodeRestart(w, r, nodeID)
	case "runtime/apply":
		a.handleNodeApply(w, r, nodeID)
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "route not found"})
	}
}

func (a *API) handleNode(w http.ResponseWriter, r *http.Request, nodeID string) {
	switch r.Method {
	case http.MethodGet:
		node, err := a.store.Get(nodeID)
		if err != nil {
			writeNodeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, node)
	case http.MethodPut:
		var req upsertNodeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
			return
		}
		if req.Name == "" || req.BaseURL == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "name and base_url are required"})
			return
		}
		node, err := a.store.Upsert(nodes.Node{
			ID:      nodeID,
			Name:    req.Name,
			BaseURL: strings.TrimRight(req.BaseURL, "/"),
		})
		if err != nil {
			writeNodeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, node)
	case http.MethodDelete:
		if err := a.store.Delete(nodeID); err != nil {
			writeNodeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPut+", "+http.MethodDelete)
	}
}

func (a *API) handleNodeRuntime(w http.ResponseWriter, r *http.Request, nodeID string) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	client, err := a.clientFor(nodeID)
	if err != nil {
		writeNodeError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	out, err := client.Runtime(ctx)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) handleNodeStatus(w http.ResponseWriter, r *http.Request, nodeID string) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	client, err := a.clientFor(nodeID)
	if err != nil {
		writeNodeError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	out, err := client.Status(ctx)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) handleNodeConfig(w http.ResponseWriter, r *http.Request, nodeID string) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	client, err := a.clientFor(nodeID)
	if err != nil {
		writeNodeError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	out, err := client.Config(ctx)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) handleNodeLogs(w http.ResponseWriter, r *http.Request, nodeID string) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	client, err := a.clientFor(nodeID)
	if err != nil {
		writeNodeError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	out, err := client.Logs(ctx)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) handleNodeUsage(w http.ResponseWriter, r *http.Request, nodeID string) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	client, err := a.clientFor(nodeID)
	if err != nil {
		writeNodeError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	out, err := client.Usage(ctx, false)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) handleNodeStart(w http.ResponseWriter, r *http.Request, nodeID string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	req, ok := decodeConfigRequest(w, r)
	if !ok {
		return
	}
	client, err := a.clientFor(nodeID)
	if err != nil {
		writeNodeError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	out, err := client.Start(ctx, nodes.ConfigRequest{Config: req.Config})
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) handleNodeStop(w http.ResponseWriter, r *http.Request, nodeID string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	client, err := a.clientFor(nodeID)
	if err != nil {
		writeNodeError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	out, err := client.Stop(ctx)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) handleNodeRestart(w http.ResponseWriter, r *http.Request, nodeID string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	req, ok := decodeConfigRequest(w, r)
	if !ok {
		return
	}
	client, err := a.clientFor(nodeID)
	if err != nil {
		writeNodeError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	out, err := client.Restart(ctx, nodes.ConfigRequest{Config: req.Config})
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) handleNodeApply(w http.ResponseWriter, r *http.Request, nodeID string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	if a.usersStore == nil || a.inboundStore == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "stores not configured"})
		return
	}
	client, err := a.clientFor(nodeID)
	if err != nil {
		writeNodeError(w, err)
		return
	}
	nodeInbounds, err := a.inboundStore.ListInboundsByNode(nodeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	userAccesses, err := a.usersStore.ListUserInboundsByNode(nodeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	userIDs := collectNodeUserIDs(userAccesses)
	userMap, err := a.usersStore.GetUsersByIDs(userIDs)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	node, err := a.store.Get(nodeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	status, _, err := jobs.ApplyNodeUsers(ctx, client, nodeInbounds, userAccesses, userMap, a.inboundStore, a.outboundStore, a.applyOpts, node)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// collectNodeUserIDs 提取用户凭据列表中去重后的 UserID（api.go 内部使用）。
func collectNodeUserIDs(accesses []users.UserInbound) []string {
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

func (a *API) clientFor(nodeID string) (*nodes.Client, error) {
	node, err := a.store.Get(nodeID)
	if err != nil {
		return nil, err
	}
	return a.clientFactory(node), nil
}

// Dial 根据节点 ID 返回 RPC 客户端，可用于 jobs.NodeDialer。
func (a *API) Dial(nodeID string) (*nodes.Client, error) {
	return a.clientFor(nodeID)
}

func decodeConfigRequest(w http.ResponseWriter, r *http.Request) (singboxConfigRequest, bool) {
	var req singboxConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
		return singboxConfigRequest{}, false
	}
	if req.Config == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "config is required"})
		return singboxConfigRequest{}, false
	}
	return req, true
}

func writeNodeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, nodes.ErrNodeNotFound) {
		status = http.StatusNotFound
	}
	writeJSON(w, status, map[string]any{"error": err.Error()})
}

func writeMethodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
