//go:build js && wasm

package main

import "syscall/js"

func (a *app) bind() {
	// 登录 / 退出
	a.byID("login-form").Call("addEventListener", "submit", js.FuncOf(func(this js.Value, args []js.Value) any {
		args[0].Call("preventDefault")
		go a.login()
		return nil
	}))

	a.byID("logout").Call("addEventListener", "click", js.FuncOf(func(this js.Value, args []js.Value) any {
		go a.logout()
		return nil
	}))

	// 节点表单
	a.byID("node-form").Call("addEventListener", "submit", js.FuncOf(func(this js.Value, args []js.Value) any {
		args[0].Call("preventDefault")
		go a.createNode()
		return nil
	}))

	// 用户表单
	a.byID("user-form").Call("addEventListener", "submit", js.FuncOf(func(this js.Value, args []js.Value) any {
		args[0].Call("preventDefault")
		go a.createUser()
		return nil
	}))

	// 编辑 Modal 表单
	a.byID("edit-form").Call("addEventListener", "submit", js.FuncOf(func(this js.Value, args []js.Value) any {
		args[0].Call("preventDefault")
		go a.submitEditUser()
		return nil
	}))

	a.byID("edit-modal-close").Call("addEventListener", "click", js.FuncOf(func(this js.Value, args []js.Value) any {
		a.byID("edit-modal").Call("close")
		return nil
	}))

	a.byID("edit-modal-cancel").Call("addEventListener", "click", js.FuncOf(func(this js.Value, args []js.Value) any {
		a.byID("edit-modal").Call("close")
		return nil
	}))


	// 协议切换
	a.byID("user-protocol").Call("addEventListener", "change", js.FuncOf(func(this js.Value, args []js.Value) any {
		a.syncProtocolFields()
		return nil
	}))

	// 生成 Reality 密钥对
	a.byID("btn-gen-keypair").Call("addEventListener", "click", js.FuncOf(func(this js.Value, args []js.Value) any {
		go a.generateRealityKeypair()
		return nil
	}))


	// 用户列表过滤
	for _, id := range []string{"user-search", "user-filter-protocol", "user-filter-status"} {
		id := id
		a.byID(id).Call("addEventListener", "input", js.FuncOf(func(this js.Value, args []js.Value) any {
			a.renderUsers()
			return nil
		}))
		a.byID(id).Call("addEventListener", "change", js.FuncOf(func(this js.Value, args []js.Value) any {
			a.renderUsers()
			return nil
		}))
	}

	// 节点日志 modal
	a.byID("node-logs-modal-close").Call("addEventListener", "click", js.FuncOf(func(this js.Value, args []js.Value) any {
		a.byID("node-logs-modal").Call("close")
		return nil
	}))

	// 节点配置 modal
	a.byID("node-config-modal-close").Call("addEventListener", "click", js.FuncOf(func(this js.Value, args []js.Value) any {
		a.byID("node-config-modal").Call("close")
		return nil
	}))

	// 节点编辑 modal
	a.byID("node-edit-form").Call("addEventListener", "submit", js.FuncOf(func(this js.Value, args []js.Value) any {
		args[0].Call("preventDefault")
		go a.submitEditNode()
		return nil
	}))
	a.byID("node-edit-modal-close").Call("addEventListener", "click", js.FuncOf(func(this js.Value, args []js.Value) any {
		a.byID("node-edit-modal").Call("close")
		return nil
	}))
	a.byID("node-edit-modal-cancel").Call("addEventListener", "click", js.FuncOf(func(this js.Value, args []js.Value) any {
		a.byID("node-edit-modal").Call("close")
		return nil
	}))

	// 路由导航
	links := a.document.Call("querySelectorAll", "[data-route]")
	for i := 0; i < links.Get("length").Int(); i++ {
		link := links.Index(i)
		link.Call("addEventListener", "click", js.FuncOf(func(this js.Value, args []js.Value) any {
			args[0].Call("preventDefault")
			a.setRoute(this.Get("dataset").Get("route").String())
			return nil
		}))
	}

	a.window.Call("addEventListener", "popstate", js.FuncOf(func(this js.Value, args []js.Value) any {
		a.renderRoute()
		return nil
	}))
}
