package outbounds

import "errors"

var ErrOutboundNotFound = errors.New("outbound not found")

// Outbound 表示一个独立的出口代理配置。
type Outbound struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"` // socks5 / http
	Server   string `json:"server"`   // host:port
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

type Store interface {
	Upsert(outbound Outbound) (Outbound, error)
	Get(id string) (Outbound, error)
	List() ([]Outbound, error)
	Delete(id string) error
}
