# pulse

`pulse` 的目标是在一个 Go 仓库里重写 `Marzban` 与 `Marzban-node`。

当前阶段先完成基础骨架拆分，把控制面和节点面放进统一代码库，后续再逐步迁移用户管理、订阅、sing-box 配置生成、节点控制、统计与后台任务。

## 目录结构

```text
.
├── cmd/pulse         # 总控 CLI，统一启动 server/node
├── cmd/pulse-server  # 控制面服务入口，对应 Marzban
├── cmd/pulse-node    # 节点服务入口，对应 Marzban-node
├── internal/app      # CLI 分发
├── internal/config   # 共享配置
├── internal/server   # 控制面骨架
├── internal/node     # 节点面骨架
├── Marzban           # 上游参考实现
├── Marzban-node      # 上游参考实现
├── go.mod
└── README.md
```

## 运行

```bash
go run ./cmd/pulse help
go run ./cmd/pulse server
go run ./cmd/pulse node
```

## 构建

```bash
go build ./cmd/pulse
go build ./cmd/pulse-server
go build ./cmd/pulse-node
```

## 打包发布

默认不需要手动在本地打包。

推送形如 `v*` 的 Git tag 后，GitHub Actions 会自动：

- 构建 `pulse-server` 与 `pulse-node`
- 生成 Linux `amd64` / `arm64` 发布包
- 生成 `checksums.txt`
- 发布到 GitHub Release

如果只是本地验证打包流程，再手动执行：

```bash
make package-server TARGET_OS=linux TARGET_ARCH=amd64 VERSION=v0.1.0
make package-node TARGET_OS=linux TARGET_ARCH=amd64 VERSION=v0.1.0
make checksums
```

本地产物会输出到 `dist/release/`。

## 安装脚本

安装脚本在 `scripts/install.sh`。

推荐按下面的固定顺序安装：

1. 安装 `server`

```bash
curl -fsSL https://raw.githubusercontent.com/ablate-ai/pulse/main/scripts/install.sh | \
  PULSE_ADMIN_PASSWORD='strong-password' sh -s -- server
```

2. 由管理员从控制面获取安装 node 用的 token

```bash
curl -H "Authorization: Bearer <admin-token>" \
  https://panel.example.com/v1/node/settings
```

3. 在 node 机器上直接安装 `node`

推荐直接让安装脚本从控制面拉取证书：

```bash
curl -fsSL https://raw.githubusercontent.com/ablate-ai/pulse/main/scripts/install.sh | \
  PULSE_SERVER_URL='https://panel.example.com' \
  PULSE_NODE_SETTINGS_TOKEN='<admin-token>' sh -s -- node
```

如果控制面证书暂时不方便直接拉取，脚本也支持在安装时交互粘贴证书内容。执行命令后，直接把整段 PEM 粘贴到终端，脚本会在读取到 `-----END CERTIFICATE-----` 后自动继续：

```bash
curl -fsSL https://raw.githubusercontent.com/ablate-ai/pulse/main/scripts/install.sh | sh -s -- node
```

如果你已经把证书内容放在本地文件里，也可以直接传 PEM 内容：

```bash
curl -fsSL https://raw.githubusercontent.com/ablate-ai/pulse/main/scripts/install.sh | \
  PULSE_NODE_TLS_CLIENT_CERT_PEM="$(cat server_client_cert.pem)" sh -s -- node
```

如果你已经提前把证书保存成文件，也可以继续传文件路径：

```bash
curl -fsSL https://raw.githubusercontent.com/ablate-ai/pulse/main/scripts/install.sh | \
  PULSE_NODE_TLS_CLIENT_CERT_FILE='/etc/pulse/server_client_cert.pem' sh -s -- node
```

当前脚本会：

- 下载 GitHub Release 对应平台的 `tar.gz`
- 安装二进制到 `/usr/local/bin`
- 安装示例配置到 `/etc/pulse`
- `server` 首次启动时自动生成用于访问节点的客户端证书与私钥
- 管理员可通过控制面 `/v1/node/settings` 或 `/v1/node/settings.pem` 获取 node 需要信任的 server 客户端证书
- `node` 安装时支持四种方式：从控制面自动拉取、交互粘贴证书、直接传 PEM 内容、传证书文件路径
- 将安装时提供的管理员密码或 TLS 证书路径写入 `/etc/pulse/*.env`
- 安装 `systemd` 服务
- 自动执行 `systemctl enable --now`

## 建议的迁移顺序

1. 先迁移 `Marzban-node` 的 sing-box 运行时控制与安全通信。
2. 再迁移 `Marzban` 的控制面 API、节点管理和系统设置。
3. 然后补用户、订阅、模板、统计任务。
4. 最后再决定是否重写前端，或先保留现有前端通过新 API 运行。
