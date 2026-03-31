package server

import (
	"context"
	"encoding/json"
	"fmt"
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
	"pulse/internal/outbounds"
	"pulse/internal/panel"
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
	var outboundStore outbounds.Store = db.OutboundStore()
	authManager := auth.NewManager(cfg.AdminUsername, cfg.AdminPassword, db.SessionStore())
	clientOptions := nodes.ClientOptions{
		ClientCertFile: cfg.ServerNodeClientCertFile,
		ClientKeyFile:  cfg.ServerNodeClientKeyFile,
	}

	// 启动调度器
	applyOpts := jobs.ApplyOptions{}
	nodeAPI := serverapi.NewWithUsers(store, userStore, inboundStore, outboundStore, clientOptions, applyOpts)
	scheduler := jobs.NewScheduler(nil)
	scheduler.Add(jobs.Job{
		Name:     "sync-usage",
		Interval: 1 * time.Minute,
		Fn: func(ctx context.Context) error {
			_, err := jobs.SyncUsage(ctx, userStore, store, inboundStore, nodeAPI.Dial, applyOpts, outboundStore)
			return err
		},
	})
	scheduler.Add(jobs.Job{
		Name:     "reset-traffic",
		Interval: 1 * time.Minute,
		Fn: func(ctx context.Context) error {
			_, err := jobs.ResetTraffic(ctx, userStore, store, inboundStore, nodeAPI.Dial, applyOpts, outboundStore)
			return err
		},
	})
	scheduler.Add(jobs.Job{
		Name:     "activate-on-hold",
		Interval: 1 * time.Minute,
		Fn: func(ctx context.Context) error {
			return jobs.ActivateExpiredOnHold(ctx, userStore, store, inboundStore, nodeAPI.Dial, applyOpts, outboundStore)
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
		summary, err := usage.Build(store, userStore, 14)
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
			"total_upload_bytes":   summary.TotalUploadBytes,
			"total_download_bytes": summary.TotalDownloadBytes,
			"total_used_bytes":     summary.TotalUsedBytes,
			"active_users_count":   summary.ActiveUsersCount,
			"limited_users_count":  summary.LimitedUsersCount,
			"disabled_users_count": summary.DisabledUsersCount,
			"expired_users_count":  summary.ExpiredUsersCount,
			})
	})
	// 公开订阅端点，无需认证
	serverapi.RegisterSubAPI(mux, userStore, inboundStore)

	// 面板（HTMX + 服务端模板）
	panelHandler, err := panel.New(authManager, userStore, store, inboundStore, outboundStore, nodeAPI.Dial, applyOpts, cfg.ServerAddr, cfg.ServerNodeClientCertFile, db.SettingsStore())
	if err != nil {
		return fmt.Errorf("初始化面板: %w", err)
	}
	panelHandler.Register(mux)
	panelHandler.Start(ctx)
	serverapi.NewWithUsers(store, userStore, inboundStore, outboundStore, clientOptions, applyOpts).Register(protectedV1)
	serverapi.RegisterUsersAPI(protectedV1, userStore, store, inboundStore, outboundStore, clientOptions, applyOpts)
	serverapi.RegisterSystemAPIWithInbounds(protectedV1, userStore, store, inboundStore, clientOptions, applyOpts)
	serverapi.RegisterInboundsAPI(protectedV1, inboundStore, userStore, store, outboundStore, nodeAPI.Dial, applyOpts)
	serverapi.RegisterOutboundsAPI(protectedV1, outboundStore)
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
	mux.Handle("/v1/outbounds", authManager.Middleware(protectedV1))
	mux.Handle("/v1/outbounds/", authManager.Middleware(protectedV1))

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
