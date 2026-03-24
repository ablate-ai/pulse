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
	selectEl.Set("innerHTML", `<option value="">选择节点</option>`)

	if len(resp.Nodes) == 0 {
		container.Set("textContent", "暂无节点")
		container.Get("classList").Call("add", "empty-state")
		a.setStatus("没有节点，先添加节点")
		return
	}

	container.Get("classList").Call("remove", "empty-state")
	var buf strings.Builder
	for _, n := range resp.Nodes {
		buf.WriteString(fmt.Sprintf(`<article class="node-card">
  <div class="node-card-head">
    <div class="node-card-name">%s</div>
    <div id="node-badge-%s" class="node-badge">%s</div>
  </div>
  <div class="node-card-url">%s</div>
  <div class="node-card-actions">
    <button class="btn btn-ghost btn-sm" data-action="node-status" data-id="%s">状态</button>
    <button class="btn btn-ghost btn-sm" data-action="node-start" data-id="%s">启动</button>
    <button class="btn btn-ghost btn-sm" data-action="node-stop" data-id="%s">停止</button>
    <button class="btn btn-ghost btn-sm" data-action="node-restart" data-id="%s">重启</button>
    <button class="btn btn-ghost btn-sm" data-action="node-logs" data-id="%s">日志</button>
    <button class="btn btn-ghost btn-sm btn-danger" data-action="delete-node" data-id="%s">删除</button>
  </div>
  <div id="node-logs-%s" class="detail-box" hidden></div>
</article>`,
			escape(n.Name),
			escape(n.ID), nodeBadge(false),
			escape(n.BaseURL),
			escape(n.ID), escape(n.ID), escape(n.ID), escape(n.ID),
			escape(n.ID), escape(n.ID),
			escape(n.ID),
		))

		option := a.document.Call("createElement", "option")
		option.Set("value", n.ID)
		option.Set("textContent", n.Name)
		selectEl.Call("appendChild", option)
	}
	container.Set("innerHTML", buf.String())
	a.bindNodeButtons()
	a.setStatus(fmt.Sprintf("已加载 %d 个节点", len(resp.Nodes)))

	// 异步刷新各节点运行状态
	for _, n := range resp.Nodes {
		go func(nodeID string) {
			var status struct {
				Running bool `json:"running"`
			}
			if err := getJSON("/v1/nodes/"+nodeID+"/runtime/status", &status, a.token); err != nil {
				return
			}
			badge := a.byID("node-badge-" + nodeID)
			badge.Set("innerHTML", nodeBadge(status.Running))
		}(n.ID)
	}
}

func (a *app) bindNodeButtons() {
	buttons := a.document.Call("querySelectorAll",
		"[data-action='node-status'], [data-action='node-start'], [data-action='node-stop'], [data-action='node-restart'], [data-action='node-logs'], [data-action='delete-node']")
	length := buttons.Get("length").Int()
	for i := 0; i < length; i++ {
		button := buttons.Index(i)
		button.Call("addEventListener", "click", js.FuncOf(func(this js.Value, args []js.Value) any {
			action := this.Get("dataset").Get("action").String()
			id := this.Get("dataset").Get("id").String()
			go func() {
				switch action {
				case "node-status":
					var status struct {
						Running bool `json:"running"`
					}
					if err := getJSON("/v1/nodes/"+id+"/runtime/status", &status, a.token); err != nil {
						a.handleAuthError(err)
						a.setStatus("读取节点状态失败: " + err.Error())
						return
					}
					badge := a.byID("node-badge-" + id)
					badge.Set("innerHTML", nodeBadge(status.Running))
					a.setStatus(fmt.Sprintf("节点 %s 运行状态: %t", id, status.Running))

				case "node-start":
					var startStatus struct {
						Running bool `json:"running"`
					}
					if err := postJSON("/v1/nodes/"+id+"/runtime/apply", nil, &startStatus, a.token); err != nil {
						a.handleAuthError(err)
						a.setStatus("启动节点失败: " + err.Error())
						return
					}
					badge := a.byID("node-badge-" + id)
					badge.Set("innerHTML", nodeBadge(startStatus.Running))
					a.setStatus("节点已下发配置: " + id)

				case "node-stop":
					if err := postJSON("/v1/nodes/"+id+"/runtime/stop", nil, nil, a.token); err != nil {
						a.handleAuthError(err)
						a.setStatus("停止节点失败: " + err.Error())
						return
					}
					badge := a.byID("node-badge-" + id)
					badge.Set("innerHTML", nodeBadge(false))
					a.setStatus("节点已停止: " + id)

				case "node-restart":
					var restartStatus struct {
						Running bool `json:"running"`
					}
					if err := postJSON("/v1/nodes/"+id+"/runtime/apply", nil, &restartStatus, a.token); err != nil {
						a.handleAuthError(err)
						a.setStatus("重启节点失败: " + err.Error())
						return
					}
					badge := a.byID("node-badge-" + id)
					badge.Set("innerHTML", nodeBadge(restartStatus.Running))
					a.setStatus("节点已重新下发配置: " + id)

				case "node-logs":
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
