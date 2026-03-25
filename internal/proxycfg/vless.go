package proxycfg

import (
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strconv"

	"pulse/internal/users"
)

type singboxConfig struct {
	Log       map[string]any   `json:"log"`
	Inbounds  []inbound        `json:"inbounds"`
	Outbounds []map[string]any `json:"outbounds"`
}

type inbound struct {
	Type       string           `json:"type"`
	Tag        string           `json:"tag"`
	Listen     string           `json:"listen"`
	ListenPort int              `json:"listen_port"`
	Users      []map[string]any `json:"users"`
	Transport  map[string]any   `json:"transport,omitempty"`
	TLS        map[string]any   `json:"tls,omitempty"`
	Method     string           `json:"method,omitempty"`
	Password   string           `json:"password,omitempty"`
}

// BuildOptions 控制 BuildSingboxConfig 的可选行为。
type BuildOptions struct {
	// TLSProxyMode 为 true 时，Trojan inbound 改为 WebSocket 模式并监听本地端口，
	// 由外部 TLS proxy 统一终止 TLS 并反代过来。
	TLSProxyMode bool
}

// TLSRoute 描述 TLS proxy 的一条路由规则。
type TLSRoute struct {
	Host    string // 客户端域名
	Backend string // 后端地址 127.0.0.1:port
}

func BuildSingboxConfig(nodeUsers []users.User, opts BuildOptions) (string, error) {
	if len(nodeUsers) == 0 {
		return "", fmt.Errorf("at least one user is required")
	}

	type inboundKey struct {
		Port     int
		Tag      string
		Protocol string
		Method   string
	}

	inboundIndex := make(map[inboundKey]int)
	inbounds := make([]inbound, 0)

	for _, user := range nodeUsers {
		key := inboundKey{
			Port:     user.Port,
			Tag:      inboundTag(user),
			Protocol: protocolOf(user),
			Method:   methodOf(user),
		}

		index, ok := inboundIndex[key]
		if !ok {
			listen, listenPort := listenAddrFor(user, opts)
			inbounds = append(inbounds, inbound{
				Type:       key.Protocol,
				Tag:        key.Tag,
				Listen:     listen,
				ListenPort: listenPort,
				Users:      make([]map[string]any, 0, 1),
				Transport:  transportFor(key.Protocol, opts),
				TLS:        tlsFor(user, opts),
				Method:     key.Method,
				Password:   inboundPasswordFor(key.Protocol, key.Method),
			})
			index = len(inbounds) - 1
			inboundIndex[key] = index
		}

		inbounds[index].Users = append(inbounds[index].Users, inboundUser(user)...)
	}

	sort.Slice(inbounds, func(i, j int) bool {
		if inbounds[i].ListenPort == inbounds[j].ListenPort {
			return inbounds[i].Tag < inbounds[j].Tag
		}
		return inbounds[i].ListenPort < inbounds[j].ListenPort
	})

	cfg := singboxConfig{
		Log: map[string]any{
			"level": "warn",
		},
		Inbounds: inbounds,
		Outbounds: []map[string]any{
			{
				"type": "direct",
				"tag":  "direct",
			},
		},
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal sing-box config: %w", err)
	}
	return string(data), nil
}

func inboundTag(user users.User) string {
	if user.InboundTag != "" {
		return user.InboundTag
	}
	return fmt.Sprintf("pulse-%s-%d", protocolOf(user), user.Port)
}

func protocolOf(user users.User) string {
	if user.Protocol == "" {
		return "vless"
	}
	return user.Protocol
}

func methodOf(user users.User) string {
	if protocolOf(user) != "shadowsocks" {
		return ""
	}
	if user.Method != "" {
		return user.Method
	}
	return "aes-128-gcm"
}

// localPortFor 将外部端口映射为 sing-box 本地 WS 监听端口（+20000，避免冲突）。
func localPortFor(externalPort int) int {
	return 20000 + externalPort%20000
}

