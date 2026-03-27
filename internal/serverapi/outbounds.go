package serverapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"pulse/internal/idgen"
	"pulse/internal/outbounds"
)

type outboundAPI struct {
	store outbounds.Store
}

func RegisterOutboundsAPI(mux *http.ServeMux, store outbounds.Store) {
	a := &outboundAPI{store: store}
	mux.HandleFunc("/v1/outbounds", a.handleOutbounds)
	mux.HandleFunc("/v1/outbounds/", a.handleOutboundRoutes)
}

func (a *outboundAPI) handleOutbounds(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := a.store.List()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"outbounds": items})
	case http.MethodPost:
		var req outbounds.Outbound
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
			return
		}
		if req.Name == "" || req.Server == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "name and server are required"})
			return
		}
		if req.Protocol == "" {
			req.Protocol = "socks5"
		}
		if req.ID == "" {
			req.ID = idgen.NextString()
		}
		item, err := a.store.Upsert(req)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, item)
	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}

func (a *outboundAPI) handleOutboundRoutes(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/outbounds/")
	if id == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "outbound id is required"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		item, err := a.store.Get(id)
		if err != nil {
			writeOutboundError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	case http.MethodPut:
		existing, err := a.store.Get(id)
		if err != nil {
			writeOutboundError(w, err)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&existing); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
			return
		}
		existing.ID = id
		if existing.Protocol == "" {
			existing.Protocol = "socks5"
		}
		item, err := a.store.Upsert(existing)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, item)
	case http.MethodDelete:
		if err := a.store.Delete(id); err != nil {
			writeOutboundError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPut+", "+http.MethodDelete)
	}
}

func writeOutboundError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, outbounds.ErrOutboundNotFound) {
		status = http.StatusNotFound
	}
	writeJSON(w, status, map[string]any{"error": err.Error()})
}
