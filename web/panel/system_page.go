//go:build js && wasm

package main

import (
	"fmt"
	"strings"
)

func (a *app) loadSystemInfo() {
	var resp struct {
		NodesCount         int            `json:"nodes_count"`
		UsersCount         int            `json:"users_count"`
		Protocols          map[string]int `json:"protocols"`
		TotalUsedBytes     int64          `json:"total_used_bytes"`
		LimitedUsersCount  int            `json:"limited_users_count"`
		DisabledUsersCount int            `json:"disabled_users_count"`
		Version            string         `json:"version"`
		Commit             string         `json:"commit"`
	}
	if err := getJSON("/v1/system/info", &resp, a.token); err != nil {
		a.handleAuthError(err)
		a.setStatus("加载系统统计失败: " + err.Error())
		return
	}

	activeUsers := resp.UsersCount - resp.LimitedUsersCount - resp.DisabledUsersCount

	// 协议分布小字
	protoParts := make([]string, 0, 3)
	for _, key := range []string{"vless", "trojan", "shadowsocks"} {
		if value, ok := resp.Protocols[key]; ok {
			protoParts = append(protoParts, fmt.Sprintf("%s %d", key, value))
		}
	}
	protoStr := "暂无"
	if len(protoParts) > 0 {
		protoStr = strings.Join(protoParts, " · ")
	}

	container := a.byID("system-info")
	container.Get("classList").Call("remove", "empty")
	container.Set("innerHTML", fmt.Sprintf(
		`<article class="stat-card">
			<p class="meta">节点数</p>
			<strong>%d</strong>
		</article>
		<article class="stat-card">
			<p class="meta">用户数</p>
			<strong>%d</strong>
		</article>
		<article class="stat-card">
			<p class="meta">总流量</p>
			<strong>%s</strong>
		</article>
		<article class="stat-card">
			<p class="meta">活跃用户</p>
			<strong>%d</strong>
		</article>
		<p class="meta overview-meta-row">限速 %d · 停用 %d · 协议: %s</p>
	<p class="meta overview-meta-row">server %s</p>`,
		resp.NodesCount,
		resp.UsersCount,
		formatBytes(resp.TotalUsedBytes),
		activeUsers,
		resp.LimitedUsersCount,
		resp.DisabledUsersCount,
		escape(protoStr),
		escape(resp.Version),
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

	installCmd := "curl -fsSL https://raw.githubusercontent.com/ablate-ai/pulse/main/scripts/install.sh | sh -s -- node"
	cert := strings.TrimSpace(settings.Certificate)

	container := a.byID("node-install-info")
	container.Get("classList").Call("remove", "empty")
	container.Set("innerHTML", fmt.Sprintf(
		`<div class="install-col">
			<p class="meta">安装命令</p>
			<div class="pre-wrap">
				<pre id="install-cmd-pre">%s</pre>
				<button class="btn btn-ghost btn-sm copy-btn"
					onclick="copyText(document.getElementById('install-cmd-pre').textContent)">复制</button>
			</div>
		</div>
		<div class="install-col">
			<p class="meta">Node 信任证书</p>
			<div class="pre-wrap">
				<pre id="install-cert-pre">%s</pre>
				<button class="btn btn-ghost btn-sm copy-btn"
					onclick="copyText(document.getElementById('install-cert-pre').textContent)">复制</button>
			</div>
		</div>`,
		escape(installCmd),
		escape(cert),
	))
}
