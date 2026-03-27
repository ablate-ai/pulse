package nodeapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	caddyPulseDDir = "/etc/caddy/pulse.d"
	caddyfilePath  = "/etc/caddy/Caddyfile"
)

func (a *API) handleCaddySync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	var req struct {
		Domains []string `json:"domains"`
		WSPort  int      `json:"ws_port"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
		return
	}
	if req.WSPort <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "ws_port is required"})
		return
	}
	if err := syncCaddyRoutes(req.Domains, req.WSPort); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"domains": len(req.Domains),
		"ws_port": req.WSPort,
	})
}

// syncCaddyRoutes 将 Trojan 域名列表同步到 /etc/caddy/pulse.d/，每个域名一个文件，
// 删除不再使用的旧文件，最后热重载 Caddy。
// Caddy 未安装时直接返回（非 A2 节点）。
func syncCaddyRoutes(domains []string, wsPort int) error {
	if _, err := exec.LookPath("caddy"); err != nil {
		return nil
	}
	if err := os.MkdirAll(caddyPulseDDir, 0755); err != nil {
		return fmt.Errorf("create caddy pulse.d dir: %w", err)
	}

	// 写入当前域名的配置文件
	wanted := make(map[string]struct{}, len(domains))
	for _, domain := range domains {
		if domain == "" {
			continue
		}
		wanted[domain] = struct{}{}
		content := fmt.Sprintf(
			"%s {\n\thandle /ws {\n\t\t# Caddy v2 自动处理 WebSocket 升级\n\t\treverse_proxy 127.0.0.1:%d\n\t}\n}\n",
			domain, wsPort,
		)
		path := filepath.Join(caddyPulseDDir, domain+".caddy")
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return fmt.Errorf("write caddy config for %s: %w", domain, err)
		}
	}

	// 删除不再需要的旧域名文件
	entries, err := os.ReadDir(caddyPulseDDir)
	if err != nil {
		return fmt.Errorf("read caddy pulse.d dir: %w", err)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".caddy") {
			continue
		}
		domain := strings.TrimSuffix(entry.Name(), ".caddy")
		if _, ok := wanted[domain]; !ok {
			_ = os.Remove(filepath.Join(caddyPulseDDir, entry.Name()))
		}
	}

	return reloadCaddy()
}

type caddyRoute struct {
	Domain string `json:"domain"`
	Config string `json:"config"`
}

func (a *API) handleCaddyStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	_, err := exec.LookPath("caddy")
	installed := err == nil

	running := false
	if installed {
		running = exec.Command("systemctl", "is-active", "--quiet", "caddy").Run() == nil
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"installed": installed,
		"running":   running,
		"routes":    readCaddyRoutes(),
	})
}

func readCaddyRoutes() []caddyRoute {
	entries, err := os.ReadDir(caddyPulseDDir)
	if err != nil {
		return []caddyRoute{}
	}
	routes := make([]caddyRoute, 0, len(entries))
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".caddy") {
			continue
		}
		domain := strings.TrimSuffix(entry.Name(), ".caddy")
		content, err := os.ReadFile(filepath.Join(caddyPulseDDir, entry.Name()))
		if err != nil {
			continue
		}
		routes = append(routes, caddyRoute{Domain: domain, Config: string(content)})
	}
	return routes
}

func reloadCaddy() error {
	cmd := exec.Command("caddy", "reload", "--config", caddyfilePath, "--adapter", "caddyfile")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("caddy reload: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
