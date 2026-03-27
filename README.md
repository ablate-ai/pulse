# pulse

Go 重写版 Marzban + Marzban-node，控制面与节点统一在单一仓库。

## 目录结构

```text
.
├── cmd/pulse-server          # 控制面入口
├── cmd/pulse-node            # 节点入口
├── cmd/pulse                 # 合并入口（server + node）
├── internal/app              # 应用启动与生命周期
├── internal/auth             # 管理员认证
├── internal/buildinfo        # 版本信息
├── internal/cert             # 自签证书生成
├── internal/certmgr          # mTLS 证书管理
├── internal/config           # 配置结构
├── internal/idgen            # Snowflake ID 生成
├── internal/inbounds         # inbound / host 模型与 store
├── internal/jobs             # 后台调度任务
├── internal/node             # 节点侧服务（sing-box 管理、gRPC server）
├── internal/nodeapi          # 节点 RPC API（含 Caddy 热重载）
├── internal/nodeauth         # 节点认证中间件
├── internal/nodes            # 节点 store 与 RPC client
├── internal/panel            # 管理面板（HTMX + Tailwind 服务端渲染）
│   └── templates/            # HTML 模板
├── internal/proxycfg         # sing-box / 代理配置生成
├── internal/server           # 控制面 HTTP 服务
├── internal/serverapi        # 控制面 REST API
├── internal/singbox          # sing-box 进程管理
├── internal/store/sqlite     # SQLite 持久化
├── internal/subscription     # 订阅 URL 生成
├── internal/usage            # 节点流量统计
├── internal/users            # 用户模型与 store
├── internal/xray             # xray 协议支持（预留）
├── scripts/install.sh        # 生产安装脚本
├── scripts/uninstall.sh      # 卸载脚本
└── scripts/setup-caddy.sh    # Caddy 反代配置脚本
```

## 开发

```bash
# 同时编译并启动 server + node
make dev

# 停止开发进程
make stop

# 仅运行测试
make test

# 编译所有二进制（pulse / pulse-server / pulse-node）
make build
```

默认访问地址：`http://localhost:8080`（账号 `admin` / 密码 `admin123`），node 监听 `:8081`。

> `make dev` 使用硬编码的开发凭据，生产环境请使用安装脚本。

## 发布新版本

```bash
make release
```

交互式选择 patch / minor / major，脚本会先运行测试，通过后自动打 tag 并推送，GitHub Actions 触发构建。

## 生产安装

### 1. 安装 server

```bash
# 安装最新版
curl -fsSL https://raw.githubusercontent.com/ablate-ai/pulse/main/scripts/install.sh | sh -s -- server

# 安装指定版本
curl -fsSL https://raw.githubusercontent.com/ablate-ai/pulse/main/scripts/install.sh | sh -s -- server v0.1.18

# 指定管理员密码和面板域名（启用 HTTPS）
curl -fsSL https://raw.githubusercontent.com/ablate-ai/pulse/main/scripts/install.sh | \
  PULSE_ADMIN_PASSWORD='strong-password' PULSE_PANEL_DOMAIN='panel.example.com' sh -s -- server
```

首次安装会随机生成端口和管理员密码，安装结束后打印：

```
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  面板地址: http://<IP>:<随机端口>
  管理员:   admin
  密码:     <随机生成>
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
```

若安装时指定了 `PULSE_PANEL_DOMAIN`，面板地址会显示为 `https://<domain>`。

如需修改端口或其他配置：

```bash
vim /etc/pulse/pulse-server.env
systemctl restart pulse-server
```

**安装脚本支持的环境变量：**

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `PULSE_REPO` | `ablate-ai/pulse` | GitHub 仓库（owner/repo） |
| `PULSE_ADMIN_USERNAME` | `admin` | 管理员用户名 |
| `PULSE_ADMIN_PASSWORD` | 随机生成 | 管理员密码 |
| `PULSE_SERVER_ADDR` | 随机端口 | server 监听地址，格式 `:端口` |
| `PULSE_PANEL_DOMAIN` | 空 | 面板对外域名，设置后自动启用 HTTPS |
| `PULSE_INSTALL_BIN` | `/usr/local/bin` | 二进制安装目录 |
| `PULSE_INSTALL_ETC` | `/etc/pulse` | 配置安装目录 |
| `PULSE_STATE_DIR` | `/var/lib/pulse` | 数据目录 |

### 2. 获取 node 所需证书

登录控制面 → Overview 页面，复制「安装 Node」区块中的 server 客户端证书（PEM 格式）。

### 3. 在 node 机器上安装 node

```bash
# 安装最新版
curl -fsSL https://raw.githubusercontent.com/ablate-ai/pulse/main/scripts/install.sh | sh -s -- node

# 安装指定版本
curl -fsSL https://raw.githubusercontent.com/ablate-ai/pulse/main/scripts/install.sh | sh -s -- node v0.1.18

# 重新粘贴证书（已有证书需强制覆盖时）
curl -fsSL https://raw.githubusercontent.com/ablate-ai/pulse/main/scripts/install.sh | sh -s -- --force node
```

执行后脚本会提示粘贴证书，把第 2 步复制的 PEM 内容粘贴进去，输入空行确认后自动继续。

### 4. 启用 HTTPS / Caddy（可选，推荐生产环境）

如需 Trojan 在标准 443 端口运行，在每台节点机器上运行：

```bash
sh <(curl -fsSL https://raw.githubusercontent.com/ablate-ai/pulse/main/scripts/setup-caddy.sh)
```

**脚本做的事：**

1. 检测 443 端口是否已被占用
2. 自动安装 Caddy（apt / dnf / yum，如已安装则跳过）
3. 生成 `/etc/caddy/Caddyfile`，内容为 `import /etc/caddy/pulse.d/*.caddy`
4. 启动/热重载 Caddy

**安装完成后，在面板完成配置：**

1. 进入 **面板 → Caddy 管理**，找到对应节点
2. 点击「配置」，开启「启用 Caddy WS 模式（Trojan）」开关
3. 可选：填写 ACME Email（Let's Encrypt 邮箱）和面板域名（用于面板 HTTPS）
4. 保存后面板自动触发一次路由同步

后续每次应用节点配置（新增/修改 inbound）时，面板会自动更新 `/etc/caddy/pulse.d/<domain>.caddy` 并热重载 Caddy。

**可配置的环境变量（安装脚本）：**

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `ACME_EMAIL` | 空 | Let's Encrypt 账号邮箱（可选，也可事后在面板填写） |
| `CADDYFILE` | `/etc/caddy/Caddyfile` | Caddyfile 路径 |

**架构示意：**

```
客户端
  ├── HTTPS  → :443 (Caddy) → pulse-server 面板
  └── Trojan → wss://<domain>/ws → :443 (Caddy) → sing-box:<inbound_port>
              每个 Trojan inbound 各自路由，配置自动写入 /etc/caddy/pulse.d/<domain>.caddy
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
- server：随机生成端口和管理员密码（可通过环境变量覆盖），若设置 `PULSE_PANEL_DOMAIN` 则写入面板域名
- node：交互式提示粘贴 server 客户端证书 PEM，写入 `/etc/pulse/server_client_cert.pem`
- 注册并启动 systemd 服务（`systemctl enable --now`）
