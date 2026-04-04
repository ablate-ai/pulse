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
	"pulse/internal/routerules"
	"pulse/internal/users"
)

type singboxConfig struct {
	Log          map[string]any     `json:"log"`
	Inbounds     []inboundBlock     `json:"inbounds"`
	Outbounds    []map[string]any   `json:"outbounds"`
	Route        *routeBlock        `json:"route,omitempty"`
	Experimental *experimentalBlock `json:"experimental,omitempty"`
}

type experimentalBlock struct {
	V2RayAPI *v2rayAPIBlock `json:"v2ray_api,omitempty"`
}

type v2rayAPIBlock struct {
	Listen string      `json:"listen"`
	Stats  *v2rayStats `json:"stats,omitempty"`
}

type v2rayStats struct {
	Enabled bool     `json:"enabled"`
	Users   []string `json:"users"`
}

type routeBlock struct {
	Rules   []routeRule    `json:"rules"`
	RuleSet []ruleSetBlock `json:"rule_set,omitempty"`
	Final   string         `json:"final"`
}

// ruleSetBlock 对应 sing-box route.rule_set 数组元素。
type ruleSetBlock struct {
	Type   string `json:"type"`   // 固定 "remote"
	Tag    string `json:"tag"`
	Format string `json:"format"` // "binary" 或 "source"
	URL    string `json:"url"`
}

type routeRule struct {
	Inbound       []string `json:"inbound,omitempty"`
	DomainSuffix  []string `json:"domain_suffix,omitempty"`
	DomainKeyword []string `json:"domain_keyword,omitempty"`
	Domain        []string `json:"domain,omitempty"`
	IPCIDR        []string `json:"ip_cidr,omitempty"`
	RuleSet       []string `json:"rule_set,omitempty"`
	Outbound      string   `json:"outbound"`
}

type inboundBlock struct {
	Type       string           `json:"type"`
	Tag        string           `json:"tag"`
	Listen     string           `json:"listen"`
	ListenPort int              `json:"listen_port"`
	Users      []map[string]any `json:"users,omitempty"`
	Transport  map[string]any   `json:"transport,omitempty"`
	TLS        map[string]any   `json:"tls,omitempty"`
	Method     string           `json:"method,omitempty"`
	Password   string           `json:"password,omitempty"`
}

// ProbeInboundPort 探针专用内部混合入站端口，仅监听 127.0.0.1，
// 供 pulse-node 解锁检测时将流量路由经过 sing-box 分流规则。
const ProbeInboundPort = 16799

