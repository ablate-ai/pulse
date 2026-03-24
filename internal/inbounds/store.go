package inbounds

import "errors"

var (
	ErrInboundNotFound = errors.New("inbound not found")
	ErrHostNotFound    = errors.New("host not found")
)

// Inbound 表示某节点上的一个监听入站（sing-box inbound）。
type Inbound struct {
	ID       string `json:"id"`
	NodeID   string `json:"node_id"`
	Protocol string `json:"protocol"` // vless / vmess / trojan / shadowsocks
	Tag      string `json:"tag"`      // sing-box inbound tag，同节点内唯一
	Port     int    `json:"port"`
}

// Host 表示客户端连接模板：地址 + TLS/传输层配置。
// 一个 Inbound 可以有多个 Host（例如不同的域名前置）。
type Host struct {
	ID            string `json:"id"`
	InboundID     string `json:"inbound_id"`
	Remark        string `json:"remark"`
	Address       string `json:"address"`                  // 客户端连接地址（域名 / IP）
	Port          int    `json:"port,omitempty"`           // 覆盖入站端口，0 表示使用入站端口
	SNI           string `json:"sni,omitempty"`            // TLS SNI
	Host          string `json:"host,omitempty"`           // HTTP Host 头
	Path          string `json:"path,omitempty"`           // WebSocket / HTTP path
	Security      string `json:"security,omitempty"`       // none / tls / reality
	ALPN          string `json:"alpn,omitempty"`           // 如 h2,http/1.1
	Fingerprint   string `json:"fingerprint,omitempty"`    // TLS 指纹
	AllowInsecure bool   `json:"allow_insecure,omitempty"` // 跳过证书验证
	MuxEnable     bool   `json:"mux_enable,omitempty"`     // 多路复用
}

// InboundStore 管理 Inbound 和 Host 的持久化。
type InboundStore interface {
	UpsertInbound(inbound Inbound) (Inbound, error)
	GetInbound(id string) (Inbound, error)
	ListInbounds() ([]Inbound, error)
	ListInboundsByNode(nodeID string) ([]Inbound, error)
	DeleteInbound(id string) error

	UpsertHost(host Host) (Host, error)
	GetHost(id string) (Host, error)
	ListHosts() ([]Host, error)
	ListHostsByInbound(inboundID string) ([]Host, error)
	DeleteHost(id string) error
}
