package nodeapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"pulse/internal/nodeauth"
	"pulse/internal/singbox"
)

type API struct {
	manager   *singbox.Manager
	authToken string
}

type singboxConfigRequest struct {
	Config string `json:"config"`
}

func New(manager *singbox.Manager, authToken string) *API {
	return &API{
		manager:   manager,
		authToken: authToken,
	}
}

func (a *API) Register(mux *http.ServeMux) {
	mux.Handle("/v1/node/runtime", nodeauth.Middleware(a.authToken, http.HandlerFunc(a.handleRuntime)))
	mux.Handle("/v1/node/runtime/status", nodeauth.Middleware(a.authToken, http.HandlerFunc(a.handleStatus)))
	mux.Handle("/v1/node/runtime/usage", nodeauth.Middleware(a.authToken, http.HandlerFunc(a.handleUsage)))
	mux.Handle("/v1/node/runtime/version", nodeauth.Middleware(a.authToken, http.HandlerFunc(a.handleVersion)))
	mux.Handle("/v1/node/runtime/logs", nodeauth.Middleware(a.authToken, http.HandlerFunc(a.handleLogs)))
	mux.Handle("/v1/node/runtime/start", nodeauth.Middleware(a.authToken, http.HandlerFunc(a.handleStart)))
	mux.Handle("/v1/node/runtime/stop", nodeauth.Middleware(a.authToken, http.HandlerFunc(a.handleStop)))
	mux.Handle("/v1/node/runtime/restart", nodeauth.Middleware(a.authToken, http.HandlerFunc(a.handleRestart)))
}

func (a *API) handleRuntime(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	writeJSON(w, http.StatusOK, a.manager.RuntimeInfo(ctx))
}

func (a *API) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}

	writeJSON(w, http.StatusOK, a.manager.Status())
}

func (a *API) handleUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}

	writeJSON(w, http.StatusOK, a.manager.Usage())
}

func (a *API) handleVersion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	version, err := a.manager.Version(ctx)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"version": version})
}

func (a *API) handleLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"logs": a.manager.Logs()})
}

func (a *API) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}

	req, ok := decodeConfigRequest(w, r)
	if !ok {
		return
	}

	if err := a.manager.Start(req.Config); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, a.manager.Status())
}

func (a *API) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}

	if err := a.manager.Stop(); err != nil {
		status := http.StatusBadRequest
		if err == singbox.ErrNotRunning {
			status = http.StatusConflict
		}
		writeJSON(w, status, map[string]any{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, a.manager.Status())
}

func (a *API) handleRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}

	req, ok := decodeConfigRequest(w, r)
	if !ok {
		return
	}

	if err := a.manager.Restart(req.Config); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, a.manager.Status())
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
