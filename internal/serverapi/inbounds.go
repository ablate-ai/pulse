package serverapi

import (
	"context"
	"encoding/json"
	"errors"
	"log"
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

type inboundAPI struct {
	store         inbounds.InboundStore
	userStore     users.Store
	nodeStore     nodes.Store
	outboundStore outbounds.Store
	dial          jobs.NodeDialer
	applyOpts     jobs.ApplyOptions
}

func RegisterInboundsAPI(mux *http.ServeMux, store inbounds.InboundStore, userStore users.Store, nodeStore nodes.Store, outboundStore outbounds.Store, dial jobs.NodeDialer, applyOpts jobs.ApplyOptions) {
	a := &inboundAPI{store: store, userStore: userStore, nodeStore: nodeStore, outboundStore: outboundStore, dial: dial, applyOpts: applyOpts}
	mux.HandleFunc("/v1/inbounds", a.handleInbounds)
	mux.HandleFunc("/v1/inbounds/", a.handleInboundRoutes)
	mux.HandleFunc("/v1/hosts", a.handleHosts)
	mux.HandleFunc("/v1/hosts/", a.handleHostRoutes)
}

// ─── Inbound handlers ────────────────────────────────────────────────────────

func (a *inboundAPI) handleInbounds(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		var items []inbounds.Inbound
		var err error
		if nodeID := r.URL.Query().Get("node_id"); nodeID != "" {
			items, err = a.store.ListInboundsByNode(nodeID)
		} else {
			items, err = a.store.ListInbounds()
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"inbounds": items})
	case http.MethodPost:
		var req inbounds.Inbound
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
			return
		}
		if req.NodeID == "" || req.Protocol == "" || req.Port == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "node_id, protocol and port are required"})
			return
		}
		if !supportedProtocol(req.Protocol) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "unsupported protocol"})
			return
		}
		if req.ID == "" {
			req.ID = idgen.NextString()
		}
		if req.Tag == "" {
			req.Tag = "pulse-" + req.Protocol + "-" + req.ID
		}
		item, err := a.store.UpsertInbound(req)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		a.applyInboundNode(item.NodeID)
		writeJSON(w, http.StatusOK, item)
	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}

func (a *inboundAPI) handleInboundRoutes(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/inbounds/")
	if id == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "inbound id is required"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		item, err := a.store.GetInbound(id)
		if err != nil {
			writeInboundError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	case http.MethodPut:
		var req inbounds.Inbound
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
			return
		}
		req.ID = id
		if req.Protocol != "" && !supportedProtocol(req.Protocol) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "unsupported protocol"})
			return
		}
		// 合并现有字段
		existing, err := a.store.GetInbound(id)
		if err != nil {
			writeInboundError(w, err)
			return
		}
		if req.NodeID == "" {
			req.NodeID = existing.NodeID
		}
		if req.Protocol == "" {
			req.Protocol = existing.Protocol
		}
		if req.Tag == "" {
			req.Tag = existing.Tag
		}
		if req.Port == 0 {
			req.Port = existing.Port
		}
		item, err := a.store.UpsertInbound(req)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		a.applyInboundNode(item.NodeID)
		writeJSON(w, http.StatusOK, item)
	case http.MethodDelete:
		existing, err := a.store.GetInbound(id)
		if err != nil {
			writeInboundError(w, err)
			return
		}
		if err := a.store.DeleteInbound(id); err != nil {
			writeInboundError(w, err)
			return
		}
		a.applyInboundNode(existing.NodeID)
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPut+", "+http.MethodDelete)
	}
}

// ─── Host handlers ────────────────────────────────────────────────────────────

func (a *inboundAPI) handleHosts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		inboundID := r.URL.Query().Get("inbound_id")
		var items []inbounds.Host
		var err error
		if inboundID != "" {
			items, err = a.store.ListHostsByInbound(inboundID)
		} else {
			items, err = a.store.ListHosts()
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"hosts": items})
	case http.MethodPost:
		var req inbounds.Host
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
			return
		}
		if req.InboundID == "" || req.Address == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "inbound_id and address are required"})
			return
		}
		if _, err := a.store.GetInbound(req.InboundID); err != nil {
			writeInboundError(w, err)
			return
		}
		if req.ID == "" {
			req.ID = idgen.NextString()
		}
		if req.Security == "" {
			req.Security = "none"
		}
		item, err := a.store.UpsertHost(req)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, item)
	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}

func (a *inboundAPI) handleHostRoutes(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/hosts/")
	if id == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "host id is required"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		item, err := a.store.GetHost(id)
		if err != nil {
			writeHostError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	case http.MethodPut:
		existing, err := a.store.GetHost(id)
		if err != nil {
			writeHostError(w, err)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&existing); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
			return
		}
		existing.ID = id
		item, err := a.store.UpsertHost(existing)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, item)
	case http.MethodDelete:
		if err := a.store.DeleteHost(id); err != nil {
			writeHostError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPut+", "+http.MethodDelete)
	}
}

// applyInboundNode 在后台异步将指定节点的最新配置下发到节点（inbound 变更后调用）。
func (a *inboundAPI) applyInboundNode(nodeID string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := jobs.ApplyNode(ctx, nodeID, a.nodeStore, a.userStore, a.store, a.outboundStore, a.dial, a.applyOpts); err != nil {
			log.Printf("warn: apply node %s after inbound change: %v", nodeID, err)
		}
	}()
}

func writeInboundError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, inbounds.ErrInboundNotFound) {
		status = http.StatusNotFound
	}
	writeJSON(w, status, map[string]any{"error": err.Error()})
}

func writeHostError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, inbounds.ErrHostNotFound) {
		status = http.StatusNotFound
	}
	writeJSON(w, status, map[string]any{"error": err.Error()})
}
