//go:build js && wasm

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"syscall/js"
)

type node struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
}

type user struct {
	ID            string `json:"id"`
	Username      string `json:"username"`
	Protocol      string `json:"protocol"`
	Secret        string `json:"secret"`
	Method        string `json:"method"`
	Enabled       bool   `json:"enabled"`
	NodeID        string `json:"node_id"`
	Domain        string `json:"domain"`
	Port          int    `json:"port"`
	TrafficLimit  int64  `json:"traffic_limit_bytes"`
	UploadBytes   int64  `json:"upload_bytes"`
	DownloadBytes int64  `json:"download_bytes"`
	UsedBytes     int64  `json:"used_bytes"`
	ApplyCount    int    `json:"apply_count"`
	LastAppliedAt string `json:"last_applied_at"`
}

type app struct {
	document js.Value
	storage  js.Value
	token    string
}

func main() {
	app := &app{
		document: js.Global().Get("document"),
		storage:  js.Global().Get("localStorage"),
	}
	app.bind()
	app.bootstrap()
	select {}
}

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
}

func (a *app) bootstrap() {
	a.setStatus("加载中...")
	a.syncProtocolFields()
	a.token = a.storage.Call("getItem", "pulse_token").String()
	if a.token == "" {
		a.setAuthenticated(false)
		a.setStatus("请先登录")
		return
	}
	go a.checkSession()
}

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

