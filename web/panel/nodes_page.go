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
	selectEl.Set("innerHTML", `<option value="">选择节点（可选）</option>`)

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
    <button class="btn btn-ghost btn-sm" data-action="inbounds-node" data-id="%s">入站</button>
    <button class="btn btn-ghost btn-sm" data-action="apply-node" data-id="%s">下发</button>
    <button class="btn btn-ghost btn-sm" data-action="stop-node"  data-id="%s">停止</button>
    <button class="btn btn-ghost btn-sm" data-action="logs-node"   data-id="%s">日志</button>
    <button class="btn btn-ghost btn-sm" data-action="config-node" data-id="%s">配置</button>
    <button class="btn btn-ghost btn-sm" data-action="edit-node"   data-id="%s">编辑</button>
    <button class="btn btn-ghost btn-sm btn-danger" data-action="delete-node" data-id="%s">删除</button>
  </div>
</article>`,
			escape(n.Name),
			escape(n.ID), nodeBadge(false),
			escape(n.BaseURL),
			escape(n.ID),
			escape(n.ID),
			escape(n.ID), escape(n.ID), escape(n.ID), escape(n.ID), escape(n.ID), escape(n.ID),
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
		"delete-node":   a.deleteNode,
		"apply-node":    a.applyNode,
		"stop-node":     a.stopNode,
		"logs-node":     a.showNodeLogs,
		"config-node":   a.showNodeConfig,
		"edit-node":     a.openEditNodeModal,
		"inbounds-node": a.openNodeInboundsModal,
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

func (a *app) showNodeConfig(id string) {
	var resp struct {
		Config string `json:"config"`
	}
	if err := getJSON("/v1/nodes/"+id+"/runtime/config", &resp, a.token); err != nil {
		a.handleAuthError(err)
		a.setStatus("获取配置失败: " + err.Error())
		return
	}
	configContent := a.byID("node-config-content")
	if resp.Config == "" {
		configContent.Set("textContent", "暂无配置（节点尚未下发）")
	} else {
		configContent.Set("textContent", resp.Config)
	}
	a.byID("node-config-modal").Call("showModal")
}

// ─── 节点入站管理 ────────────────────────────────────────────────────────────

// openNodeInboundsModal 打开节点入站管理 modal。
func (a *app) openNodeInboundsModal(nodeID string) {
	a.editingNodeID = nodeID
	// 设置 modal 标题
	for _, n := range a.nodes {
		if n.ID == nodeID {
			a.byID("node-inbounds-title").Set("textContent", "入站管理 — "+n.Name)
			break
		}
	}
	// 清空并加载入站列表
	a.byID("node-inbounds-list").Set("innerHTML", "加载中...")
	a.byID("ni-node-id").Set("value", nodeID)
	a.syncNodeInboundProtocolFields()
	a.byID("node-inbounds-modal").Call("showModal")
	go a.loadAndRenderNodeInbounds(nodeID)
}

// loadAndRenderNodeInbounds 加载并渲染节点入站列表。
func (a *app) loadAndRenderNodeInbounds(nodeID string) {
	var resp struct {
		Inbounds []nodeInbound `json:"inbounds"`
	}
	if err := getJSON("/v1/inbounds?node_id="+nodeID, &resp, a.token); err != nil {
		a.byID("node-inbounds-list").Set("textContent", "加载失败: "+err.Error())
		return
	}
	a.nodeInbounds[nodeID] = resp.Inbounds
	a.renderNodeInboundsList(nodeID)
}

// renderNodeInboundsList 渲染节点入站列表 HTML。
func (a *app) renderNodeInboundsList(nodeID string) {
	list := a.byID("node-inbounds-list")
	nibs := a.nodeInbounds[nodeID]

	if len(nibs) == 0 {
		list.Set("innerHTML", `<div class="empty-state" style="padding:12px">暂无入站配置</div>`)
		return
	}

	var buf strings.Builder
	for _, ib := range nibs {
		secTag := ""
		if ib.Security != "" {
			secTag = fmt.Sprintf(` <span class="badge badge-tls">%s</span>`, escape(strings.ToUpper(ib.Security)))
		}
		buf.WriteString(fmt.Sprintf(
			`<div class="inbound-row">
  <span class="inbound-info">%s <span class="text-muted">:%d</span>%s <span class="text-muted tag">%s</span></span>
  <div class="inbound-actions">
    <button class="btn btn-ghost btn-sm btn-danger" data-action="del-node-inbound" data-id="%s" data-node="%s">删除</button>
  </div>
