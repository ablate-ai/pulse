//go:build js && wasm

package main

import "strings"

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
	a.storage.Call("setItem", "pulse_token", resp.Token)
	a.setAuthenticated(true)
	a.setStatus("登录成功")
	a.reset("login-form")
	a.syncProtocolFields()
	a.loadSystemInfo()
	a.loadNodes()
	a.loadUsers()
}

func (a *app) logout() {
	_ = postJSON("/v1/auth/logout", nil, nil, a.token)
	a.storage.Call("removeItem", "pulse_token")
	a.token = ""
	a.setAuthenticated(false)
	a.setStatus("已退出")
	a.setRoute("/overview")
}

func (a *app) checkSession() {
	err := getJSON("/v1/auth/me", nil, a.token)
	if err != nil {
		a.storage.Call("removeItem", "pulse_token")
		a.token = ""
		a.setAuthenticated(false)
		a.setStatus("登录已失效，请重新登录")
		return
	}
	a.setAuthenticated(true)
	a.syncProtocolFields()
	a.loadSystemInfo()
	a.loadNodes()
	a.loadUsers()
	a.renderRoute()
}

func (a *app) handleAuthError(err error) {
	if strings.Contains(err.Error(), "unauthorized") {
		a.storage.Call("removeItem", "pulse_token")
		a.token = ""
		a.setAuthenticated(false)
	}
}
