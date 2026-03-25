//go:build js && wasm

package main

import (
	"fmt"
	"net/http"
	"strings"
	"syscall/js"
)

// createUser 创建用户，可选关联节点（自动生成凭据）。
func (a *app) createUser() {
	trafficLimit := gbToBytes(a.value("user-traffic-limit"))
	userPayload := map[string]any{
		"username":                  a.value("user-name"),
		"traffic_limit_bytes":       trafficLimit,
		"data_limit_reset_strategy": a.value("user-reset-strategy"),
	}
	if expireVal := a.value("user-expire-at"); expireVal != "" {
		userPayload["expire_at"] = datetimeToRFC3339(expireVal)
	}

	var createdUser user
	if err := postJSON("/v1/users", userPayload, &createdUser, a.token); err != nil {
		a.handleAuthError(err)
		a.setStatus("创建用户失败: " + err.Error())
		return
	}

	// 可选：关联节点（只需 node_id，凭据自动生成）
	if nodeID := a.value("user-node"); nodeID != "" {
		if err := postJSON("/v1/users/"+createdUser.ID+"/inbounds",
			map[string]any{"node_id": nodeID}, nil, a.token); err != nil {
			a.setStatus("创建入站失败: " + err.Error())
			return
		}
	}

	a.setStatus("用户已创建")
	a.reset("user-form")
	a.loadUsers()
}

// loadUsers 从 API 加载用户列表并渲染。
func (a *app) loadUsers() {
	var resp struct {
		Users []user `json:"users"`
	}
	if err := getJSON("/v1/users", &resp, a.token); err != nil {
		a.handleAuthError(err)
		a.setStatus("加载用户失败: " + err.Error())
		return
	}
	a.users = resp.Users
	a.userInbounds = make(map[string][]userInbound)
	a.renderUsers()
}

// renderUsers 渲染用户卡片列表。
func (a *app) renderUsers() {
	container := a.byID("users")
	container.Set("innerHTML", "")

	items := a.filteredUsers()
	if len(items) == 0 {
		container.Set("textContent", "暂无符合条件的用户")
		container.Get("classList").Call("add", "empty-state")
		return
	}

	container.Get("classList").Call("remove", "empty-state")
	var buf strings.Builder
	for _, u := range items {
		pct := trafficPercent(u.UsedBytes, u.TrafficLimit)
		fillClass := trafficFillClass(pct)

		buf.WriteString(fmt.Sprintf(`<article class="user-card">
  <div class="user-card-head">
    <div class="user-card-name">%s</div>
    <div class="user-card-badges">%s</div>
  </div>
  <div class="traffic-bar" title="%s / %s">
    <div class="%s" style="width:%d%%"></div>
  </div>
  <div class="user-card-traffic">
    <span>↑ %s &nbsp; ↓ %s</span>
    <span>已用 %s / %s</span>
  </div>
  <div class="user-card-meta">
    <span>过期 %s</span>
  </div>
  <div class="user-card-actions">
    <button class="btn btn-ghost btn-sm" data-action="toggle-inbounds" data-id="%s">入站</button>
    <button class="btn btn-ghost btn-sm" data-action="edit-user" data-id="%s">编辑</button>
    <button class="btn btn-ghost btn-sm btn-danger" data-action="delete-user" data-id="%s">删除</button>
  </div>
  <div id="inbounds-%s" class="inbounds-panel" hidden></div>
</article>`,
			escape(u.Username),
			statusBadge(u.Status),
			formatBytesShort(u.UsedBytes), formatLimit(u.TrafficLimit),
			fillClass, pct,
			formatBytesShort(u.UploadBytes), formatBytesShort(u.DownloadBytes),
			formatBytesShort(u.UsedBytes), formatLimit(u.TrafficLimit),
			displayTime(u.ExpireAt),
			escape(u.ID),
			escape(u.ID),
			escape(u.ID),
			escape(u.ID),
		))
	}
	container.Set("innerHTML", buf.String())
	a.bindUserButtons()
}

// filteredUsers 根据搜索框和状态过滤器返回匹配的用户列表。
func (a *app) filteredUsers() []user {
	query := strings.ToLower(strings.TrimSpace(a.value("user-search")))
	status := strings.ToLower(strings.TrimSpace(a.value("user-filter-status")))
	out := make([]user, 0, len(a.users))
	for _, u := range a.users {
		if status != "" && strings.ToLower(u.Status) != status {
			continue
		}
		if query != "" {
			haystack := strings.ToLower(strings.Join([]string{
				u.ID, u.Username,
			}, " "))
			if !strings.Contains(haystack, query) {
				continue
			}
		}
		out = append(out, u)
	}
	return out
}

