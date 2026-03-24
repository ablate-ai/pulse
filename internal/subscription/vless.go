package subscription

import (
	"encoding/base64"
	"fmt"
	"net/url"

	"pulse/internal/users"
)

func Link(user users.User) string {
	switch user.Protocol {
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
	query.Set("security", "none")

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
	query.Set("type", "tcp")
	query.Set("security", "none")

	u := url.URL{
		Scheme:   "trojan",
		User:     url.User(user.Secret),
		Host:     fmt.Sprintf("%s:%d", user.Domain, user.Port),
		RawQuery: query.Encode(),
		Fragment: user.Username,
	}

	return u.String()
}

func shadowsocksLink(user users.User) string {
	credentials := fmt.Sprintf("%s:%s", user.Method, user.Secret)
	encoded := base64.RawURLEncoding.EncodeToString([]byte(credentials))
	return fmt.Sprintf("ss://%s@%s:%d#%s", encoded, user.Domain, user.Port, url.QueryEscape(user.Username))
}
