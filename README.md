# pulse

Go 重写版 Marzban + Marzban-node，控制面与节点统一在单一仓库。

## 目录结构

```text
.
├── cmd/pulse-server      # 控制面入口
├── cmd/pulse-node        # 节点入口
├── internal/server       # 控制面 HTTP 服务
├── internal/serverapi    # 控制面 REST API
├── internal/nodes        # 节点 store 与 RPC client
├── internal/users        # 用户模型与 store
├── internal/inbounds     # inbound / host 模型与 store
├── internal/jobs         # 后台调度任务
├── internal/proxycfg     # sing-box 配置生成
├── internal/subscription # 订阅 URL 生成
├── internal/store/sqlite # SQLite 持久化
├── web/panel             # 前端静态资源（WASM）
├── scripts/install.sh    # 生产安装脚本
├── scripts/uninstall.sh  # 卸载脚本
└── scripts/setup-caddy.sh # Caddy 反代配置脚本（A2 架构）
```

## 开发

```bash
# 编译前端 WASM
make wasm

# 启动控制面（自动编译 wasm + server）
make run-server

# 启动节点
make run-node

# 运行测试
make test
```

默认控制面访问地址：`http://localhost:8080`（账号 `admin` / `admin123`）

## 发布新版本

```bash
make release
```

交互式选择 patch / minor / major，脚本会自动打 tag 并推送，GitHub Actions 触发构建。

## 生产安装

### 1. 安装 server

```bash
curl -fsSL https://raw.githubusercontent.com/ablate-ai/pulse/main/scripts/install.sh | \
  PULSE_ADMIN_PASSWORD='your-password' sh -s -- server
```

安装完成后服务自动启动，面板地址：`http://<IP>:8080`

如需修改端口或其他配置：

```bash
vim /etc/pulse/pulse-server.env
systemctl restart pulse-server
```

### 2. 获取 node 所需证书

登录控制面 → Overview 页面，复制「安装 Node」区块中的 server 客户端证书（PEM 格式）。

### 3. 在 node 机器上安装 node

```bash
curl -fsSL https://raw.githubusercontent.com/ablate-ai/pulse/main/scripts/install.sh | sh -s -- node
```

执行后脚本会提示粘贴证书，把第 2 步复制的 PEM 内容粘贴进去，读到 `-----END CERTIFICATE-----` 后自动继续。

### 4. 启用 HTTPS（可选，推荐生产环境）

pulse 默认以 HTTP 提供面板，如需 HTTPS 及 Trojan 在标准 443 端口运行，运行 Caddy 配置脚本：

```bash
PANEL_DOMAIN=panel.example.com sh <(curl -fsSL https://raw.githubusercontent.com/ablate-ai/pulse/main/scripts/setup-caddy.sh)
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
| `PANEL_DOMAIN` | 必填 | 面板对外域名 |
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
# 卸载 server（保留数据）
curl -fsSL https://raw.githubusercontent.com/ablate-ai/pulse/main/scripts/uninstall.sh | sh -s -- server

# 卸载 node
curl -fsSL https://raw.githubusercontent.com/ablate-ai/pulse/main/scripts/uninstall.sh | sh -s -- node

# 同时删除数据库、证书等（不可恢复）
curl -fsSL https://raw.githubusercontent.com/ablate-ai/pulse/main/scripts/uninstall.sh | sh -s -- server --purge
```

**卸载脚本做的事：**

- 停止并禁用 systemd 服务
- 删除二进制、配置文件、服务文件
- server：删除面板静态资源
- node：删除节点客户端证书
- 默认**保留**数据目录（`/var/lib/pulse`），`--purge` 一并删除

---

### 安装脚本做了什么

- 从 GitHub Release 下载对应平台（linux/amd64 或 linux/arm64）的 tar.gz
- 安装二进制到 `/usr/local/bin`
- 首次安装时写入示例配置到 `/etc/pulse/*.env`（已有配置不覆盖）
- 注册并启动 systemd 服务（`systemctl enable --now`）
