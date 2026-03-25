//go:build js && wasm

package main

import (
	"fmt"
	"net/http"
	"strings"
	"syscall/js"
)

type nodeUsage struct {
	Running       bool  `json:"running"`
	UploadTotal   int64 `json:"upload_total"`
	DownloadTotal int64 `json:"download_total"`
	Connections   int   `json:"connections"`
}

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
  <div id="node-usage-%s" class="node-card-usage"></div>
  <div class="node-card-actions">
    <button class="btn btn-ghost btn-sm" data-action="apply-node" data-id="%s">下发</button>
    <button class="btn btn-ghost btn-sm" data-action="stop-node"  data-id="%s">停止</button>
    <button class="btn btn-ghost btn-sm" data-action="logs-node"  data-id="%s">日志</button>
    <button class="btn btn-ghost btn-sm" data-action="edit-node"  data-id="%s">编辑</button>
    <button class="btn btn-ghost btn-sm btn-danger" data-action="delete-node" data-id="%s">删除</button>
  </div>
</article>`,
			escape(n.Name),
			escape(n.ID), nodeBadge(false),
			escape(n.BaseURL),
			escape(n.ID),
			escape(n.ID), escape(n.ID), escape(n.ID), escape(n.ID), escape(n.ID),
		))

		option := a.document.Call("createElement", "option")
		option.Set("value", n.ID)
		option.Set("textContent", n.Name)
		selectEl.Call("appendChild", option)
	}
	container.Set("innerHTML", buf.String())
	a.bindNodeButtons()
	a.setStatus(fmt.Sprintf("已加载 %d 个节点", len(resp.Nodes)))

	// 异步刷新各节点运行状态和流量统计
	for _, n := range resp.Nodes {
		go func(nodeID string) {
			var usage nodeUsage
			if err := getJSON("/v1/nodes/"+nodeID+"/runtime/usage", &usage, a.token); err != nil {
				return
			}
			badge := a.byID("node-badge-" + nodeID)
			badge.Set("innerHTML", nodeBadge(usage.Running))
			if usage.Running {
				usageEl := a.byID("node-usage-" + nodeID)
				usageEl.Set("innerHTML", fmt.Sprintf(
					`<span class="node-stat">↑ %s</span><span class="node-stat">↓ %s</span><span class="node-stat">连接 %d</span>`,
					formatBytes(usage.UploadTotal),
					formatBytes(usage.DownloadTotal),
					usage.Connections,
				))
			}
		}(n.ID)
	}
}

func (a *app) bindNodeButtons() {
	actions := map[string]func(string){
		"delete-node": a.deleteNode,
		"apply-node":  a.applyNode,
		"stop-node":   a.stopNode,
		"logs-node":   a.showNodeLogs,
		"edit-node":   a.openEditNodeModal,
	}
	for action, fn := range actions {
		fn := fn
		buttons := a.document.Call("querySelectorAll", fmt.Sprintf("[data-action='%s']", action))
		for i := 0; i < buttons.Get("length").Int(); i++ {
			button := buttons.Index(i)
			button.Call("addEventListener", "click", js.FuncOf(func(this js.Value, args []js.Value) any {
				id := this.Get("dataset").Get("id").String()
				go fn(id)
				return nil
			}))
		}
	}
}

func (a *app) deleteNode(id string) {
	if err := doRequest(http.MethodDelete, "/v1/nodes/"+id, nil, nil, a.token); err != nil {
		a.handleAuthError(err)
		a.setStatus("删除节点失败: " + err.Error())
		return
	}
	a.setStatus("节点已删除")
	a.loadNodes()
}

func (a *app) applyNode(id string) {
	a.setStatus("下发配置中...")
	var status struct {
		Running bool `json:"running"`
	}
	if err := postJSON("/v1/nodes/"+id+"/runtime/apply", map[string]any{}, &status, a.token); err != nil {
		a.handleAuthError(err)
		a.setStatus("下发失败: " + err.Error())
		return
	}
	badge := a.byID("node-badge-" + id)
	badge.Set("innerHTML", nodeBadge(status.Running))
	a.setStatus("配置已下发")
}

func (a *app) stopNode(id string) {
	a.setStatus("停止中...")
	var status struct {
		Running bool `json:"running"`
	}
	if err := postJSON("/v1/nodes/"+id+"/runtime/stop", nil, &status, a.token); err != nil {
		a.handleAuthError(err)
		a.setStatus("停止失败: " + err.Error())
		return
	}
	badge := a.byID("node-badge-" + id)
	badge.Set("innerHTML", nodeBadge(false))
	a.byID("node-usage-"+id).Set("innerHTML", "")
	a.setStatus("节点已停止")
}

func (a *app) showNodeLogs(id string) {
	var resp struct {
		Logs []string `json:"logs"`
	}
	if err := getJSON("/v1/nodes/"+id+"/runtime/logs", &resp, a.token); err != nil {
		a.handleAuthError(err)
		a.setStatus("获取日志失败: " + err.Error())
		return
	}
	logContent := a.byID("node-logs-content")
	if len(resp.Logs) == 0 {
		logContent.Set("textContent", "暂无日志")
	} else {
		logContent.Set("textContent", strings.Join(resp.Logs, "\n"))
		logContent.Set("scrollTop", logContent.Get("scrollHeight"))
	}
	a.byID("node-logs-modal").Call("showModal")
}

func (a *app) openEditNodeModal(id string) {
	for _, n := range a.nodes {
		if n.ID == id {
			a.byID("edit-node-id").Set("value", n.ID)
			a.byID("edit-node-name").Set("value", n.Name)
			a.byID("edit-node-url").Set("value", n.BaseURL)
			a.byID("node-edit-modal").Call("showModal")
			return
		}
	}
}

func (a *app) submitEditNode() {
	id := a.value("edit-node-id")
	payload := map[string]any{
		"name":     a.value("edit-node-name"),
		"base_url": a.value("edit-node-url"),
	}
	if err := putJSON("/v1/nodes/"+id, payload, nil, a.token); err != nil {
		a.handleAuthError(err)
		a.setStatus("编辑节点失败: " + err.Error())
		return
	}
	a.byID("node-edit-modal").Call("close")
	a.setStatus("节点已更新")
	a.loadNodes()
}
