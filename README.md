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

本地生成 Linux 发布包：

```bash
make package-server TARGET_OS=linux TARGET_ARCH=amd64 VERSION=v0.1.0
make package-node TARGET_OS=linux TARGET_ARCH=amd64 VERSION=v0.1.0
make checksums
```

产物会输出到 `dist/release/`。

## 安装脚本

安装脚本在 `scripts/install.sh`。

推荐按下面的固定顺序安装：

1. 安装 `server`

```bash
curl -fsSL https://raw.githubusercontent.com/ablate-ai/pulse/main/scripts/install.sh | \
  PULSE_ADMIN_PASSWORD='strong-password' sh -s -- server
```

2. 由管理员从控制面的 `/v1/node/settings` 获取 node 需要信任的证书

```bash
curl -H "Authorization: Bearer <admin-token>" \
  https://panel.example.com/v1/node/settings
```

3. 将证书保存到 node 机器后，再安装 `node`

```bash
curl -fsSL https://raw.githubusercontent.com/ablate-ai/pulse/main/scripts/install.sh | \
  PULSE_NODE_TLS_CLIENT_CERT_FILE='/etc/pulse/server_client_cert.pem' sh -s -- node
```

当前脚本会：

- 下载 GitHub Release 对应平台的 `tar.gz`
- 安装二进制到 `/usr/local/bin`
- 安装示例配置到 `/etc/pulse`
- `server` 首次启动时自动生成用于访问节点的客户端证书与私钥
- 管理员可通过控制面 `/v1/node/settings` 获取 node 需要信任的 server 客户端证书
- 将安装时提供的管理员密码或 TLS 证书路径写入 `/etc/pulse/*.env`
- 安装 `systemd` 服务
- 自动执行 `systemctl enable --now`

## 建议的迁移顺序

1. 先迁移 `Marzban-node` 的 sing-box 运行时控制与安全通信。
2. 再迁移 `Marzban` 的控制面 API、节点管理和系统设置。
3. 然后补用户、订阅、模板、统计任务。
4. 最后再决定是否重写前端，或先保留现有前端通过新 API 运行。
