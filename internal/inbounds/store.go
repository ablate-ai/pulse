package inbounds

import "errors"

var (
	ErrInboundNotFound = errors.New("inbound not found")
	ErrHostNotFound    = errors.New("host not found")
)

// Inbound 表示某节点上的一个监听入站（sing-box inbound），含服务端配置。
type Inbound struct {
	ID       string `json:"id"`
	NodeID   string `json:"node_id"`
	Protocol string `json:"protocol"` // vless / vmess / trojan / shadowsocks
	Tag      string `json:"tag"`      // sing-box inbound tag，同节点内唯一
	Port     int    `json:"port"`
	// OutboundID 绑定的出口 ID；空字符串表示直连。
	OutboundID string `json:"outbound_id,omitempty"`
	// Shadowsocks 加密方式
	Method string `json:"method,omitempty"`
	// Shadowsocks 2022 服务端 PSK（仅 2022-blake3-* 系列需要）
	Password string `json:"password,omitempty"`
	// TLS / Reality 服务端配置
	Security             string `json:"security,omitempty"`              // "reality"（VLESS）
	RealityPrivateKey    string `json:"reality_private_key,omitempty"`   // 服务端私钥
	RealityPublicKey     string `json:"reality_public_key,omitempty"`    // 客户端公钥，用于订阅链接
	RealityHandshakeAddr string `json:"reality_handshake_addr,omitempty"` // 握手目标 host:port
	RealityShortID       string `json:"reality_short_id,omitempty"`
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
	// Reality 客户端参数（不填则从关联 Inbound 继承）
	RealityPublicKey string `json:"reality_public_key,omitempty"`
	RealityShortID   string `json:"reality_short_id,omitempty"`
	RealitySpiderX   string `json:"reality_spider_x,omitempty"`
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
