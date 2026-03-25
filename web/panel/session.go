//go:build js && wasm

package main

import (
	"strings"
	"syscall/js"
)

// ─── Cookie 工具 ──────────────────────────────────────────────────────────────

func setCookie(name, value string) {
	js.Global().Get("document").Set("cookie",
		name+"="+value+"; path=/; SameSite=Strict")
}

func getCookie(name string) string {
	all := js.Global().Get("document").Get("cookie").String()
	for _, part := range strings.Split(all, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) == 2 && kv[0] == name {
			return kv[1]
		}
	}
	return ""
}

func deleteCookie(name string) {
	js.Global().Get("document").Set("cookie",
		name+"=; path=/; expires=Thu, 01 Jan 1970 00:00:00 GMT")
}

// ─── 登录 / 登出 / Session 检查 ───────────────────────────────────────────────

func (a *app) login() {
	var resp struct {
		Token string `json:"token"`
	}
	err := postJSON("/v1/auth/login", map[string]any{
		"username": a.value("login-username"),
		"password": a.value("login-password"),
	}, &resp, "")
	if err != nil {
		a.setStatus("登录失败: " + err.Error())
		return
	}
	a.token = resp.Token
	setCookie("pulse_token", resp.Token)
	a.setAuthenticated(true)
	a.setStatus("登录成功")
	a.reset("login-form")
	a.syncNodeInboundProtocolFields()
	a.loadSystemInfo()
	a.loadNodes()
	a.loadUsers()
}

func (a *app) logout() {
	_ = postJSON("/v1/auth/logout", nil, nil, a.token)
	deleteCookie("pulse_token")
	a.token = ""
	a.setAuthenticated(false)
	a.setStatus("已退出")
	a.setRoute("/overview")
}

func (a *app) checkSession() {
	err := getJSON("/v1/auth/me", nil, a.token)
	if err != nil {
		deleteCookie("pulse_token")
		a.token = ""
		a.setAuthenticated(false)
		a.setStatus("登录已失效，请重新登录")
		return
	}
	a.setAuthenticated(true)
	a.syncNodeInboundProtocolFields()
	a.loadSystemInfo()
	a.loadNodes()
	a.loadUsers()
	a.renderRoute()
}

func (a *app) handleAuthError(err error) {
	if strings.Contains(err.Error(), "unauthorized") {
		deleteCookie("pulse_token")
		a.token = ""
		a.setAuthenticated(false)
	}
}
