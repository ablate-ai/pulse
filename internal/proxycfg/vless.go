package proxycfg

import (
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"

	"pulse/internal/inbounds"
	"pulse/internal/outbounds"
	"pulse/internal/users"
)

type singboxConfig struct {
	Log       map[string]any   `json:"log"`
	Inbounds  []inboundBlock   `json:"inbounds"`
	Outbounds []map[string]any `json:"outbounds"`
	Route     *routeBlock      `json:"route,omitempty"`
}

type routeBlock struct {
	Rules []routeRule `json:"rules"`
	Final string      `json:"final"`
}

type routeRule struct {
	Inbound  []string `json:"inbound"`
	Outbound string   `json:"outbound"`
}

type inboundBlock struct {
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
	// CaddyEnabled=true 时，Trojan inbound 改为 127.0.0.1 监听 + WS 传输，
	// 由外部 Caddy 终止 TLS 并反代。false = 直连模式（sing-box 自管 TLS）。
	CaddyEnabled bool
	// OutboundMap 出口 ID → Outbound，用于 inbound 路由绑定。
	OutboundMap map[string]outbounds.Outbound
}

// BuildSingboxConfig 根据节点 inbound 配置和用户凭据生成 sing-box 配置 JSON。
// nodeInbounds 是节点上定义的 inbound 列表（协议/端口/TLS 等服务端配置）。
// userAccesses 是有访问权限的用户凭据（UUID/Secret），每条对应一个 (user, node) 对。
// 只有 userMap 中对应用户 EffectiveEnabled() 为 true 的用户才会被写入配置。
// idleConfig 序列化一次后复用，内容固定无需每次重新生成。
var idleConfig = func() string {
	b, _ := json.Marshal(singboxConfig{
		Log:       map[string]any{"level": "warn", "output": "stderr"},
		Inbounds:  []inboundBlock{},
		Outbounds: []map[string]any{{"type": "direct", "tag": "direct"}},
	})
	return string(b)
}()

// BuildIdleConfig 返回无 inbound 的最小 sing-box 配置，保持进程存活用。
func BuildIdleConfig() string { return idleConfig }

func BuildSingboxConfig(nodeInbounds []inbounds.Inbound, userAccesses []users.UserInbound, userMap map[string]users.User, opts BuildOptions) (string, error) {
	if len(nodeInbounds) == 0 {
		return "", fmt.Errorf("at least one inbound is required")
	}

	// 过滤出已启用的用户访问记录
	activeAccesses := make([]users.UserInbound, 0, len(userAccesses))
	for _, acc := range userAccesses {
		u, ok := userMap[acc.UserID]
		if ok && u.EffectiveEnabled() {
			activeAccesses = append(activeAccesses, acc)
		}
	}
	if len(activeAccesses) == 0 {
		return "", fmt.Errorf("at least one active user is required")
	}

	blocks := make([]inboundBlock, 0, len(nodeInbounds))
	for _, ib := range nodeInbounds {
		tag := ib.Tag
		if tag == "" {
			tag = fmt.Sprintf("pulse-%s-%d", ib.Protocol, ib.Port)
		}

		listenAddr := "::"
		listenPort := ib.Port

		if opts.CaddyEnabled && ib.Protocol == "trojan" {
			listenAddr = "127.0.0.1"
		}

		method := ""
		password := ""
		var userList []map[string]any
		if ib.Protocol == "shadowsocks" {
			method = ib.Method
			if method == "" {
				method = "2022-blake3-aes-128-gcm"
			}
			if strings.HasPrefix(method, "2022-") {
				// SS 2022 多用户：服务端 PSK + 每人独立密码
				password = ib.Password
				userList = make([]map[string]any, 0, len(activeAccesses))
				for _, acc := range activeAccesses {
					u, ok := userMap[acc.UserID]
					if !ok {
						continue
					}
					userList = append(userList, map[string]any{
						"name":     u.Username,
						"password": acc.Secret,
					})
				}
			} else {
				// 旧版 SS：单一共享密码
				password = "pulse-shared-secret"
				userList = nil
			}
		} else {
			userList = make([]map[string]any, 0, len(activeAccesses))
			for _, acc := range activeAccesses {
				u, ok := userMap[acc.UserID]
				if !ok {
					continue
				}
				userList = append(userList, buildInboundUser(ib, acc, u.Username))
			}
		}

		blocks = append(blocks, inboundBlock{
			Type:       ib.Protocol,
			Tag:        tag,
			Listen:     listenAddr,
			ListenPort: listenPort,
			Users:      userList,
			Transport:  transportFor(ib.Protocol, opts),
			TLS:        tlsForInbound(ib, opts),
			Method:     method,
			Password:   password,
		})
	}

	sort.Slice(blocks, func(i, j int) bool {
		if blocks[i].ListenPort == blocks[j].ListenPort {
			return blocks[i].Tag < blocks[j].Tag
		}
		return blocks[i].ListenPort < blocks[j].ListenPort
	})

	// 构建出口列表和路由规则
	directOut := map[string]any{"type": "direct", "tag": "direct"}
	outboundList := []map[string]any{directOut}
	seenOutboundIDs := make(map[string]struct{})
	var rules []routeRule

	for _, ib := range nodeInbounds {
		if ib.OutboundID == "" {
			continue
		}
		ob, ok := opts.OutboundMap[ib.OutboundID]
		if !ok {
			continue
		}
		obTag := "out-" + ob.ID
		// 若该出口尚未添加，生成 outbound block
		if _, seen := seenOutboundIDs[ob.ID]; !seen {
			seenOutboundIDs[ob.ID] = struct{}{}
			obBlock := buildOutboundBlock(ob, obTag)
			outboundList = append(outboundList, obBlock)
		}
		// 找到 inbound 对应的 tag
		ibTag := ib.Tag
		if ibTag == "" {
			ibTag = fmt.Sprintf("pulse-%s-%d", ib.Protocol, ib.Port)
		}
		rules = append(rules, routeRule{
			Inbound:  []string{ibTag},
			Outbound: obTag,
		})
	}

	cfg := singboxConfig{
		Log: map[string]any{
			"level": "warn",
		},
		Inbounds:  blocks,
		Outbounds: outboundList,
	}
	if len(rules) > 0 {
		cfg.Route = &routeBlock{Rules: rules, Final: "direct"}
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal sing-box config: %w", err)
	}
	return string(data), nil
}

