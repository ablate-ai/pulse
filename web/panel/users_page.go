//go:build js && wasm

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"syscall/js"
)

func (a *app) syncProtocolFields() {
	protocol := a.value("user-protocol")
	secret := a.byID("user-secret")
	method := a.byID("user-method")

	switch protocol {
	case "trojan":
		secret.Set("placeholder", "Trojan 密码，可留空自动生成")
		method.Set("value", "")
		method.Set("disabled", true)
	case "shadowsocks":
		secret.Set("placeholder", "Shadowsocks 密码，可留空自动生成")
		method.Set("disabled", false)
		if method.Get("value").String() == "" {
			method.Set("value", "aes-128-gcm")
		}
	default:
		secret.Set("placeholder", "VLESS 无需密码，可留空")
		method.Set("value", "")
		method.Set("disabled", true)
	}
}

func (a *app) createUser() {
	port, err := strconv.Atoi(a.value("user-port"))
	if err != nil {
		a.setStatus("端口必须是数字")
		return
	}

	payload := map[string]any{
		"username":            a.value("user-name"),
		"protocol":            a.value("user-protocol"),
		"secret":              a.value("user-secret"),
		"method":              a.value("user-method"),
		"node_id":             a.value("user-node"),
		"domain":              a.value("user-domain"),
		"port":                port,
		"traffic_limit_bytes": parseInt64(a.value("user-traffic-limit")),
	}
	if err := postJSON("/v1/users", payload, nil, a.token); err != nil {
		a.handleAuthError(err)
		a.setStatus("创建用户失败: " + err.Error())
		return
	}
	a.setStatus("用户已创建")
	a.reset("user-form")
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
		container.Get("classList").Call("add", "empty")
		return
	}

	container.Get("classList").Call("remove", "empty")
	for _, u := range items {
		card := fmt.Sprintf(
			`<article class="card">
				<div class="card-id">ID %s</div>
				<h3>%s</h3>
				<p class="meta">协议: %s · 节点: %s</p>
				<p class="meta">%s:%d</p>
				<p class="meta">状态: %s · 已用: %dB / 上限: %s</p>
				<p class="meta">累计下发: %d · 最近下发: %s</p>
				<div class="actions">
					<button data-action="subscription" data-id="%s">订阅</button>
					<button data-action="config" data-id="%s">查看 Config</button>
					<button data-action="apply" data-id="%s">Apply</button>
					<button data-action="delete-user" data-id="%s" class="ghost">删除</button>
				</div>
				<div id="subscription-%s" class="subscription" hidden></div>
				<div id="config-%s" class="detail-box" hidden></div>
			 </article>`,
			escape(u.ID),
			escape(u.Username),
			escape(strings.ToUpper(u.Protocol)),
			escape(u.NodeID),
			escape(u.Domain),
			u.Port,
			escape(userStatus(u)),
			u.UsedBytes,
			escape(formatLimit(u.TrafficLimit)),
			u.ApplyCount,
			escape(displayTime(u.LastAppliedAt)),
			escape(u.ID),
			escape(u.ID),
			escape(u.ID),
			escape(u.ID),
			escape(u.ID),
			escape(u.ID),
		)
		container.Set("innerHTML", container.Get("innerHTML").String()+card)
	}
	a.bindUserButtons()
}

func (a *app) filteredUsers() []user {
	query := strings.ToLower(strings.TrimSpace(a.value("user-search")))
	protocol := strings.ToLower(strings.TrimSpace(a.value("user-filter-protocol")))
	out := make([]user, 0, len(a.users))
	for _, item := range a.users {
		if protocol != "" && strings.ToLower(item.Protocol) != protocol {
			continue
		}
		if query != "" {
			haystack := strings.ToLower(strings.Join([]string{
				item.ID,
				item.Username,
				item.NodeID,
				item.Domain,
			}, " "))
			if !strings.Contains(haystack, query) {
				continue
			}
		}
		out = append(out, item)
	}
	return out
}

func (a *app) bindUserButtons() {
	buttons := a.document.Call("querySelectorAll", "[data-action='subscription'], [data-action='config'], [data-action='apply'], [data-action='delete-user']")
	length := buttons.Get("length").Int()
	for i := 0; i < length; i++ {
		button := buttons.Index(i)
		button.Call("addEventListener", "click", js.FuncOf(func(this js.Value, args []js.Value) any {
			action := this.Get("dataset").Get("action").String()
			id := this.Get("dataset").Get("id").String()
			go func() {
				switch action {
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
					a.setStatus("已加载订阅: " + id)
				case "config":
					var resp struct {
						NodeConfig json.RawMessage `json:"node_config"`
					}
					if err := postJSON("/v1/users/"+id+"/apply", nil, &resp, a.token); err != nil {
						a.handleAuthError(err)
						a.setStatus("读取配置失败: " + err.Error())
						return
					}
					box := a.byID("config-" + id)
					box.Set("hidden", false)
					if len(resp.NodeConfig) == 0 {
						box.Set("textContent", "当前没有下发配置")
						a.setStatus("当前没有可展示的节点配置: " + id)
						return
					}
					var pretty bytes.Buffer
					if err := json.Indent(&pretty, resp.NodeConfig, "", "  "); err != nil {
						box.Set("textContent", string(resp.NodeConfig))
					} else {
						box.Set("textContent", pretty.String())
					}
					a.setStatus("已加载节点配置: " + id)
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
					a.setStatus(fmt.Sprintf("用户 %s 已下发，节点运行状态: %t", id, resp.NodeStatus.Running))
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
