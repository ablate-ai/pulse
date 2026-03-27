package nodes

import "errors"

var ErrNodeNotFound = errors.New("node not found")

type Node struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	BaseURL          string `json:"base_url"`
	UploadBytes      int64  `json:"upload_bytes"`
	DownloadBytes    int64  `json:"download_bytes"`
	CaddyACMEEmail   string `json:"caddy_acme_email"`
	CaddyPanelDomain string `json:"caddy_panel_domain"`
	CaddyEnabled     bool   `json:"caddy_enabled"`
	// 出口转发配置（留空表示直连）
	ForwardEnabled  bool   `json:"forward_enabled"`
	ForwardProtocol string `json:"forward_protocol,omitempty"` // socks5 / http
	ForwardServer   string `json:"forward_server,omitempty"`   // host:port
	ForwardUsername string `json:"forward_username,omitempty"`
	ForwardPassword string `json:"forward_password,omitempty"`
}

type Store interface {
	Upsert(node Node) (Node, error)
	Delete(id string) error
	Get(id string) (Node, error)
	List() ([]Node, error)
	// AddTraffic 原子性地将 upload/download 字节数累加到节点流量计数器。
	AddTraffic(nodeID string, upload, download int64) error
	UpdateCaddyConfig(nodeID, acmeEmail, panelDomain string, caddyEnabled bool) error
}