// buildOutboundBlock 根据 Outbound 配置生成 sing-box outbound block。
func buildOutboundBlock(ob outbounds.Outbound, tag string) map[string]any {
	host, portStr, err := net.SplitHostPort(ob.Server)
	if err != nil {
		return map[string]any{"type": "direct", "tag": tag}
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return map[string]any{"type": "direct", "tag": tag}
	}

	switch ob.Protocol {
	case "ss":
		return map[string]any{
			"type":        "shadowsocks",
			"tag":         tag,
			"server":      host,
			"server_port": port,
			"method":      ob.Method,
			"password":    ob.Password,
		}
	case "vless":
		fp := ob.Fingerprint
		if fp == "" {
			fp = "chrome"
		}
		return map[string]any{
			"type":            "vless",
			"tag":             tag,
			"server":          host,
			"server_port":     port,
			"uuid":            ob.UUID,
			"packet_encoding": "xudp",
			"tls": map[string]any{
				"enabled":     true,
				"server_name": ob.SNI,
				"utls": map[string]any{
					"enabled":     true,
					"fingerprint": fp,
				},
				"reality": map[string]any{
					"enabled":    true,
					"public_key": ob.PublicKey,
					"short_id":   ob.ShortID,
				},
			},
		}
	case "http":
		block := map[string]any{
			"type":        "http",
			"tag":         tag,
			"server":      host,
			"server_port": port,
		}
		if ob.Username != "" {
			block["username"] = ob.Username
			block["password"] = ob.Password
		}
		return block
	default: // socks5
		block := map[string]any{
			"type":        "socks",
			"tag":         tag,
			"server":      host,
			"server_port": port,
			"version":     "5",
		}
		if ob.Username != "" {
			block["username"] = ob.Username
			block["password"] = ob.Password
		}
		return block
	}
}

func buildInboundUser(ib inbounds.Inbound, acc users.UserInbound, username string) map[string]any {
	switch ib.Protocol {
	case "trojan", "shadowsocks":
		return map[string]any{
			"name":     username,
			"password": acc.Secret,
		}
	default: // vless, vmess
		user := map[string]any{
			"uuid": acc.UUID,
			"name": username,
		}
		if ib.Protocol == "vless" && ib.Security == "reality" {
			user["flow"] = "xtls-rprx-vision"
		}
		return user
	}
}

func transportFor(protocol string, opts BuildOptions) map[string]any {
	if opts.CaddyEnabled && protocol == "trojan" {
		return map[string]any{"type": "ws", "path": "/ws"}
	}
	return nil
}

// tlsForInbound 根据节点 inbound 配置选择 TLS 设置。
func tlsForInbound(ib inbounds.Inbound, opts BuildOptions) map[string]any {
	switch ib.Protocol {
	case "trojan":
		if opts.CaddyEnabled {
			return nil // TLS 由 Caddy 终止
		}
		return trojanTLSFor(ib)
	case "vless":
		return realityTLSFor(ib)
	default: // shadowsocks、vmess 不支持 tls 字段
		return nil
	}
}

func trojanTLSFor(ib inbounds.Inbound) map[string]any {
	if ib.TLSCertPath == "" || ib.TLSKeyPath == "" {
		return nil
	}
	return map[string]any{
		"enabled":          true,
		"certificate_path": ib.TLSCertPath,
		"key_path":         ib.TLSKeyPath,
	}
}

func realityTLSFor(ib inbounds.Inbound) map[string]any {
	if ib.Security != "reality" || ib.RealityPrivateKey == "" {
		return nil
	}

	handshakeServer := "www.google.com"
	handshakePort := 443
	if ib.RealityHandshakeAddr != "" {
		if host, portStr, err := net.SplitHostPort(ib.RealityHandshakeAddr); err == nil {
			handshakeServer = host
			if p, err := strconv.Atoi(portStr); err == nil {
				handshakePort = p
			}
		}
	}

	shortIDs := []string{""}
	if ib.RealityShortID != "" {
		shortIDs = []string{ib.RealityShortID}
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
			"private_key": ib.RealityPrivateKey,
			"short_id":    shortIDs,
		},
	}
}

