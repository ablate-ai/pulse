package node

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"pulse/internal/config"
	"pulse/internal/nodeapi"
	"pulse/internal/singbox"
)

func Run() error {
	cfg := config.Load()
	manager := singbox.NewManager()
	runtimeInfo := manager.RuntimeInfo(context.Background())

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		runtimeInfo = manager.RuntimeInfo(r.Context())
		status := "ok"
		if !runtimeInfo.Available || runtimeInfo.Version == "" {
			status = "degraded"
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"service": "pulse-node",
			"status":  status,
			"role":    "node-plane",
		})
	})
	mux.HandleFunc("/v1/node/info", func(w http.ResponseWriter, r *http.Request) {
		runtimeInfo = manager.RuntimeInfo(r.Context())
		writeJSON(w, http.StatusOK, map[string]any{
			"name":        "pulse-node",
			"description": "Go rewrite target for Marzban node runtime",
			"addr":        cfg.NodeAddr,
			"singbox":     runtimeInfo,
		})
	})

	nodeapi.New(manager, cfg.NodeAuthToken).Register(mux)

	srv := &http.Server{
		Addr:              cfg.NodeAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	if runtimeInfo.Available {
		log.Printf("pulse-node sing-box module: %s", runtimeInfo.Module)
		if runtimeInfo.Version != "" {
			log.Printf("pulse-node sing-box version: %s", runtimeInfo.Version)
		}
	} else {
		log.Printf("pulse-node sing-box unavailable: %s", runtimeInfo.LastError)
	}

	log.Printf("pulse-node listening on %s", cfg.NodeAddr)
	err := srv.ListenAndServe()
	if err == nil || err == http.ErrServerClosed {
		return shutdown(srv)
	}

	return err
}

func shutdown(srv *http.Server) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return srv.Shutdown(ctx)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