</div>`,
			escape(strings.ToUpper(ib.Protocol)), ib.Port, secTag, escape(ib.Tag),
			escape(ib.ID), escape(nodeID),
		))
	}
	list.Set("innerHTML", buf.String())
	a.bindNodeInboundButtons()
}

// bindNodeInboundButtons 为节点入站列表内的按钮绑定事件。
func (a *app) bindNodeInboundButtons() {
	list := a.byID("node-inbounds-list")
	buttons := list.Call("querySelectorAll", "[data-action='del-node-inbound']")
	for i := 0; i < buttons.Get("length").Int(); i++ {
		button := buttons.Index(i)
		button.Call("addEventListener", "click", js.FuncOf(func(this js.Value, args []js.Value) any {
			ibID := this.Get("dataset").Get("id").String()
			nodeID := this.Get("dataset").Get("node").String()
			go func() {
				if err := doRequest(http.MethodDelete, "/v1/inbounds/"+ibID, nil, nil, a.token); err != nil {
					a.handleAuthError(err)
					a.setStatus("删除入站失败: " + err.Error())
					return
				}
				a.setStatus("入站已删除")
				a.loadAndRenderNodeInbounds(nodeID)
			}()
			return nil
		}))
	}
}

// submitNodeInbound 提交添加节点入站表单。
func (a *app) submitNodeInbound() {
	nodeID := a.value("ni-node-id")
	if nodeID == "" {
		return
	}

	protocol := a.value("ni-protocol")
	port, err := parsePort(a.value("ni-port"))
	if err != nil {
		a.setStatus("端口无效")
		return
	}

	payload := map[string]any{
		"node_id":  nodeID,
		"protocol": protocol,
		"port":     port,
	}
	if method := a.value("ni-method"); method != "" {
		payload["method"] = method
	}
	security := a.value("ni-security")
	if security == "reality" {
		payload["security"] = "reality"
		payload["reality_private_key"] = a.value("ni-reality-privkey")
		payload["reality_public_key"] = a.value("ni-reality-pubkey")
		payload["reality_short_id"] = a.value("ni-reality-shortid")
		if sni := a.value("ni-reality-sni"); sni != "" {
			payload["reality_handshake_addr"] = sni + ":443"
		}
	}

	var createdIb nodeInbound
	if err := postJSON("/v1/inbounds", payload, &createdIb, a.token); err != nil {
		a.handleAuthError(err)
		a.setStatus("添加入站失败: " + err.Error())
		return
	}

	// 自动创建默认 Host（订阅链接需要客户端连接地址）
	if addr := a.value("ni-address"); addr != "" && createdIb.ID != "" {
		hostPayload := map[string]any{
			"inbound_id": createdIb.ID,
			"address":    addr,
		}
		if security == "reality" {
			hostPayload["security"] = "reality"
		}
		if err := postJSON("/v1/hosts", hostPayload, nil, a.token); err != nil {
			a.setStatus("入站已添加，但创建 Host 失败: " + err.Error())
			a.loadAndRenderNodeInbounds(nodeID)
			return
		}
	}

	// 手动清空可见字段，保留隐藏的 ni-node-id
	a.byID("ni-port").Set("value", "")
	a.byID("ni-address").Set("value", "")
	a.byID("ni-method").Set("value", "")
	a.byID("ni-reality-sni").Set("value", "")
	a.byID("ni-reality-pubkey").Set("value", "")
	a.byID("ni-reality-privkey").Set("value", "")
	a.byID("ni-reality-shortid").Set("value", "")
	a.setStatus("入站已添加")
	a.loadAndRenderNodeInbounds(nodeID)
}

// syncNodeInboundProtocolFields 根据协议切换节点入站表单字段显示。
func (a *app) syncNodeInboundProtocolFields() {
	protocol := a.value("ni-protocol")
	methodWrap := a.byID("ni-method-wrap")
	realityWrap := a.byID("ni-reality-wrap")
	secEl := a.byID("ni-security")

	switch protocol {
	case "shadowsocks":
		methodWrap.Set("hidden", false)
		realityWrap.Set("hidden", true)
		secEl.Set("value", "")
	case "vless":
		methodWrap.Set("hidden", true)
		realityWrap.Set("hidden", false)
		secEl.Set("value", "reality")
	default: // trojan, vmess
		methodWrap.Set("hidden", true)
		realityWrap.Set("hidden", true)
		secEl.Set("value", "")
	}
}

// generateRealityKeypair 生成 Reality 密钥对并填充到节点入站表单。
func (a *app) generateRealityKeypair() {
	var resp struct {
		PrivateKey string `json:"private_key"`
		PublicKey  string `json:"public_key"`
		ShortID    string `json:"short_id"`
	}
	if err := getJSON("/v1/tools/reality-keypair", &resp, a.token); err != nil {
		a.setStatus("生成失败: " + err.Error())
		return
	}
	a.byID("ni-reality-pubkey").Set("value", resp.PublicKey)
	a.byID("ni-reality-privkey").Set("value", resp.PrivateKey)
	a.byID("ni-reality-shortid").Set("value", resp.ShortID)
	a.setStatus("已生成密钥对和 Short ID")
}
