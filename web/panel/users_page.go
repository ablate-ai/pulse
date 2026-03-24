//go:build js && wasm

package main

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"syscall/js"
)

func (a *app) syncProtocolFields() {
	protocol := a.value("user-protocol")
	secretEl := a.byID("user-secret")
	methodWrap := a.byID("user-method-wrap")
	methodEl := a.byID("user-method")

	switch protocol {
	case "trojan":
		secretEl.Set("placeholder", "Trojan 密码，可留空自动生成")
		methodWrap.Set("hidden", true)
		methodEl.Set("disabled", true)
	case "shadowsocks":
		secretEl.Set("placeholder", "Shadowsocks 密码，可留空自动生成")
		methodWrap.Set("hidden", false)
		methodEl.Set("disabled", false)
		if methodEl.Get("value").String() == "" {
			methodEl.Set("value", "aes-128-gcm")
		}
	default:
		secretEl.Set("placeholder", "VLESS/VMess UUID，可留空自动生成")
		methodWrap.Set("hidden", true)
		methodEl.Set("disabled", true)
	}
}

func (a *app) createUser() {
	port, err := strconv.Atoi(a.value("user-port"))
	if err != nil {
		a.setStatus("端口必须是数字")
		return
	}

	payload := map[string]any{
		"username":                  a.value("user-name"),
		"protocol":                  a.value("user-protocol"),
		"secret":                    a.value("user-secret"),
		"method":                    a.value("user-method"),
		"node_id":                   a.value("user-node"),
		"domain":                    a.value("user-domain"),
		"port":                      port,
		"traffic_limit_bytes":       gbToBytes(a.value("user-traffic-limit")),
		"data_limit_reset_strategy": a.value("user-reset-strategy"),
	}

	if expireVal := a.value("user-expire-at"); expireVal != "" {
		payload["expire_at"] = datetimeToRFC3339(expireVal)
	}

	if err := postJSON("/v1/users", payload, nil, a.token); err != nil {
		a.handleAuthError(err)
		a.setStatus("创建用户失败: " + err.Error())
		return
	}
	a.setStatus("用户已创建")
	a.reset("user-form")
	a.byID("user-port").Set("value", randomPort())
	a.syncProtocolFields()
	a.loadUsers()
}

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
	a.renderUsers()
}

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
    <div class="user-card-badges">%s%s</div>
  </div>
  <div class="user-card-meta">
    <span>%s:%d</span>
    <span>节点 %s</span>
    <span>过期 %s</span>
  </div>
  <div class="traffic-bar" title="%s / %s">
    <div class="%s" style="width:%d%%"></div>
  </div>
  <div class="user-card-traffic">
    <span>↑ %s &nbsp; ↓ %s</span>
    <span>已用 %s / %s</span>
  </div>
  <div class="user-card-actions">
    <button class="btn btn-ghost btn-sm" data-action="edit" data-id="%s">编辑</button>
    <button class="btn btn-ghost btn-sm" data-action="apply" data-id="%s">Apply</button>
    <button class="btn btn-ghost btn-sm" data-action="subscription" data-id="%s">订阅</button>
    <button class="btn btn-ghost btn-sm btn-danger" data-action="delete-user" data-id="%s">删除</button>
  </div>
  <div id="subscription-%s" class="detail-box" hidden></div>
</article>`,
			escape(u.Username),
			statusBadge(u.Status),
			protoBadge(u.Protocol),
			escape(u.Domain), u.Port,
			escape(u.NodeID),
			displayTime(u.ExpireAt),
			formatBytesShort(u.UsedBytes), formatLimit(u.TrafficLimit),
			fillClass, pct,
			formatBytesShort(u.UploadBytes), formatBytesShort(u.DownloadBytes),
			formatBytesShort(u.UsedBytes), formatLimit(u.TrafficLimit),
			escape(u.ID), escape(u.ID), escape(u.ID), escape(u.ID),
			escape(u.ID),
		))
	}
	container.Set("innerHTML", buf.String())
	a.bindUserButtons()
}

func (a *app) filteredUsers() []user {
	query := strings.ToLower(strings.TrimSpace(a.value("user-search")))
	protocol := strings.ToLower(strings.TrimSpace(a.value("user-filter-protocol")))
	status := strings.ToLower(strings.TrimSpace(a.value("user-filter-status")))
	out := make([]user, 0, len(a.users))
	for _, u := range a.users {
		if protocol != "" && strings.ToLower(u.Protocol) != protocol {
			continue
		}
		if status != "" && strings.ToLower(u.Status) != status {
			continue
		}
		if query != "" {
			haystack := strings.ToLower(strings.Join([]string{
				u.ID, u.Username, u.NodeID, u.Domain,
			}, " "))
			if !strings.Contains(haystack, query) {
				continue
			}
		}
		out = append(out, u)
	}
	return out
}

func (a *app) openEditModal(userID string) {
	for _, u := range a.users {
		if u.ID != userID {
			continue
		}
		a.editingUserID = userID
		a.byID("edit-user-id").Set("value", u.ID)
		a.byID("edit-traffic-limit").Set("value", bytesToGBString(u.TrafficLimit))
		a.byID("edit-expire-at").Set("value", datetimeLocalValue(u.ExpireAt))

		// 设置 status select
		statusEl := a.byID("edit-status")
		statusEl.Set("value", u.Status)

		// 设置 reset strategy select
		resetEl := a.byID("edit-reset-strategy")
		resetEl.Set("value", u.DataLimitResetStrategy)

		a.byID("edit-modal").Call("showModal")
		return
	}
	a.setStatus("未找到用户: " + userID)
}

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

func (a *app) bindUserButtons() {
	buttons := a.document.Call("querySelectorAll",
		"[data-action='edit'], [data-action='subscription'], [data-action='apply'], [data-action='delete-user']")
	length := buttons.Get("length").Int()
	for i := 0; i < length; i++ {
		button := buttons.Index(i)
		button.Call("addEventListener", "click", js.FuncOf(func(this js.Value, args []js.Value) any {
			action := this.Get("dataset").Get("action").String()
			id := this.Get("dataset").Get("id").String()
			go func() {
				switch action {
				case "edit":
					a.openEditModal(id)
				case "subscription":
					var resp struct {
						Link string `json:"link"`
					}
					if err := getJSON("/v1/users/"+id+"/subscription", &resp, a.token); err != nil {
						a.handleAuthError(err)
						a.setStatus("读取订阅失败: " + err.Error())
						return
					}
					box := a.byID("subscription-" + id)
					box.Set("hidden", false)
					box.Set("textContent", resp.Link)
					a.setStatus("已加载订阅链接")
				case "apply":
					var resp struct {
						NodeStatus struct {
							Running bool `json:"running"`
						} `json:"node_status"`
					}
					if err := postJSON("/v1/users/"+id+"/apply", nil, &resp, a.token); err != nil {
						a.handleAuthError(err)
						a.setStatus("Apply 失败: " + err.Error())
						return
					}
					a.setStatus(fmt.Sprintf("用户 %s 已下发，节点运行中: %t", id, resp.NodeStatus.Running))
				case "delete-user":
					if err := doRequest(http.MethodDelete, "/v1/users/"+id, nil, nil, a.token); err != nil {
						a.handleAuthError(err)
						a.setStatus("删除用户失败: " + err.Error())
						return
					}
					a.setStatus("用户已删除: " + id)
					a.loadUsers()
				}
			}()
			return nil
		}))
	}
}
