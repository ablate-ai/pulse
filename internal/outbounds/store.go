package outbounds

import "errors"

var ErrOutboundNotFound = errors.New("outbound not found")

// Outbound 表示一个独立的出口代理配置。
// Protocol 可选值：socks5 / http / ss / vless
type Outbound struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"` // socks5 / http / ss / vless
	Server   string `json:"server"`   // host:port

	// socks5 / http 认证（可选）
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`

	// ss 专用
	Method string `json:"method,omitempty"` // 加密方式，如 2022-blake3-aes-128-gcm

	// vless+reality 专用
	UUID        string `json:"uuid,omitempty"`
	SNI         string `json:"sni,omitempty"`
	PublicKey   string `json:"public_key,omitempty"`
	ShortID     string `json:"short_id,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"` // utls 指纹，默认 chrome
}

type Store interface {
	Upsert(outbound Outbound) (Outbound, error)
	Get(id string) (Outbound, error)
	List() ([]Outbound, error)
	Delete(id string) error
}
