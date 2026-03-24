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
└── scripts/install.sh    # 生产安装脚本
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

### 安装脚本做了什么

- 从 GitHub Release 下载对应平台（linux/amd64 或 linux/arm64）的 tar.gz
- 安装二进制到 `/usr/local/bin`
- 首次安装时写入示例配置到 `/etc/pulse/*.env`（已有配置不覆盖）
- 注册并启动 systemd 服务（`systemctl enable --now`）