// BuildOptions 控制 BuildSingboxConfig 的可选行为。
type BuildOptions struct {
	// OutboundMap 出口 ID → Outbound，用于 inbound 路由绑定。
	OutboundMap map[string]outbounds.Outbound
	// RouteRules 全局分流规则，按 Priority 升序匹配，优先于 inbound 绑定规则。
	RouteRules []routerules.RouteRule
	// NodeID 当前节点 ID，用于过滤 NodeIDs 非空的分流规则。
	NodeID string
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

		if ib.Protocol == "trojan" {
			listenAddr = "127.0.0.1"
		}

		method := ""
		password := ""
		// 过滤出被分配到此 inbound 的用户凭据
		// 若 InboundID 为空（旧版数据），则对所有 inbound 生效（向后兼容）
		ibAccesses := make([]users.UserInbound, 0, len(activeAccesses))
		for _, acc := range activeAccesses {
			if acc.InboundID == "" || acc.InboundID == ib.ID {
				ibAccesses = append(ibAccesses, acc)
			}
		}

		var userList []map[string]any
		if ib.Protocol == "shadowsocks" {
			method = ib.Method
			if method == "" {
				method = "2022-blake3-aes-128-gcm"
			}
			if strings.HasPrefix(method, "2022-") {
				// SS 2022 多用户：服务端 PSK + 每人独立密码
				password = ib.Password
				userList = make([]map[string]any, 0, len(ibAccesses))
				for _, acc := range ibAccesses {
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
			userList = make([]map[string]any, 0, len(ibAccesses))
			for _, acc := range ibAccesses {
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

	// 注入探针专用入站：仅 127.0.0.1 监听，流量经过 sing-box 路由规则，
	// 使 pulse-node 解锁检测能反映代理后的解锁效果。
	blocks = append(blocks, inboundBlock{
		Type:       "mixed",
		Tag:        "pulse-probe",
		Listen:     "127.0.0.1",
		ListenPort: ProbeInboundPort,
	})

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

	// ensureOutbound 确保出口已加入列表，返回其 tag。
	ensureOutbound := func(obID string) string {
		if obID == "" {
			return "direct"
		}
		ob, ok := opts.OutboundMap[obID]
		if !ok {
			return "direct"
		}
		obTag := "out-" + ob.ID
		if _, seen := seenOutboundIDs[ob.ID]; !seen {
			seenOutboundIDs[ob.ID] = struct{}{}
			outboundList = append(outboundList, buildOutboundBlock(ob, obTag))
		}
		return obTag
	}

	// 全局分流规则（优先级高，先匹配）
	seenRuleSetTags := make(map[string]struct{})
	var ruleSetBlocks []ruleSetBlock
	for _, rr := range opts.RouteRules {
		// NodeIDs 非空时，检查当前节点是否在列表中
		if rr.NodeIDs != "" && opts.NodeID != "" {
			allowed := false
			for _, nid := range strings.Split(rr.NodeIDs, ",") {
				if strings.TrimSpace(nid) == opts.NodeID {
					allowed = true
					break
				}
			}
			if !allowed {
				continue
			}
		}
		obTag := ensureOutbound(rr.OutboundID)
		rule := routeRule{Outbound: obTag}

		if rr.RuleType == "rule_set" {
			// patterns 为单个 tag 名称，URL 直接存在 RuleSetURL 字段
			tag := strings.TrimSpace(rr.Patterns)
			if tag == "" || rr.RuleSetURL == "" {
				continue
			}
			if _, seen := seenRuleSetTags[tag]; !seen {
				seenRuleSetTags[tag] = struct{}{}
				format := rr.RuleSetFormat
				if format == "" {
					format = "source"
				}
				ruleSetBlocks = append(ruleSetBlocks, ruleSetBlock{
					Type:   "remote",
					Tag:    tag,
					Format: format,
					URL:    rr.RuleSetURL,
				})
			}
			rule.RuleSet = []string{tag}
		} else {
			patterns := splitPatterns(rr.Patterns)
			if len(patterns) == 0 {
				continue
			}
			switch rr.RuleType {
			case "domain_suffix":
				rule.DomainSuffix = patterns
			case "domain_keyword":
				rule.DomainKeyword = patterns
			case "domain":
				rule.Domain = patterns
			case "ip_cidr":
				rule.IPCIDR = patterns
			default:
				continue
			}
		}
		rules = append(rules, rule)
	}

	// inbound 绑定出口规则
	for _, ib := range nodeInbounds {
		if ib.OutboundID == "" {
			continue
		}
		obTag := ensureOutbound(ib.OutboundID)
		if obTag == "direct" {
			continue
		}
		ibTag := ib.Tag
		if ibTag == "" {
			ibTag = fmt.Sprintf("pulse-%s-%d", ib.Protocol, ib.Port)
		}
		rules = append(rules, routeRule{
			Inbound:  []string{ibTag},
			Outbound: obTag,
		})
	}

	// 收集去重后的活跃用户名，用于 V2Ray Stats 流量统计
	seenUsers := make(map[string]struct{})
	var statUsers []string
	for _, acc := range activeAccesses {
		u, ok := userMap[acc.UserID]
		if !ok {
			continue
		}
		if _, dup := seenUsers[u.Username]; !dup {
			seenUsers[u.Username] = struct{}{}
			statUsers = append(statUsers, u.Username)
		}
	}
	sort.Strings(statUsers)

	cfg := singboxConfig{
		Log: map[string]any{
			"level": "warn",
		},
		Inbounds:  blocks,
		Outbounds: outboundList,
		Experimental: &experimentalBlock{
			V2RayAPI: &v2rayAPIBlock{
				Listen: "127.0.0.1:0",
				Stats: &v2rayStats{
					Enabled: true,
					Users:   statUsers,
				},
			},
		},
	}
	if len(rules) > 0 || len(ruleSetBlocks) > 0 {
		cfg.Route = &routeBlock{Rules: rules, RuleSet: ruleSetBlocks, Final: "direct"}
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
	default:
		return map[string]any{"type": "direct", "tag": tag}
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
	if protocol == "trojan" {
		return map[string]any{"type": "ws", "path": "/ws"}
	}
	return nil
}

// tlsForInbound 根据节点 inbound 配置选择 TLS 设置。
// tlsForInbound 根据节点 inbound 配置选择 TLS 设置。
// Trojan 始终由 Caddy 终止 TLS，此处返回 nil。
func tlsForInbound(ib inbounds.Inbound, opts BuildOptions) map[string]any {
	if ib.Protocol == "vless" {
		return realityTLSFor(ib)
	}
	return nil
}

// splitPatterns 将逗号或换行分隔的字符串拆分为去空白的非空列表。
func splitPatterns(s string) []string {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' })
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			result = append(result, t)
		}
	}
	return result
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

