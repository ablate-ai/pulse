package subscription

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strings"

	"pulse/internal/inbounds"
	"pulse/internal/users"
)

// Link 根据节点 inbound、客户端 host 模板、用户凭据和用户信息生成订阅链接。
func Link(nodeInbound inbounds.Inbound, host inbounds.Host, access users.UserInbound, user users.User) string {
	// 连接地址和端口
	addr := host.Address
	port := host.Port
	if port == 0 {
		port = nodeInbound.Port
	}

	// 节点显示名称：优先用 Host Remark，其次 Inbound Tag，否则用连接地址
	name := host.Remark
	if name == "" {
		name = nodeInbound.Tag
	}
	if name == "" {
		name = addr
	}

	switch nodeInbound.Protocol {
	case "vmess":
		return vmessLink(nodeInbound, host, access, name, addr, port)
	case "trojan":
		return trojanLink(nodeInbound, host, access, name, addr, port)
	case "shadowsocks":
		return shadowsocksLink(nodeInbound, host, access, name, addr, port)
	default: // vless
		return vlessLink(nodeInbound, host, access, name, addr, port)
	}
}

func vlessLink(ib inbounds.Inbound, host inbounds.Host, acc users.UserInbound, username, addr string, port int) string {
	query := url.Values{}
	query.Set("type", "tcp")

	security := host.Security
	if security == "" && ib.Security != "" {
		security = ib.Security
	}

	if security == "reality" {
		pubkey := host.RealityPublicKey
		if pubkey == "" {
			pubkey = ib.RealityPublicKey
		}
		shortID := host.RealityShortID
		if shortID == "" {
			shortID = ib.RealityShortID
		}
		spiderX := host.RealitySpiderX

		query.Set("security", "reality")
		query.Set("pbk", pubkey)
		query.Set("sid", shortID)
		if spiderX != "" {
			query.Set("spx", spiderX)
		}
		sni := host.SNI
		if sni == "" && ib.RealityHandshakeAddr != "" {
			if h, _, err := net.SplitHostPort(ib.RealityHandshakeAddr); err == nil {
				sni = h
			}
		}
		if sni == "" {
			sni = addr
		}
		query.Set("sni", sni)
		fp := host.Fingerprint
		if fp == "" {
			fp = "chrome"
		}
		query.Set("fp", fp)
		query.Set("flow", "xtls-rprx-vision")
	} else {
		query.Set("security", "none")
	}

	u := url.URL{
		Scheme:   "vless",
		User:     url.User(acc.UUID),
		Host:     fmt.Sprintf("%s:%d", addr, port),
		RawQuery: query.Encode(),
		Fragment: username,
	}
	return u.String()
}

func trojanLink(ib inbounds.Inbound, host inbounds.Host, acc users.UserInbound, username, addr string, port int) string {
	query := url.Values{}
	query.Set("type", "ws")
	query.Set("path", "/ws")
	query.Set("security", "tls")
	sni := host.SNI
	if sni == "" {
		sni = addr
	}
	query.Set("sni", sni)
	if host.Fingerprint != "" {
		query.Set("fp", host.Fingerprint)
	}

	u := url.URL{
		Scheme:   "trojan",
		User:     url.User(acc.Secret),
		Host:     fmt.Sprintf("%s:%d", addr, port),
		RawQuery: query.Encode(),
		Fragment: username,
	}
	return u.String()
}

func vmessLink(ib inbounds.Inbound, host inbounds.Host, acc users.UserInbound, username, addr string, port int) string {
	obj := map[string]any{
		"v":    "2",
		"ps":   username,
		"add":  addr,
		"port": fmt.Sprintf("%d", port),
		"id":   acc.UUID,
		"aid":  "0",
		"net":  "tcp",
		"type": "none",
		"host": host.Host,
		"path": host.Path,
		"tls":  host.Security,
	}
	data, err := json.Marshal(obj)
	if err != nil {
		return ""
	}
	return "vmess://" + base64.StdEncoding.EncodeToString(data)
}

func shadowsocksLink(ib inbounds.Inbound, host inbounds.Host, acc users.UserInbound, username, addr string, port int) string {
	method := ib.Method
	if method == "" {
		method = "aes-128-gcm"
	}
	var credentials string
	// SS 2022 系列需要 "method:服务端PSK:用户PSK" 格式
	if strings.HasPrefix(method, "2022-") {
		credentials = fmt.Sprintf("%s:%s:%s", method, ib.Password, acc.Secret)
	} else {
		credentials = fmt.Sprintf("%s:%s", method, acc.Secret)
	}
	encoded := base64.RawURLEncoding.EncodeToString([]byte(credentials))
	return fmt.Sprintf("ss://%s@%s:%d#%s", encoded, addr, port, url.QueryEscape(username))
}
