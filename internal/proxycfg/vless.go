package proxycfg

import (
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strconv"

	"pulse/internal/inbounds"
	"pulse/internal/users"
)

type singboxConfig struct {
	Log       map[string]any   `json:"log"`
	Inbounds  []inboundBlock   `json:"inbounds"`
	Outbounds []map[string]any `json:"outbounds"`
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
	// SingboxWSLocalPort > 0 时，Trojan inbound 改为 WebSocket 模式并监听该本地端口，
	// 由外部 Caddy 统一终止 TLS 并反代过来。0 = 直连模式（sing-box 自管 TLS）。
	SingboxWSLocalPort int
}

// BuildSingboxConfig 根据节点 inbound 配置和用户凭据生成 sing-box 配置 JSON。
// nodeInbounds 是节点上定义的 inbound 列表（协议/端口/TLS 等服务端配置）。
// userAccesses 是有访问权限的用户凭据（UUID/Secret），每条对应一个 (user, node) 对。
// 只有 userMap 中对应用户 EffectiveEnabled() 为 true 的用户才会被写入配置。
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

	// Trojan Caddy WS 模式下，多个 Trojan inbound 合并为一个
	type trojanMergeKey struct{}
	trojanMerged := false

	blocks := make([]inboundBlock, 0, len(nodeInbounds))
	for _, ib := range nodeInbounds {
		tag := ib.Tag
		if tag == "" {
			tag = fmt.Sprintf("pulse-%s-%d", ib.Protocol, ib.Port)
		}

		listenAddr := "::"
		listenPort := ib.Port

		if opts.SingboxWSLocalPort > 0 && ib.Protocol == "trojan" {
			if trojanMerged {
				// 已生成过 trojan WS inbound，跳过
				continue
			}
			listenAddr = "127.0.0.1"
			listenPort = opts.SingboxWSLocalPort
			tag = "pulse-trojan-ws"
			trojanMerged = true
		}

		method := ""
		password := ""
		if ib.Protocol == "shadowsocks" {
			method = ib.Method
			if method == "" {
				method = "aes-128-gcm"
			}
			password = "pulse-shared-secret"
		}

		userList := make([]map[string]any, 0, len(activeAccesses))
		for _, acc := range activeAccesses {
			u, ok := userMap[acc.UserID]
			if !ok {
				continue
			}
			userList = append(userList, buildInboundUser(ib, acc, u.Username))
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

	cfg := singboxConfig{
		Log: map[string]any{
			"level": "warn",
		},
		Inbounds: blocks,
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
	if opts.SingboxWSLocalPort > 0 && protocol == "trojan" {
		return map[string]any{"type": "ws", "path": "/ws"}
	}
	return nil
}

// tlsForInbound 根据节点 inbound 配置选择 TLS 设置。
func tlsForInbound(ib inbounds.Inbound, opts BuildOptions) map[string]any {
	if ib.Protocol == "trojan" {
		if opts.SingboxWSLocalPort > 0 {
			return nil // TLS 由 Caddy 终止
		}
		return trojanTLSFor(ib)
	}
	return realityTLSFor(ib)
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
