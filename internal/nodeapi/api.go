package nodeapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"pulse/internal/buildinfo"
	"pulse/internal/certmgr"
	"pulse/internal/singbox"
)

type API struct {
	manager *singbox.Manager
	certs   *certmgr.Manager
}

type singboxConfigRequest struct {
	Config string `json:"config"`
}

func New(manager *singbox.Manager, certs *certmgr.Manager) *API {
	return &API{manager: manager, certs: certs}
}

func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/node/check", a.handleCheck)
	mux.HandleFunc("/v1/node/caddy/sync", a.handleCaddySync)
	mux.HandleFunc("GET /v1/node/caddy/status", a.handleCaddyStatus)
	mux.HandleFunc("POST /v1/node/caddy/config", a.handleCaddyConfig)
	mux.HandleFunc("/v1/node/cert/ensure", a.handleCertEnsure)
	mux.HandleFunc("/v1/node/runtime", a.handleRuntime)
	mux.HandleFunc("/v1/node/runtime/status", a.handleStatus)
	mux.HandleFunc("/v1/node/runtime/usage", a.handleUsage)
	mux.HandleFunc("/v1/node/runtime/version", a.handleVersion)
	mux.HandleFunc("/v1/node/runtime/config", a.handleConfig)
	mux.HandleFunc("/v1/node/runtime/logs", a.handleLogs)
	mux.HandleFunc("GET /v1/node/runtime/logs/stream", a.handleLogsStream)
	mux.HandleFunc("/v1/node/runtime/start", a.handleStart)
	mux.HandleFunc("/v1/node/runtime/stop", a.handleStop)
	mux.HandleFunc("/v1/node/runtime/restart", a.handleRestart)
}

func (a *API) handleRuntime(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	info := a.manager.RuntimeInfo(ctx)
	writeJSON(w, http.StatusOK, map[string]any{
		"available":    info.Available,
		"module":       info.Module,
		"version":      info.Version,
		"last_error":   info.LastError,
		"node_version": buildinfo.Version,
	})
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
	reset := r.URL.Query().Get("reset") == "true"
	writeJSON(w, http.StatusOK, a.manager.Usage(reset))
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

func (a *API) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"config": a.manager.Config()})
}

func (a *API) handleLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"logs": a.manager.Logs()})
}

func (a *API) handleLogsStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")

	// 先把缓冲区里的历史日志全部发送
	for _, line := range a.manager.Logs() {
		fmt.Fprintf(w, "data: %s\n\n", line)
	}
	flusher.Flush()

	// 订阅后续新日志
	id, ch := a.manager.Subscribe()
	defer a.manager.Unsubscribe(id)

	for {
		select {
		case <-r.Context().Done():
			return
		case line, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", line)
			flusher.Flush()
		}
	}
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

	if err := a.manager.Stop(); err != nil && err != singbox.ErrNotRunning {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
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

func (a *API) handleCertEnsure(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	var req struct {
		Domain string `json:"domain"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Domain == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "domain is required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	if err := a.certs.Ensure(ctx, req.Domain); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"domain":    req.Domain,
		"cert_path": a.certs.CertPath(req.Domain),
		"key_path":  a.certs.KeyPath(req.Domain),
	})
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
