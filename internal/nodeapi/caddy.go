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

type trojanRoute struct {
	Domain string `json:"domain"`
	Port   int    `json:"port"`
}

func (a *API) handleCaddySync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	var req struct {
		Routes []trojanRoute `json:"routes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
		return
	}
	if err := syncCaddyRoutes(req.Routes); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"routes": len(req.Routes),
	})
}

// syncCaddyRoutes 将 Trojan 路由列表同步到 /etc/caddy/pulse.d/，每个域名一个文件，
// 删除不再使用的旧文件，最后热重载 Caddy。
// Caddy 未安装时直接返回（非 A2 节点）。
func syncCaddyRoutes(routes []trojanRoute) error {
	if _, err := exec.LookPath("caddy"); err != nil {
		return nil
	}
	if err := os.MkdirAll(caddyPulseDDir, 0755); err != nil {
		return fmt.Errorf("create caddy pulse.d dir: %w", err)
	}

	// 写入当前路由的配置文件
	wanted := make(map[string]struct{}, len(routes))
	for _, route := range routes {
		if route.Domain == "" {
			continue
		}
		wanted[route.Domain] = struct{}{}
		content := fmt.Sprintf(
			"%s {\n\thandle /ws {\n\t\treverse_proxy 127.0.0.1:%d\n\t}\n}\n",
			route.Domain, route.Port,
		)
		path := filepath.Join(caddyPulseDDir, route.Domain+".caddy")
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return fmt.Errorf("write caddy config for %s: %w", route.Domain, err)
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

	caddyfile := ""
	if installed {
		if b, err := os.ReadFile(caddyfilePath); err == nil {
			caddyfile = string(b)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"installed": installed,
		"running":   running,
		"routes":    readCaddyRoutes(),
		"caddyfile": caddyfile,
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

func (a *API) handleCaddyConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	var req struct {
		ACMEEmail   string `json:"acme_email"`
		PanelDomain string `json:"panel_domain"`
		PanelPort   int    `json:"panel_port"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
		return
	}
	if err := writeCaddyfileFromConfig(req.ACMEEmail, req.PanelDomain, req.PanelPort); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if err := reloadCaddy(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func writeCaddyfileFromConfig(acmeEmail, panelDomain string, panelPort int) error {
	caddyfileDir := filepath.Dir(caddyfilePath)
	if err := os.MkdirAll(filepath.Join(caddyfileDir, "pulse.d"), 0755); err != nil {
		return fmt.Errorf("create pulse.d dir: %w", err)
	}

	var buf strings.Builder
	if acmeEmail != "" {
		fmt.Fprintf(&buf, "{\n\temail %s\n}\n\n", acmeEmail)
	}
	if panelDomain != "" {
		if panelPort <= 0 {
			panelPort = 8080
		}
		fmt.Fprintf(&buf, "# 面板 HTTPS\n%s {\n\thandle {\n\t\treverse_proxy 127.0.0.1:%d\n\t}\n}\n\n", panelDomain, panelPort)
	}
	buf.WriteString("# 由 Pulse 面板自动管理，请勿手动编辑\n")
	fmt.Fprintf(&buf, "import %s/pulse.d/*.caddy\n", caddyfileDir)

	return os.WriteFile(caddyfilePath, []byte(buf.String()), 0644)
}

func reloadCaddy() error {
	// Caddy 未运行时直接启动，运行中则 reload
	if exec.Command("systemctl", "is-active", "--quiet", "caddy").Run() != nil {
		if out, err := exec.Command("systemctl", "start", "caddy").CombinedOutput(); err != nil {
			return fmt.Errorf("caddy start: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	cmd := exec.Command("caddy", "reload", "--config", caddyfilePath, "--adapter", "caddyfile")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("caddy reload: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