func (a *app) createNode() {
	payload := map[string]any{
		"id":       a.value("node-id"),
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

func (a *app) createUser() {
	port, err := strconv.Atoi(a.value("user-port"))
	if err != nil {
		a.setStatus("端口必须是数字")
		return
	}

	payload := map[string]any{
		"id":                  a.value("user-id"),
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

func (a *app) loadSystemInfo() {
	var resp struct {
		Name               string         `json:"name"`
		Description        string         `json:"description"`
		NodesCount         int            `json:"nodes_count"`
		UsersCount         int            `json:"users_count"`
		Protocols          map[string]int `json:"protocols"`
		TotalApplyCount    int            `json:"total_apply_count"`
		TotalUsedBytes     int64          `json:"total_used_bytes"`
		LimitedUsersCount  int            `json:"limited_users_count"`
		DisabledUsersCount int            `json:"disabled_users_count"`
		LastAppliedAt      string         `json:"last_applied_at"`
	}
	if err := getJSON("/v1/system/info", &resp, a.token); err != nil {
		a.handleAuthError(err)
		a.setStatus("加载系统统计失败: " + err.Error())
		return
	}

	parts := make([]string, 0, 3)
	for _, key := range []string{"vless", "trojan", "shadowsocks"} {
		if value, ok := resp.Protocols[key]; ok {
			parts = append(parts, fmt.Sprintf("%s: %d", key, value))
		}
	}
	if len(parts) == 0 {
		parts = append(parts, "暂无协议数据")
	}

	lastApplied := displayTime(resp.LastAppliedAt)
	container := a.byID("system-info")
	container.Get("classList").Call("remove", "empty")
	container.Set("innerHTML", fmt.Sprintf(
		`<article class="card">
			<h3>%s</h3>
			<p class="meta">%s</p>
			<p class="meta">节点: %d · 用户: %d</p>
			<p class="meta">协议分布: %s</p>
			<p class="meta">累计下发: %d · 总流量: %dB</p>
			<p class="meta">有限额用户: %d · 已停用: %d · 最近下发: %s</p>
		</article>`,
		escape(resp.Name),
		escape(resp.Description),
		resp.NodesCount,
		resp.UsersCount,
		escape(strings.Join(parts, " · ")),
		resp.TotalApplyCount,
		resp.TotalUsedBytes,
		resp.LimitedUsersCount,
		resp.DisabledUsersCount,
		escape(lastApplied),
	))
}

func (a *app) syncUsage() {
	var resp struct {
		NodesSynced   int      `json:"nodes_synced"`
		UsersUpdated  int      `json:"users_updated"`
		NodesReloaded int      `json:"nodes_reloaded"`
		NodesStopped  int      `json:"nodes_stopped"`
		Errors        []string `json:"errors"`
	}
	if err := postJSON("/v1/system/sync-usage", nil, &resp, a.token); err != nil {
		a.handleAuthError(err)
		a.setStatus("同步使用量失败: " + err.Error())
		return
	}
	a.loadSystemInfo()
	a.loadUsers()
	if len(resp.Errors) > 0 {
		a.setStatus(fmt.Sprintf("同步完成，但有 %d 个错误", len(resp.Errors)))
		return
	}
	a.setStatus(fmt.Sprintf("同步完成: 节点 %d，用户 %d，重载 %d，停止 %d", resp.NodesSynced, resp.UsersUpdated, resp.NodesReloaded, resp.NodesStopped))
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
				<h3>%s</h3>
				<p class="meta">%s</p>
				<div class="actions">
					<button data-action="runtime" data-id="%s">查看 Runtime</button>
					<button data-action="logs" data-id="%s">查看 Logs</button>
					<button data-action="delete-node" data-id="%s" class="ghost">删除</button>
				</div>
				<div id="node-logs-%s" class="detail-box" hidden></div>
			 </article>`, escape(n.Name), escape(n.BaseURL), escape(n.ID), escape(n.ID), escape(n.ID), escape(n.ID))
		container.Set("innerHTML", container.Get("innerHTML").String()+card)
		option := a.document.Call("createElement", "option")
		option.Set("value", n.ID)
		option.Set("textContent", n.Name+" ("+n.ID+")")
		selectEl.Call("appendChild", option)
	}
	a.bindNodeButtons()
	a.setStatus(fmt.Sprintf("已加载 %d 个节点", len(resp.Nodes)))
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

	container := a.byID("users")
	container.Set("innerHTML", "")
	if len(resp.Users) == 0 {
		container.Set("textContent", "暂无用户")
		container.Get("classList").Call("add", "empty")
		return
	}

	container.Get("classList").Call("remove", "empty")
	for _, u := range resp.Users {
		card := fmt.Sprintf(
			`<article class="card">
				<h3>%s</h3>
				<p class="meta">协议: %s · 节点: %s</p>
				<p class="meta">%s:%d</p>
				<p class="meta">状态: %s · 已用: %dB / 上限: %s</p>
				<p class="meta">累计下发: %d · 最近下发: %s</p>
				<div class="actions">
					<button data-action="subscription" data-id="%s">订阅</button>
					<button data-action="apply" data-id="%s">Apply</button>
					<button data-action="delete-user" data-id="%s" class="ghost">删除</button>
				</div>
				<div id="subscription-%s" class="subscription" hidden></div>
			 </article>`,
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
		)
		container.Set("innerHTML", container.Get("innerHTML").String()+card)
	}
	a.bindUserButtons()
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

func (a *app) bindUserButtons() {
	buttons := a.document.Call("querySelectorAll", "[data-action='subscription'], [data-action='apply'], [data-action='delete-user']")
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

func (a *app) byID(id string) js.Value {
	return a.document.Call("getElementById", id)
}

func (a *app) value(id string) string {
	return a.byID(id).Get("value").String()
}

func (a *app) reset(id string) {
	a.byID(id).Call("reset")
}

func (a *app) setStatus(message string) {
	a.byID("status").Set("textContent", message)
}

func (a *app) login() {
	var resp struct {
		Token string `json:"token"`
	}
	err := postJSON("/v1/auth/login", map[string]any{
		"username": a.value("login-username"),
		"password": a.value("login-password"),
	}, &resp, "")
	if err != nil {
		a.setStatus("登录失败: " + err.Error())
		return
	}
	a.token = resp.Token
	a.storage.Call("setItem", "pulse_token", resp.Token)
	a.setAuthenticated(true)
	a.setStatus("登录成功")
	a.reset("login-form")
	a.syncProtocolFields()
	a.loadSystemInfo()
	a.loadNodes()
	a.loadUsers()
}

func (a *app) logout() {
	_ = postJSON("/v1/auth/logout", nil, nil, a.token)
	a.storage.Call("removeItem", "pulse_token")
	a.token = ""
	a.setAuthenticated(false)
	a.setStatus("已退出")
}

func (a *app) checkSession() {
	err := getJSON("/v1/auth/me", nil, a.token)
	if err != nil {
		a.storage.Call("removeItem", "pulse_token")
		a.token = ""
		a.setAuthenticated(false)
		a.setStatus("登录已失效，请重新登录")
		return
	}
	a.setAuthenticated(true)
	a.syncProtocolFields()
	a.loadSystemInfo()
	a.loadNodes()
	a.loadUsers()
}

func (a *app) setAuthenticated(ok bool) {
	a.byID("auth-panel").Set("hidden", ok)
	a.byID("app-panel").Set("hidden", !ok)
}

func (a *app) handleAuthError(err error) {
	if strings.Contains(err.Error(), "unauthorized") {
		a.storage.Call("removeItem", "pulse_token")
		a.token = ""
		a.setAuthenticated(false)
	}
}

func getJSON(path string, out any, token string) error {
	return doRequest(http.MethodGet, path, nil, out, token)
}

func postJSON(path string, payload any, out any, token string) error {
	return doRequest(http.MethodPost, path, payload, out, token)
}

func doRequest(method, path string, payload any, out any, token string) error {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, path, body)
	if err != nil {
		return err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(resp.Body)
		var apiErr struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(data, &apiErr); err == nil && strings.TrimSpace(apiErr.Error) != "" {
			return fmt.Errorf(apiErr.Error)
		}
		message := strings.TrimSpace(string(data))
		if message == "" {
			message = resp.Status
		}
		return fmt.Errorf(message)
	}

	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func escape(value string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&#39;",
	)
	return replacer.Replace(value)
}

func displayTime(value string) string {
	if strings.TrimSpace(value) == "" || value == "0001-01-01T00:00:00Z" {
		return "未下发"
	}
	return value
}

func parseInt64(value string) int64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0
	}
	return parsed
}

func formatLimit(value int64) string {
	if value <= 0 {
		return "不限"
	}
	return fmt.Sprintf("%dB", value)
}

func userStatus(u user) string {
	if !u.Enabled {
		return "已停用"
	}
	return "启用中"
}
