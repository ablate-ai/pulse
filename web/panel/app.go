//go:build js && wasm

package main

import (
	"syscall/js"
)

type node struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
}

// user 代表纯身份实体，不含协议/节点配置。
type user struct {
	ID                     string `json:"id"`
	Username               string `json:"username"`
	Status                 string `json:"status"`
	ExpireAt               string `json:"expire_at"`
	DataLimitResetStrategy string `json:"data_limit_reset_strategy"`
	TrafficLimit           int64  `json:"traffic_limit_bytes"`
	UploadBytes            int64  `json:"upload_bytes"`
	DownloadBytes          int64  `json:"download_bytes"`
	UsedBytes              int64  `json:"used_bytes"`
	LastTrafficResetAt     string `json:"last_traffic_reset_at"`
	CreatedAt              string `json:"created_at"`
	SubToken               string `json:"sub_token"`
}

// userInbound 代表用户与节点的访问凭据（协议配置由节点 inbound 决定）。
type userInbound struct {
	ID        string `json:"id"`
	UserID    string `json:"user_id"`
	NodeID    string `json:"node_id"`
	UUID      string `json:"uuid"`
	Secret    string `json:"secret"`
	CreatedAt string `json:"created_at"`
}

// nodeInbound 代表节点上的一个监听入站（协议 + 端口 + TLS）。
type nodeInbound struct {
	ID                   string `json:"id"`
	NodeID               string `json:"node_id"`
	Protocol             string `json:"protocol"`
	Tag                  string `json:"tag"`
	Port                 int    `json:"port"`
	Method               string `json:"method"`
	Security             string `json:"security"`
	RealityPrivateKey    string `json:"reality_private_key"`
	RealityPublicKey     string `json:"reality_public_key"`
	RealityHandshakeAddr string `json:"reality_handshake_addr"`
	RealityShortID       string `json:"reality_short_id"`
}

// host 代表客户端连接模板（地址 + TLS 参数）。
type host struct {
	ID          string `json:"id"`
	InboundID   string `json:"inbound_id"`
	Remark      string `json:"remark"`
	Address     string `json:"address"`
	Port        int    `json:"port"`
	SNI         string `json:"sni"`
	Security    string `json:"security"`
	Fingerprint string `json:"fingerprint"`
}

type app struct {
	document             js.Value
	window               js.Value
	token                string
	route                string
	nodes                []node
	users                []user
	editingUserID        string
	editingInboundUserID string                   // 正在为哪个用户添加 inbound
	editingNodeID        string                   // 正在管理入站的节点 ID
	userInbounds         map[string][]userInbound // 懒加载，key = userID
	nodeInbounds         map[string][]nodeInbound // 懒加载，key = nodeID
}

func main() {
	a := &app{
		document:     js.Global().Get("document"),
		window:       js.Global().Get("window"),
		userInbounds: make(map[string][]userInbound),
		nodeInbounds: make(map[string][]nodeInbound),
	}
	a.bind()
	a.bootstrap()
	select {}
}

func (a *app) bootstrap() {
	a.setStatus("加载中...")
	a.token = getCookie("pulse_token")
	if a.token == "" {
		a.setAuthenticated(false)
		a.setStatus("请登录")
		return
	}
	go a.checkSession()
}

func (a *app) byID(id string) js.Value {
	return a.document.Call("getElementById", id)
}

func (a *app) value(id string) string {
	return a.byID(id).Get("value").String()
}

func (a *app) reset(id string) {
	a.byID(id).Call("reset")
}

func (a *app) setStatus(message string) {
	a.byID("status").Set("textContent", message)
}

func (a *app) setAuthenticated(ok bool) {
	a.byID("auth-panel").Set("hidden", ok)
	a.byID("quick-panel").Set("hidden", !ok)
	a.byID("app-nav").Set("hidden", !ok)
	a.byID("app-panel").Set("hidden", !ok)
	if ok {
		a.renderRoute()
	}
}