// openEditModal 打开编辑用户 modal 并填充当前值。
func (a *app) openEditModal(userID string) {
	for _, u := range a.users {
		if u.ID != userID {
			continue
		}
		a.editingUserID = userID
		a.byID("edit-user-id").Set("value", u.ID)
		a.byID("edit-traffic-limit").Set("value", bytesToGBString(u.TrafficLimit))
		a.byID("edit-expire-at").Set("value", datetimeLocalValue(u.ExpireAt))

		statusEl := a.byID("edit-status")
		statusEl.Set("value", u.Status)

		resetEl := a.byID("edit-reset-strategy")
		resetEl.Set("value", u.DataLimitResetStrategy)

		a.byID("edit-modal").Call("showModal")
		return
	}
	a.setStatus("未找到用户: " + userID)
}

// submitEditUser 提交编辑用户表单。
func (a *app) submitEditUser() {
	userID := a.editingUserID
	if userID == "" {
		a.setStatus("没有正在编辑的用户")
		return
	}

	payload := map[string]any{
		"status":                    a.value("edit-status"),
		"traffic_limit_bytes":       gbToBytes(a.value("edit-traffic-limit")),
		"data_limit_reset_strategy": a.value("edit-reset-strategy"),
		"expire_at":                 datetimeToRFC3339(a.value("edit-expire-at")),
	}

	if err := doRequest("PUT", "/v1/users/"+userID, payload, nil, a.token); err != nil {
		a.handleAuthError(err)
		a.setStatus("更新用户失败: " + err.Error())
		return
	}

	a.byID("edit-modal").Call("close")
	a.editingUserID = ""
	a.setStatus("用户已更新: " + userID)
	a.loadUsers()
}

// toggleInbounds 展开或收起指定用户的入站列表面板。
func (a *app) toggleInbounds(userID string) {
	panel := a.byID("inbounds-" + userID)
	if !panel.Get("hidden").Bool() {
		panel.Set("hidden", true)
		return
	}
	go a.loadInboundsForUser(userID)
}

// loadInboundsForUser 从 API 获取用户的入站列表并渲染面板。
func (a *app) loadInboundsForUser(userID string) {
	var resp struct {
		Inbounds []userInbound `json:"inbounds"`
	}
	if err := getJSON("/v1/users/"+userID+"/inbounds", &resp, a.token); err != nil {
		a.handleAuthError(err)
		a.setStatus("加载入站失败: " + err.Error())
		return
	}
	a.userInbounds[userID] = resp.Inbounds
	a.renderInboundsPanel(userID)
	a.byID("inbounds-" + userID).Set("hidden", false)
}

// renderInboundsPanel 渲染指定用户的入站列表 HTML。
func (a *app) renderInboundsPanel(userID string) {
	panel := a.byID("inbounds-" + userID)
	inbs := a.userInbounds[userID]

	var buf strings.Builder
	buf.WriteString(`<div class="inbounds-list">`)

	for _, ib := range inbs {
		nodeName := a.nodeNameByID(ib.NodeID)
		buf.WriteString(fmt.Sprintf(
			`<div class="inbound-row">
  <span class="inbound-info">%s — UUID: %s</span>
  <div class="inbound-actions">
    <button class="btn btn-ghost btn-sm" data-action="apply-inbound" data-user="%s" data-id="%s">下发</button>
    <button class="btn btn-ghost btn-sm" data-action="sub-inbound" data-user="%s" data-id="%s">订阅</button>
    <button class="btn btn-ghost btn-sm btn-danger" data-action="del-inbound" data-user="%s" data-id="%s">删除</button>
  </div>
  <div id="sub-%s-%s" class="detail-box" hidden></div>
</div>`,
			escape(nodeName), escape(truncate(ib.UUID, 16)),
			escape(userID), escape(ib.ID),
			escape(userID), escape(ib.ID),
			escape(userID), escape(ib.ID),
			escape(userID), escape(ib.ID),
		))
	}

	// 添加入站按钮
	buf.WriteString(fmt.Sprintf(
		`<button class="btn btn-ghost btn-sm" data-action="add-inbound" data-id="%s">+ 添加入站</button>`,
		escape(userID),
	))
	buf.WriteString(`</div>`)

	panel.Set("innerHTML", buf.String())
	a.bindInboundButtons(userID)
}

