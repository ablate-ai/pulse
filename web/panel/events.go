//go:build js && wasm

package main

import "syscall/js"

func (a *app) bind() {
	a.byID("login-form").Call("addEventListener", "submit", js.FuncOf(func(this js.Value, args []js.Value) any {
		args[0].Call("preventDefault")
		go a.login()
		return nil
	}))

	a.byID("logout").Call("addEventListener", "click", js.FuncOf(func(this js.Value, args []js.Value) any {
		go a.logout()
		return nil
	}))

	a.byID("node-form").Call("addEventListener", "submit", js.FuncOf(func(this js.Value, args []js.Value) any {
		args[0].Call("preventDefault")
		go a.createNode()
		return nil
	}))

	a.byID("user-form").Call("addEventListener", "submit", js.FuncOf(func(this js.Value, args []js.Value) any {
		args[0].Call("preventDefault")
		go a.createUser()
		return nil
	}))

	a.byID("refresh-nodes").Call("addEventListener", "click", js.FuncOf(func(this js.Value, args []js.Value) any {
		go a.loadNodes()
		return nil
	}))

	a.byID("refresh-users").Call("addEventListener", "click", js.FuncOf(func(this js.Value, args []js.Value) any {
		go a.loadUsers()
		return nil
	}))

	a.byID("refresh-system").Call("addEventListener", "click", js.FuncOf(func(this js.Value, args []js.Value) any {
		go a.loadSystemInfo()
		return nil
	}))

	a.byID("sync-usage").Call("addEventListener", "click", js.FuncOf(func(this js.Value, args []js.Value) any {
		go a.syncUsage()
		return nil
	}))

	a.byID("user-protocol").Call("addEventListener", "change", js.FuncOf(func(this js.Value, args []js.Value) any {
		a.syncProtocolFields()
		return nil
	}))

	a.byID("user-search").Call("addEventListener", "input", js.FuncOf(func(this js.Value, args []js.Value) any {
		a.renderUsers()
		return nil
	}))

	a.byID("user-filter-protocol").Call("addEventListener", "change", js.FuncOf(func(this js.Value, args []js.Value) any {
		a.renderUsers()
		return nil
	}))

	links := a.document.Call("querySelectorAll", "[data-route]")
	linkCount := links.Get("length").Int()
	for i := 0; i < linkCount; i++ {
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
