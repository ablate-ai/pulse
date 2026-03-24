//go:build js && wasm

package main

import "strings"

func (a *app) currentRoute() string {
	path := strings.TrimSpace(a.window.Get("location").Get("pathname").String())
	if path == "" || path == "/" {
		return "/overview"
	}
	switch path {
	case "/overview", "/nodes", "/users":
		return path
	default:
		return "/overview"
	}
}

func (a *app) setRoute(route string) {
	if route == "" {
		route = "/overview"
	}
	a.window.Get("history").Call("pushState", nil, "", route)
	a.renderRoute()
}

func (a *app) renderRoute() {
	route := a.currentRoute()
	a.route = route
	a.showRoute("route-overview", route == "/overview")
	a.showRoute("route-nodes", route == "/nodes")
	a.showRoute("route-users", route == "/users")

	title := "Overview"
	switch route {
	case "/nodes":
		title = "Nodes"
	case "/users":
		title = "Users"
	}
	a.byID("page-title").Set("textContent", title)

	links := a.document.Call("querySelectorAll", "[data-route]")
	length := links.Get("length").Int()
	for i := 0; i < length; i++ {
		link := links.Index(i)
		active := link.Get("dataset").Get("route").String() == route
		if active {
			link.Get("classList").Call("add", "active")
		} else {
			link.Get("classList").Call("remove", "active")
		}
	}
}

func (a *app) showRoute(id string, visible bool) {
	a.byID(id).Set("hidden", !visible)
}