// bindInboundButtons 为入站面板内的按钮绑定事件处理。
func (a *app) bindInboundButtons(userID string) {
	panel := a.byID("inbounds-" + userID)
	buttons := panel.Call("querySelectorAll", "[data-action]")
	length := buttons.Get("length").Int()
	for i := 0; i < length; i++ {
		button := buttons.Index(i)
		button.Call("addEventListener", "click", js.FuncOf(func(this js.Value, args []js.Value) any {
			action := this.Get("dataset").Get("action").String()
			ibID := this.Get("dataset").Get("id").String()
			uid := this.Get("dataset").Get("user").String()
			go func() {
				switch action {
				case "apply-inbound":
					if err := postJSON("/v1/users/"+uid+"/inbounds/"+ibID+"/apply", nil, nil, a.token); err != nil {
						a.handleAuthError(err)
						a.setStatus("下发失败: " + err.Error())
						return
					}
					a.setStatus(fmt.Sprintf("入站 %s 已下发", ibID))

				case "sub-inbound":
					var resp struct {
						Links []struct {
							Protocol string `json:"protocol"`
							Remark   string `json:"remark"`
							Link     string `json:"link"`
						} `json:"links"`
					}
					if err := getJSON("/v1/users/"+uid+"/inbounds/"+ibID+"/subscription", &resp, a.token); err != nil {
						a.handleAuthError(err)
						a.setStatus("读取订阅失败: " + err.Error())
						return
					}
					box := a.byID("sub-" + uid + "-" + ibID)
					box.Set("hidden", false)
					var linkHTML strings.Builder
					for _, l := range resp.Links {
						linkHTML.WriteString(fmt.Sprintf(
							`<div class="sub-link-row"><span class="sub-proto">%s</span><span class="detail-link">%s</span> <button type="button" class="btn btn-ghost btn-sm" onclick="copyText(%q)">复制</button></div>`,
							escape(strings.ToUpper(l.Protocol)), escape(l.Link), l.Link,
						))
					}
					if len(resp.Links) == 0 {
						linkHTML.WriteString(`<span class="text-muted">暂无链接（节点未配置入站）</span>`)
					}
					box.Set("innerHTML", linkHTML.String())
					a.setStatus("已加载订阅链接")

				case "del-inbound":
					if err := doRequest(http.MethodDelete, "/v1/users/"+uid+"/inbounds/"+ibID, nil, nil, a.token); err != nil {
						a.handleAuthError(err)
						a.setStatus("删除入站失败: " + err.Error())
						return
					}
					a.setStatus("入站已删除")
					a.loadInboundsForUser(uid)

				case "add-inbound":
					// ibID 在 add-inbound 操作中实际对应 data-id（即 userID）
					a.editingInboundUserID = ibID
					a.openAddInboundModal()
				}
			}()
			return nil
		}))
	}
}

// openAddInboundModal 打开添加入站 modal（仅选择节点）。
func (a *app) openAddInboundModal() {
	selectEl := a.byID("add-ib-node")
	selectEl.Set("innerHTML", `<option value="">选择节点</option>`)
	for _, n := range a.nodes {
		opt := a.document.Call("createElement", "option")
		opt.Set("value", n.ID)
		opt.Set("textContent", n.Name)
		selectEl.Call("appendChild", opt)
	}
	a.byID("inbound-add-modal").Call("showModal")
}

// submitAddInbound 提交添加入站表单（只需 node_id）。
func (a *app) submitAddInbound() {
	userID := a.editingInboundUserID
	if userID == "" {
		return
	}
	nodeID := a.value("add-ib-node")
	if nodeID == "" {
		a.setStatus("请选择节点")
		return
	}
	if err := postJSON("/v1/users/"+userID+"/inbounds",
		map[string]any{"node_id": nodeID}, nil, a.token); err != nil {
		a.handleAuthError(err)
		a.setStatus("添加入站失败: " + err.Error())
		return
	}
	a.byID("inbound-add-modal").Call("close")
	a.editingInboundUserID = ""
	a.setStatus("入站已添加")
	a.loadInboundsForUser(userID)
}

// bindUserButtons 为用户卡片上的操作按钮绑定事件。
func (a *app) bindUserButtons() {
	buttons := a.document.Call("querySelectorAll",
		"[data-action='edit-user'], [data-action='delete-user'], [data-action='toggle-inbounds']")
	length := buttons.Get("length").Int()
	for i := 0; i < length; i++ {
		button := buttons.Index(i)
		button.Call("addEventListener", "click", js.FuncOf(func(this js.Value, args []js.Value) any {
			action := this.Get("dataset").Get("action").String()
			id := this.Get("dataset").Get("id").String()
			go func() {
				switch action {
				case "edit-user":
					a.openEditModal(id)
				case "delete-user":
					if err := doRequest(http.MethodDelete, "/v1/users/"+id, nil, nil, a.token); err != nil {
						a.handleAuthError(err)
						a.setStatus("删除用户失败: " + err.Error())
						return
					}
					a.setStatus("用户已删除: " + id)
					a.loadUsers()
				case "toggle-inbounds":
					a.toggleInbounds(id)
				}
			}()
			return nil
		}))
	}
}

// nodeNameByID 根据节点 ID 返回节点名称，未找到时返回 ID 本身。
func (a *app) nodeNameByID(nodeID string) string {
	for _, n := range a.nodes {
		if n.ID == nodeID {
			return n.Name
		}
	}
	return nodeID
}

// truncate 截断字符串到指定长度，超出时加省略号。
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