// listenAddrFor 返回 inbound 的监听地址和端口。
// TLSProxyMode 下 Trojan 监听在本地，其余协议不变。
func listenAddrFor(user users.User, opts BuildOptions) (listen string, port int) {
	if opts.TLSProxyMode && protocolOf(user) == "trojan" {
		return "127.0.0.1", localPortFor(user.Port)
	}
	return "::", user.Port
}

// BuildTLSRoutes 根据用户列表生成 TLS proxy 路由表。
// 每个 Trojan 用户的域名映射到对应的本地 WS 端口。
// 若 panelDomain/panelBackend 非空，面板域名也加入路由。
func BuildTLSRoutes(nodeUsers []users.User, panelDomain, panelBackend string) []TLSRoute {
	seen := make(map[string]bool)
	routes := make([]TLSRoute, 0)

	for _, u := range nodeUsers {
		if protocolOf(u) != "trojan" || u.Domain == "" {
			continue
		}
		if seen[u.Domain] {
			continue
		}
		seen[u.Domain] = true
		routes = append(routes, TLSRoute{
			Host:    u.Domain,
			Backend: fmt.Sprintf("127.0.0.1:%d", localPortFor(u.Port)),
		})
	}

	if panelDomain != "" && panelBackend != "" {
		routes = append(routes, TLSRoute{Host: panelDomain, Backend: panelBackend})
	}
	return routes
}

func transportFor(protocol string, opts BuildOptions) map[string]any {
	if opts.TLSProxyMode && protocol == "trojan" {
		return map[string]any{"type": "ws", "path": "/ws"}
	}
	return nil
}

func inboundPasswordFor(protocol, method string) string {
	switch protocol {
	case "shadowsocks":
		return "pulse-shared-secret"
	default:
		return ""
	}
}

// tlsFor 根据协议、security 和选项选择 TLS 配置。
// TLSProxyMode 下 Trojan 的 TLS 由外部代理处理，inbound 本身不需要 TLS。
func tlsFor(user users.User, opts BuildOptions) map[string]any {
	if protocolOf(user) == "trojan" {
		if opts.TLSProxyMode {
			return nil // TLS 由 TLS proxy 终止
		}
		return trojanTLSFor(user)
	}
	return realityTLSFor(user)
}

// trojanTLSFor 生成 Trojan inbound 的标准 TLS 配置（非 TLSProxyMode 时使用）。
func trojanTLSFor(user users.User) map[string]any {
	if user.TLSCertPath == "" || user.TLSKeyPath == "" {
		return nil
	}
	return map[string]any{
		"enabled":          true,
		"certificate_path": user.TLSCertPath,
		"key_path":         user.TLSKeyPath,
	}
}

// realityTLSFor 生成 sing-box inbound 的 Reality TLS 配置。
// 仅当 Security=="reality" 且 RealityPrivateKey 非空时生效。
func realityTLSFor(user users.User) map[string]any {
	if user.Security != "reality" || user.RealityPrivateKey == "" {
		return nil
	}

	handshakeServer := "www.google.com"
	handshakePort := 443
	if user.RealityHandshakeAddr != "" {
		if host, portStr, err := net.SplitHostPort(user.RealityHandshakeAddr); err == nil {
			handshakeServer = host
			if p, err := strconv.Atoi(portStr); err == nil {
				handshakePort = p
			}
		}
	}

	shortIDs := []string{""}
	if user.RealityShortID != "" {
		shortIDs = []string{user.RealityShortID}
	}

	return map[string]any{
		"enabled":     true,
		"server_name": handshakeServer,
		"reality": map[string]any{
			"enabled": true,
			"handshake": map[string]any{
				"server":      handshakeServer,
				"server_port": handshakePort,
			},
			"private_key": user.RealityPrivateKey,
			"short_id":    shortIDs,
		},
	}
}


func inboundUser(user users.User) []map[string]any {
	switch protocolOf(user) {
	case "trojan":
		return []map[string]any{{
			"name":     user.Username,
			"password": user.Secret,
		}}
	case "shadowsocks":
		return []map[string]any{{
			"name":     user.Username,
			"password": user.Secret,
		}}
	default:
		return []map[string]any{{
			"uuid": user.UUID,
			"name": user.Username,
		}}
	}
}
