# pulse

Go 重写版 Marzban + Marzban-node，控制面与节点统一在单一仓库。

## 目录结构

```text
.
├── cmd/pulse-server          # 控制面入口
├── cmd/pulse-node            # 节点入口
├── internal/server           # 控制面 HTTP 服务
├── internal/serverapi        # 控制面 REST API
├── internal/panel            # 管理面板（HTMX + Tailwind 服务端渲染）
│   └── templates/            # HTML 模板
├── internal/nodes            # 节点 store 与 RPC client
├── internal/users            # 用户模型与 store
├── internal/inbounds         # inbound / host 模型与 store
├── internal/jobs             # 后台调度任务
├── internal/proxycfg         # sing-box 配置生成
├── internal/subscription     # 订阅 URL 生成
├── internal/store/sqlite     # SQLite 持久化
├── scripts/install.sh        # 生产安装脚本
├── scripts/uninstall.sh      # 卸载脚本
└── scripts/setup-caddy.sh    # Caddy 反代配置脚本
```

## 开发

```bash
# 同时编译并启动 server + node（热重启）
make dev

# 仅运行测试
make test
```

默认控制面访问地址：`http://localhost:8080`（账号 `admin` / 密码 `admin123`）

> `make dev` 使用硬编码的开发凭据，生产环境请使用安装脚本。

## 发布新版本

```bash
make release
```

交互式选择 patch / minor / major，脚本会自动打 tag 并推送，GitHub Actions 触发构建。

## 生产安装

### 1. 安装 server

```bash
# 安装最新版
curl -fsSL https://raw.githubusercontent.com/ablate-ai/pulse/main/scripts/install.sh | sh -s -- server

# 安装指定版本
curl -fsSL https://raw.githubusercontent.com/ablate-ai/pulse/main/scripts/install.sh | sh -s -- server v0.1.18
```

首次安装会随机生成管理员密码并在安装结束时打印：

```
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  面板地址: http://<IP>:<PORT>
  管理员:   admin
  密码:     <随机生成>
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
```

登录后可在「Settings」页面修改密码，修改结果持久化到数据库，重启后依然有效。

如需修改端口或其他配置：

```bash
vim /etc/pulse/pulse-server.env
systemctl restart pulse-server
```

### 2. 获取 node 所需证书

登录控制面 → Overview 页面，复制「安装 Node」区块中的 server 客户端证书（PEM 格式）。

### 3. 在 node 机器上安装 node

```bash
# 安装最新版
curl -fsSL https://raw.githubusercontent.com/ablate-ai/pulse/main/scripts/install.sh | sh -s -- node

# 安装指定版本
curl -fsSL https://raw.githubusercontent.com/ablate-ai/pulse/main/scripts/install.sh | sh -s -- node v0.1.18
```

执行后脚本会提示粘贴证书，把第 2 步复制的 PEM 内容粘贴进去，读到 `-----END CERTIFICATE-----` 后自动继续。

### 4. 启用 HTTPS（可选，推荐生产环境）

pulse 默认以 HTTP 提供面板，如需 HTTPS 及 Trojan 在标准 443 端口运行，运行 Caddy 配置脚本：

```bash
# server 节点（有面板）
PANEL_DOMAIN=panel.example.com sh <(curl -fsSL https://raw.githubusercontent.com/ablate-ai/pulse/main/scripts/setup-caddy.sh)

# agent 节点（无面板，只需安装 Caddy + 创建 Trojan 路由目录）
sh <(curl -fsSL https://raw.githubusercontent.com/ablate-ai/pulse/main/scripts/setup-caddy.sh)
```

**脚本做的事：**

1. 从 `/etc/pulse/pulse-server.env` 自动读取面板端口（`PULSE_SERVER_ADDR`）
2. 检测 443 端口是否已被占用
3. 自动安装 Caddy（apt / dnf / yum，如已安装则跳过）
4. 生成 `/etc/caddy/Caddyfile`：面板 HTTPS 块 + `import /etc/caddy/pulse.d/*.caddy`
5. 热重载 Caddy，重启 pulse-server

**Trojan 域名路由无需手动配置**，创建 Trojan inbound 后 pulse-server 会自动写入 `/etc/caddy/pulse.d/<domain>.caddy` 并热重载 Caddy。

**可配置的环境变量：**

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `PANEL_DOMAIN` | 空 | 面板对外域名（agent 节点可留空） |
| `PANEL_PORT` | 自动读取，兜底 `8080` | pulse-server 监听端口 |
| `ACME_EMAIL` | 空 | Let's Encrypt 账号邮箱（可选） |
| `CADDYFILE` | `/etc/caddy/Caddyfile` | Caddyfile 路径 |

**架构示意：**

```
客户端
  ├── HTTPS  → :443 (Caddy) → :PANEL_PORT (pulse-server 面板)
  └── Trojan → wss://<domain>/ws → :443 (Caddy) → :WS_PORT (sing-box)
              每个 Trojan inbound 域名对应 /etc/caddy/pulse.d/<domain>.caddy
```

---

### 卸载

```bash
# 一键卸载全部（server、node、Caddy、证书、数据，不可恢复）
curl -fsSL https://raw.githubusercontent.com/ablate-ai/pulse/main/scripts/uninstall.sh | sh
```

**卸载脚本做的事：**

- 停止并禁用 pulse-server、pulse-node、Caddy systemd 服务
- 删除所有二进制、配置文件、服务文件
- 删除 Caddyfile 及 ACME 证书目录
- 删除数据目录（`/var/lib/pulse`、`/var/lib/caddy`）

---

### 安装脚本做了什么

- 从 GitHub Release 下载对应平台（linux/amd64 或 linux/arm64）的 tar.gz
- 安装二进制到 `/usr/local/bin`
- 首次安装时写入示例配置到 `/etc/pulse/*.env`（已有配置不覆盖）
- 注册并启动 systemd 服务（`systemctl enable --now`）
