package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"pulse/internal/auth"
	"pulse/internal/buildinfo"
	"pulse/internal/cert"
	"pulse/internal/config"
	"pulse/internal/inbounds"
	"pulse/internal/jobs"
	"pulse/internal/nodes"
	"pulse/internal/serverapi"
	sqliteStore "pulse/internal/store/sqlite"
	"pulse/internal/usage"
	"pulse/internal/users"
)

func Run() error {
	cfg := config.Load()
	if err := cert.EnsureSelfSignedKeyPair(cfg.ServerNodeClientCertFile, cfg.ServerNodeClientKeyFile, "pulse-server-node-client"); err != nil {
		return err
	}
	db, err := sqliteStore.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()

	var store nodes.Store = db.NodeStore()
	var userStore users.Store = db.UserStore()
	var inboundStore inbounds.InboundStore = db.InboundStore()
	authManager := auth.NewManager(cfg.AdminUsername, cfg.AdminPassword)
	clientOptions := nodes.ClientOptions{
		ClientCertFile: cfg.ServerNodeClientCertFile,
		ClientKeyFile:  cfg.ServerNodeClientKeyFile,
	}

	// 启动调度器
	applyOpts := jobs.ApplyOptions{
		TLSProxyMode: true,
		PanelDomain:  cfg.PanelDomain,
		PanelBackend: cfg.PanelFallbackAddr,
	}
	nodeAPI := serverapi.NewWithUsers(store, userStore, clientOptions, applyOpts)
	scheduler := jobs.NewScheduler(nil)
	scheduler.Add(jobs.Job{
		Name:     "sync-usage",
		Interval: 1 * time.Minute,
		Fn: func(ctx context.Context) error {
			_, err := jobs.SyncUsage(ctx, userStore, store, nodeAPI.Dial, applyOpts)
			return err
		},
	})
	scheduler.Add(jobs.Job{
		Name:     "reset-traffic",
		Interval: 1 * time.Minute,
		Fn: func(ctx context.Context) error {
			_, err := jobs.ResetTraffic(ctx, userStore, store, nodeAPI.Dial, applyOpts)
			return err
		},
	})

	ctx, cancelCtx := context.WithCancel(context.Background())
	defer cancelCtx()
	scheduler.Start(ctx)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"service": "pulse-server",
			"status":  "ok",
			"role":    "control-plane",
		})
	})
	mux.HandleFunc("/v1/auth/login", authManager.HandleLogin)
	mux.Handle("/v1/auth/logout", authManager.Middleware(http.HandlerFunc(authManager.HandleLogout)))
	mux.Handle("/v1/auth/me", authManager.Middleware(http.HandlerFunc(authManager.HandleMe)))
	protectedV1 := http.NewServeMux()
	protectedV1.HandleFunc("/v1/system/info", func(w http.ResponseWriter, r *http.Request) {
		summary, err := usage.Build(store, userStore)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"name":                 "pulse-server",
			"description":          "Go rewrite target for Marzban control plane",
			"version":              buildinfo.Version,
			"commit":               buildinfo.Commit,
			"build_date":           buildinfo.BuildDate,
			"addr":                 cfg.ServerAddr,
			"nodes_count":          summary.NodesCount,
			"users_count":          summary.UsersCount,
			"protocols":            summary.Protocols,
			"total_apply_count":    summary.TotalApplyCount,
			"total_upload_bytes":   summary.TotalUploadBytes,
			"total_download_bytes": summary.TotalDownloadBytes,
			"total_used_bytes":     summary.TotalUsedBytes,
			"limited_users_count":  summary.LimitedUsersCount,
			"disabled_users_count": summary.DisabledUsersCount,
			"last_applied_at":      summary.LastAppliedAt,
		})
	})
	registerWeb(mux, cfg.WebDir)
	serverapi.NewWithUsers(store, userStore, clientOptions, applyOpts).Register(protectedV1)
	serverapi.RegisterUsersAPI(protectedV1, userStore, store, clientOptions, applyOpts)
	serverapi.RegisterSystemAPI(protectedV1, userStore, store, clientOptions, applyOpts)
	serverapi.RegisterInboundsAPI(protectedV1, inboundStore)
	serverapi.RegisterToolsAPI(protectedV1)
	mux.Handle("/v1/tools/", authManager.Middleware(protectedV1))
	mux.Handle("/v1/node/settings", authManager.Middleware(protectedV1))
	mux.Handle("/v1/node/settings.pem", authManager.Middleware(protectedV1))
	mux.Handle("/v1/system/info", authManager.Middleware(protectedV1))
	mux.Handle("/v1/system/sync-usage", authManager.Middleware(protectedV1))
	mux.Handle("/v1/nodes", authManager.Middleware(protectedV1))
	mux.Handle("/v1/nodes/", authManager.Middleware(protectedV1))
	mux.Handle("/v1/users", authManager.Middleware(protectedV1))
	mux.Handle("/v1/users/", authManager.Middleware(protectedV1))
	mux.Handle("/v1/inbounds", authManager.Middleware(protectedV1))
	mux.Handle("/v1/inbounds/", authManager.Middleware(protectedV1))
	mux.Handle("/v1/hosts", authManager.Middleware(protectedV1))
	mux.Handle("/v1/hosts/", authManager.Middleware(protectedV1))

	srv := &http.Server{
		Addr:              cfg.ServerAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("pulse-server listening on %s", cfg.ServerAddr)
	err = srv.ListenAndServe()
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
