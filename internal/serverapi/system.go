package serverapi

import (
	"context"
	"net/http"
	"time"

	"pulse/internal/cert"
	"pulse/internal/config"
	"pulse/internal/jobs"
	"pulse/internal/nodes"
	"pulse/internal/users"
)

type systemAPI struct {
	users             users.Store
	nodes             nodes.Store
	base              *API
	nodeClientCertPEM string
	applyOpts         jobs.ApplyOptions
}

func RegisterSystemAPI(mux *http.ServeMux, usersStore users.Store, nodesStore nodes.Store, clientOptions nodes.ClientOptions, applyOpts jobs.ApplyOptions) {
	cfg := config.Load()
	clientCertPEM, _ := cert.ReadCertificatePEM(cfg.ServerNodeClientCertFile)
	base := New(nodesStore, clientOptions)
	api := &systemAPI{
		users:             usersStore,
		nodes:             nodesStore,
		base:              base,
		nodeClientCertPEM: clientCertPEM,
		applyOpts:         applyOpts,
	}
	mux.HandleFunc("/v1/node/settings", api.handleNodeSettings)
	mux.HandleFunc("/v1/node/settings.pem", api.handleNodeSettingsPEM)
	mux.HandleFunc("/v1/system/sync-usage", api.handleSyncUsage)
}

func (a *systemAPI) handleNodeSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"certificate": a.nodeClientCertPEM,
	})
}

func (a *systemAPI) handleNodeSettingsPEM(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(a.nodeClientCertPEM))
}

func (a *systemAPI) handleSyncUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	result, err := jobs.SyncUsage(ctx, a.users, a.nodes, a.base.Dial, a.applyOpts)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}
