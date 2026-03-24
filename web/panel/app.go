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

type user struct {
	ID            string `json:"id"`
	Username      string `json:"username"`
	Protocol      string `json:"protocol"`
	Secret        string `json:"secret"`
	Method        string `json:"method"`
	Enabled       bool   `json:"enabled"`
	NodeID        string `json:"node_id"`
	Domain        string `json:"domain"`
	Port          int    `json:"port"`
	TrafficLimit  int64  `json:"traffic_limit_bytes"`
	UploadBytes   int64  `json:"upload_bytes"`
	DownloadBytes int64  `json:"download_bytes"`
	UsedBytes     int64  `json:"used_bytes"`
	ApplyCount    int    `json:"apply_count"`
	LastAppliedAt string `json:"last_applied_at"`
}

type app struct {
	document js.Value
	storage  js.Value
	window   js.Value
	token    string
	route    string
	nodes    []node
	users    []user
}

func main() {
	app := &app{
		document: js.Global().Get("document"),
		storage:  js.Global().Get("localStorage"),
		window:   js.Global().Get("window"),
	}
	app.bind()
	app.bootstrap()
	select {}
}

func (a *app) bootstrap() {
	a.setStatus("加载中...")
	a.syncProtocolFields()
	a.token = a.storage.Call("getItem", "pulse_token").String()
	if a.token == "" {
		a.setAuthenticated(false)
		a.setStatus("请先登录")
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
