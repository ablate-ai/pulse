//go:build js && wasm

package main

import (
	"fmt"
	"net/http"
	"strings"
	"syscall/js"
)

func (a *app) createNode() {
	payload := map[string]any{
		"name":     a.value("node-name"),
		"base_url": a.value("node-url"),
	}
	if err := postJSON("/v1/nodes", payload, nil, a.token); err != nil {
		a.handleAuthError(err)
		a.setStatus("创建节点失败: " + err.Error())
		return
	}
	a.setStatus("节点已保存")
	a.reset("node-form")
	a.loadNodes()
}

func (a *app) loadNodes() {
	var resp struct {
		Nodes []node `json:"nodes"`
	}
	if err := getJSON("/v1/nodes", &resp, a.token); err != nil {
		a.handleAuthError(err)
		a.setStatus("加载节点失败: " + err.Error())
		return
	}

	a.nodes = resp.Nodes
	container := a.byID("nodes")
	selectEl := a.byID("user-node")
	container.Set("innerHTML", "")
	selectEl.Set("innerHTML", `<option value="">先选择节点</option>`)

	if len(resp.Nodes) == 0 {
		container.Set("textContent", "暂无节点")
		container.Get("classList").Call("add", "empty")
		a.setStatus("没有节点，先创建节点")
		return
	}

	container.Get("classList").Call("remove", "empty")
	for _, n := range resp.Nodes {
		card := fmt.Sprintf(
			`<article class="card">
				<div class="card-id">ID %s</div>
				<h3>%s</h3>
				<p class="meta">%s</p>
				<div class="actions">
					<button data-action="runtime" data-id="%s">查看 Runtime</button>
					<button data-action="logs" data-id="%s">查看 Logs</button>
					<button data-action="delete-node" data-id="%s" class="ghost">删除</button>
				</div>
				<div id="node-logs-%s" class="detail-box" hidden></div>
			 </article>`, escape(n.ID), escape(n.Name), escape(n.BaseURL), escape(n.ID), escape(n.ID), escape(n.ID), escape(n.ID))
		container.Set("innerHTML", container.Get("innerHTML").String()+card)
		option := a.document.Call("createElement", "option")
		option.Set("value", n.ID)
		option.Set("textContent", n.Name+" ("+n.ID+")")
		selectEl.Call("appendChild", option)
	}
	a.bindNodeButtons()
	a.setStatus(fmt.Sprintf("已加载 %d 个节点", len(resp.Nodes)))
}

func (a *app) bindNodeButtons() {
	buttons := a.document.Call("querySelectorAll", "[data-action='runtime'], [data-action='logs'], [data-action='delete-node']")
	length := buttons.Get("length").Int()
	for i := 0; i < length; i++ {
		button := buttons.Index(i)
		button.Call("addEventListener", "click", js.FuncOf(func(this js.Value, args []js.Value) any {
			action := this.Get("dataset").Get("action").String()
			id := this.Get("dataset").Get("id").String()
			go func() {
				switch action {
				case "runtime":
					var runtime struct {
						Version string `json:"version"`
						Module  string `json:"module"`
					}
					if err := getJSON("/v1/nodes/"+id+"/runtime", &runtime, a.token); err != nil {
						a.handleAuthError(err)
						a.setStatus("读取节点 runtime 失败: " + err.Error())
						return
					}
					var usage struct {
						Available     bool  `json:"available"`
						UploadTotal   int64 `json:"upload_total"`
						DownloadTotal int64 `json:"download_total"`
						Connections   int   `json:"connections"`
					}
					if err := getJSON("/v1/nodes/"+id+"/runtime/usage", &usage, a.token); err != nil {
						a.handleAuthError(err)
						a.setStatus("节点 " + id + " runtime: " + runtime.Module + " " + runtime.Version)
						return
					}
					a.setStatus(fmt.Sprintf(
						"节点 %s runtime: %s %s · 上行 %dB · 下行 %dB · 活跃连接 %d",
						id,
						runtime.Module,
						runtime.Version,
						usage.UploadTotal,
						usage.DownloadTotal,
						usage.Connections,
					))
				case "logs":
					var resp struct {
						Logs []string `json:"logs"`
					}
					if err := getJSON("/v1/nodes/"+id+"/runtime/logs", &resp, a.token); err != nil {
						a.handleAuthError(err)
						a.setStatus("读取节点日志失败: " + err.Error())
						return
					}
					box := a.byID("node-logs-" + id)
					box.Set("hidden", false)
					if len(resp.Logs) == 0 {
						box.Set("textContent", "暂无日志")
						a.setStatus("节点日志为空: " + id)
						return
					}
					box.Set("textContent", strings.Join(resp.Logs, "\n"))
					a.setStatus(fmt.Sprintf("已加载节点 %s 最近 %d 条日志", id, len(resp.Logs)))
				case "delete-node":
					if err := doRequest(http.MethodDelete, "/v1/nodes/"+id, nil, nil, a.token); err != nil {
						a.handleAuthError(err)
						a.setStatus("删除节点失败: " + err.Error())
						return
					}
					a.setStatus("节点已删除: " + id)
					a.loadNodes()
				}
			}()
			return nil
		}))
	}
}
