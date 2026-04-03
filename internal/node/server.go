package node

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"pulse/internal/cert"
	"pulse/internal/certmgr"
	"pulse/internal/config"
	"pulse/internal/nodeapi"
	"pulse/internal/singbox"
)

// singboxConfigPath 根据配置计算 sing-box 快照文件路径。
// 优先使用环境变量 PULSE_SINGBOX_LAST_CONFIG_FILE；
// 否则与节点 TLS key 同目录，避免落在程序工作目录。
func singboxConfigPath(cfg config.Config) string {
	if cfg.SingboxLastConfigFile != "" {
		return cfg.SingboxLastConfigFile
	}
	if cfg.NodeTLSKeyFile != "" {
		return filepath.Join(filepath.Dir(cfg.NodeTLSKeyFile), "singbox_last.json")
	}
	return "./singbox_last.json"
}

func Run() error {
	cfg := config.Load()
	manager := singbox.NewManagerWithPersistence(singboxConfigPath(cfg))
	runtimeInfo := manager.RuntimeInfo(context.Background())

	cm := certmgr.New(cfg.CertDir, cfg.ACMEEmail)

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

	nodeapi.New(manager, cm).Register(mux)

	tlsConfig, err := buildTLSConfig(cfg)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              cfg.NodeAddr,
		Handler:           mux,
		TLSConfig:         tlsConfig,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second, // 节点无 SSE，限制慢客户端占用
		IdleTimeout:       120 * time.Second,
	}

	if runtimeInfo.Available {
		log.Printf("pulse-node sing-box module: %s", runtimeInfo.Module)
		if runtimeInfo.Version != "" {
			log.Printf("pulse-node sing-box version: %s", runtimeInfo.Version)
		}
	} else {
		log.Printf("pulse-node sing-box unavailable: %s", runtimeInfo.LastError)
	}

	// 进程重启后自动恢复：若磁盘上有上次的配置则直接启动
	if saved := manager.SavedConfig(); saved != "" {
		log.Printf("pulse-node restoring sing-box from saved config")
		if err := manager.Start(saved); err != nil {
			log.Printf("pulse-node auto-start failed: %v", err)
		} else {
			log.Printf("pulse-node sing-box auto-started")
		}
	}

	log.Printf("pulse-node listening on %s", cfg.NodeAddr)
	err = srv.ListenAndServeTLS("", "")
	if err == nil || err == http.ErrServerClosed {
		return shutdown(srv)
	}

	return err
}

func buildTLSConfig(cfg config.Config) (*tls.Config, error) {
	if err := cert.EnsureSelfSignedKeyPair(cfg.NodeTLSCertFile, cfg.NodeTLSKeyFile, "pulse-node"); err != nil {
		return nil, err
	}
	if cfg.NodeTLSClientCertFile == "" {
		return nil, fmt.Errorf("PULSE_NODE_TLS_CLIENT_CERT_FILE is required")
	}

	certPair, err := tls.LoadX509KeyPair(cfg.NodeTLSCertFile, cfg.NodeTLSKeyFile)
	if err != nil {
		return nil, err
	}
	clientPEM, err := os.ReadFile(cfg.NodeTLSClientCertFile)
	if err != nil {
		return nil, err
	}
	clientPool := x509.NewCertPool()
	if !clientPool.AppendCertsFromPEM(clientPEM) {
		return nil, fmt.Errorf("parse client certificate")
	}

	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{certPair},
		ClientCAs:    clientPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}, nil
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
