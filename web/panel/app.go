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
}

// userInbound 代表用户与节点+协议的绑定关系。
type userInbound struct {
	ID                   string `json:"id"`
	UserID               string `json:"user_id"`
	NodeID               string `json:"node_id"`
	Protocol             string `json:"protocol"`
	UUID                 string `json:"uuid"`
	Secret               string `json:"secret"`
	Method               string `json:"method"`
	Security             string `json:"security"`
	Flow                 string `json:"flow"`
	SNI                  string `json:"sni"`
	Fingerprint          string `json:"fingerprint"`
	RealityPublicKey     string `json:"reality_public_key"`
	RealityShortID       string `json:"reality_short_id"`
	RealitySpiderX       string `json:"reality_spider_x"`
	RealityPrivateKey    string `json:"reality_private_key"`
	RealityHandshakeAddr string `json:"reality_handshake_addr"`
	Domain               string `json:"domain"`
	Port                 int    `json:"port"`
	InboundTag           string `json:"inbound_tag"`
	ApplyCount           int    `json:"apply_count"`
	LastAppliedAt        string `json:"last_applied_at"`
	CreatedAt            string `json:"created_at"`
}

type app struct {
	document             js.Value
	storage              js.Value
	window               js.Value
	token                string
	route                string
	nodes                []node
	users                []user
	editingUserID        string
	editingInboundUserID string                // 正在为哪个用户添加 inbound
	userInbounds         map[string][]userInbound // 懒加载，key = userID
}

func main() {
	a := &app{
		document:     js.Global().Get("document"),
		storage:      js.Global().Get("localStorage"),
		window:       js.Global().Get("window"),
		userInbounds: make(map[string][]userInbound),
	}
	a.bind()
	a.bootstrap()
	select {}
}

func (a *app) bootstrap() {
	a.setStatus("加载中...")
	a.syncProtocolFields()
	a.token = a.storage.Call("getItem", "pulse_token").String()
	if a.token == "" || a.token == "null" {
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
