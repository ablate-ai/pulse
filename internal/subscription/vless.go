package subscription

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"

	"pulse/internal/users"
)

// Link 根据 UserInbound 和对应的 User 生成订阅链接。
func Link(inbound users.UserInbound, user users.User) string {
	switch inbound.Protocol {
	case "vmess":
		return vmessLink(inbound, user.Username)
	case "trojan":
		return trojanLink(inbound, user.Username)
	case "shadowsocks":
		return shadowsocksLink(inbound, user.Username)
	default:
		return vlessLink(inbound, user.Username)
	}
}

func vlessLink(ib users.UserInbound, username string) string {
	query := url.Values{}
	query.Set("type", "tcp")

	if ib.Security == "reality" {
		query.Set("security", "reality")
		query.Set("pbk", ib.RealityPublicKey)
		query.Set("sid", ib.RealityShortID)
		if ib.RealitySpiderX != "" {
			query.Set("spx", ib.RealitySpiderX)
		}
		if ib.SNI != "" {
			query.Set("sni", ib.SNI)
		}
		if ib.Fingerprint != "" {
			query.Set("fp", ib.Fingerprint)
		}
		if ib.Flow != "" {
			query.Set("flow", ib.Flow)
		}
	} else {
		query.Set("security", "none")
	}

	u := url.URL{
		Scheme:   "vless",
		User:     url.User(ib.UUID),
		Host:     fmt.Sprintf("%s:%d", ib.Domain, ib.Port),
		RawQuery: query.Encode(),
		Fragment: username,
	}

	return u.String()
}

func trojanLink(ib users.UserInbound, username string) string {
	query := url.Values{}
	query.Set("type", "ws")
	query.Set("path", "/ws")
	query.Set("security", "tls")
	sni := ib.SNI
	if sni == "" {
		sni = ib.Domain
	}
	query.Set("sni", sni)
	if ib.Fingerprint != "" {
		query.Set("fp", ib.Fingerprint)
	}

	u := url.URL{
		Scheme:   "trojan",
		User:     url.User(ib.Secret),
		Host:     fmt.Sprintf("%s:%d", ib.Domain, ib.Port),
		RawQuery: query.Encode(),
		Fragment: username,
	}

	return u.String()
}

// vmessLink 生成标准 vmess:// 链接（v2 JSON base64 格式）。
func vmessLink(ib users.UserInbound, username string) string {
	obj := map[string]any{
		"v":    "2",
		"ps":   username,
		"add":  ib.Domain,
		"port": fmt.Sprintf("%d", ib.Port),
		"id":   ib.UUID,
		"aid":  "0",
		"net":  "tcp",
		"type": "none",
		"host": "",
		"path": "",
		"tls":  "",
	}
	data, err := json.Marshal(obj)
	if err != nil {
		return ""
	}
	return "vmess://" + base64.StdEncoding.EncodeToString(data)
}

func shadowsocksLink(ib users.UserInbound, username string) string {
	credentials := fmt.Sprintf("%s:%s", ib.Method, ib.Secret)
	encoded := base64.RawURLEncoding.EncodeToString([]byte(credentials))
	return fmt.Sprintf("ss://%s@%s:%d#%s", encoded, ib.Domain, ib.Port, url.QueryEscape(username))
}
