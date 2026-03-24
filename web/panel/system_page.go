//go:build js && wasm

package main

import (
	"fmt"
	"strings"
)

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
		`<article class="stat-card">
			<p class="meta">节点总数</p>
			<strong>%d</strong>
		</article>
		<article class="stat-card">
			<p class="meta">用户总数</p>
			<strong>%d</strong>
		</article>
		<article class="stat-card">
			<p class="meta">累计下发</p>
			<strong>%d</strong>
		</article>
		<article class="stat-card">
			<p class="meta">总流量</p>
			<strong>%s</strong>
		</article>
		<article class="stat-card wide">
			<p class="meta">系统</p>
			<strong>%s</strong>
			<p class="meta">%s</p>
		</article>
		<article class="stat-card wide">
			<p class="meta">协议分布</p>
			<strong>%s</strong>
			<p class="meta">有限额用户 %d · 已停用 %d · 最近下发 %s</p>
		</article>`,
		resp.NodesCount,
		resp.UsersCount,
		resp.TotalApplyCount,
		formatBytes(resp.TotalUsedBytes),
		escape(resp.Name),
		escape(resp.Description),
		escape(strings.Join(parts, " · ")),
		resp.LimitedUsersCount,
		resp.DisabledUsersCount,
		escape(lastApplied),
	))
	a.loadNodeInstallInfo()
}

func (a *app) loadNodeInstallInfo() {
	var settings struct {
		Certificate string `json:"certificate"`
	}
	if err := getJSON("/v1/node/settings", &settings, a.token); err != nil {
		a.handleAuthError(err)
		a.byID("node-install-info").Set("innerHTML", `<p class="meta">加载安装信息失败</p>`)
		a.setStatus("加载 node 安装信息失败: " + err.Error())
		return
	}

	serverURL := strings.TrimSpace(a.window.Get("location").Get("origin").String())
	command := fmt.Sprintf(
		"curl -fsSL https://raw.githubusercontent.com/ablate-ai/pulse/main/scripts/install.sh | \\\n  PULSE_SERVER_URL='%s' \\\n  PULSE_NODE_SETTINGS_TOKEN='%s' sh -s -- node",
		serverURL,
		a.token,
	)

	container := a.byID("node-install-info")
	container.Get("classList").Call("remove", "empty")
	container.Set("innerHTML", fmt.Sprintf(
		`<article class="install-card">
			<p class="meta">控制面地址</p>
			<pre>%s</pre>
		</article>
		<article class="install-card">
			<p class="meta">Bearer Token</p>
			<pre>%s</pre>
		</article>
		<article class="install-card wide">
			<p class="meta">安装命令</p>
			<pre>%s</pre>
		</article>
		<article class="install-card wide">
			<p class="meta">Node 信任证书 PEM</p>
			<pre>%s</pre>
		</article>`,
		escape(serverURL),
		escape(strings.TrimSpace(a.token)),
		escape(command),
		escape(strings.TrimSpace(settings.Certificate)),
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
