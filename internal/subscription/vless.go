package subscription

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"

	"pulse/internal/users"
)

func Link(user users.User) string {
	switch user.Protocol {
	case "vmess":
		return vmessLink(user)
	case "trojan":
		return trojanLink(user)
	case "shadowsocks":
		return shadowsocksLink(user)
	default:
		return vlessLink(user)
	}
}

func vlessLink(user users.User) string {
	query := url.Values{}
	query.Set("type", "tcp")

	if user.Security == "reality" {
		query.Set("security", "reality")
		query.Set("pbk", user.RealityPublicKey)
		query.Set("sid", user.RealityShortID)
		if user.RealitySpiderX != "" {
			query.Set("spx", user.RealitySpiderX)
		}
		if user.SNI != "" {
			query.Set("sni", user.SNI)
		}
		if user.Fingerprint != "" {
			query.Set("fp", user.Fingerprint)
		}
		if user.Flow != "" {
			query.Set("flow", user.Flow)
		}
	} else {
		query.Set("security", "none")
	}

	u := url.URL{
		Scheme:   "vless",
		User:     url.User(user.UUID),
		Host:     fmt.Sprintf("%s:%d", user.Domain, user.Port),
		RawQuery: query.Encode(),
		Fragment: user.Username,
	}

	return u.String()
}

func trojanLink(user users.User) string {
	query := url.Values{}
	query.Set("type", "ws")
	query.Set("path", "/ws")
	query.Set("security", "tls")
	sni := user.SNI
	if sni == "" {
		sni = user.Domain
	}
	query.Set("sni", sni)
	if user.Fingerprint != "" {
		query.Set("fp", user.Fingerprint)
	}

	u := url.URL{
		Scheme:   "trojan",
		User:     url.User(user.Secret),
		Host:     fmt.Sprintf("%s:%d", user.Domain, user.Port),
		RawQuery: query.Encode(),
		Fragment: user.Username,
	}

	return u.String()
}

// vmessLink 生成标准 vmess:// 链接（v2 JSON base64 格式）。
func vmessLink(user users.User) string {
	obj := map[string]any{
		"v":    "2",
		"ps":   user.Username,
		"add":  user.Domain,
		"port": fmt.Sprintf("%d", user.Port),
		"id":   user.UUID,
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

func shadowsocksLink(user users.User) string {
	credentials := fmt.Sprintf("%s:%s", user.Method, user.Secret)
	encoded := base64.RawURLEncoding.EncodeToString([]byte(credentials))
	return fmt.Sprintf("ss://%s@%s:%d#%s", encoded, user.Domain, user.Port, url.QueryEscape(user.Username))
}
