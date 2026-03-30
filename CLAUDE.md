# Pulse — 项目说明

## 项目概述

Go 重写版 Marzban + Marzban-node，单仓双进制：
- `pulse-server`：控制面（HTTP API + 管理面板 + 订阅服务）
- `pulse-node`：节点面（管理 sing-box 进程，接受控制面 RPC 指令）

---

## 架构

```
pulse-server
  ├── REST API (/v1/*)           # serverapi/
  ├── 管理面板 (/panel/*)        # panel/ — HTMX + Go template，无 JS 构建步骤
  ├── 订阅端点 (/sub/:token)     # serverapi/sub.go
  ├── SQLite 持久化              # store/sqlite/
  └── 定时调度器                 # jobs/scheduler.go

pulse-node
  ├── RPC API (/v1/node/*)       # nodeapi/
  ├── sing-box 进程管理          # singbox/manager.go
  └── Caddy 路由热重载           # nodeapi/caddy.go
```

控制面与节点通过 mTLS 通信：
- 控制面在启动时自签 `server_client_cert.pem` / `server_client_key.pem`
- 节点启动时自签自身 TLS 证书，并在配置中信任控制面的客户端证书
- 安装节点时需将 `server_client_cert.pem` 内容粘贴到节点机器

---

## 关键设计决策

### 订阅格式
只返回 sing-box JSON 格式，不支持 Clash / V2Ray / Outline 等多格式。
端点：`GET /sub/:token`

### 数据库
SQLite，通过 `ALTER TABLE` 增量迁移，无迁移框架。
新增字段在 `store/sqlite/sqlite.go` 的 `migrateXxxTable()` 方法中处理。

### 前端
HTMX + Tailwind CSS（CDN），Go template 服务端渲染，**无 JS 构建步骤**。
模板全部在 `internal/panel/templates/`，通过 `//go:embed` 嵌入二进制。
模板函数（`formatBytes`、`addInt64` 等）在 `panel/handler.go` 的 `templateFuncs()` 中注册。

### 用户状态
通过 `EffectiveStatus()` 计算（非存储值）：
- `active` — 正常
- `disabled` — 手动禁用
- `limited` — 超过流量限额
- `expired` — 超过到期时间
- `on_hold` — 等待激活（`OnHoldExpireAt` 到期后自动转 `active`）

### 节点流量
节点流量只增不减（`AddTraffic` 累加），用户流量重置只清零用户表，
仪表盘总流量从节点累计值汇总，不受用户重置影响。

### 单用户多协议
一个用户对应一条 `user_inbounds` 记录（per user+node），`uuid` 和 `secret` 共用。
`BuildSingboxConfig` 将所有活跃用户写入节点上所有 inbound，用户自动获得所有协议的订阅链接。

---

## 配置（环境变量）

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `PULSE_SERVER_ADDR` | `:8080` | 控制面监听地址 |
| `PULSE_NODE_ADDR` | `:8081` | 节点监听地址 |
| `PULSE_DB_PATH` | `./pulse.db` | SQLite 数据库路径 |
| `PULSE_ADMIN_USERNAME` | `admin` | 管理员用户名 |
| `PULSE_ADMIN_PASSWORD` | `admin` | 管理员密码 |
| `PULSE_SERVER_NODE_CLIENT_CERT_FILE` | `./server_client_cert.pem` | 控制面客户端证书（节点安装时需粘贴此文件内容） |
| `PULSE_SERVER_NODE_CLIENT_KEY_FILE` | `./server_client_key.pem` | 控制面客户端私钥 |

---

## 开发

```bash
make dev      # 同时启动 server(:8080) + node(:8081)，使用开发凭据
make stop     # 停止
make test     # 运行全部测试
make build    # 编译三个二进制
```

默认账号 `admin` / `admin123`，面板 `http://localhost:8080/panel`。

---

## 定时任务

三个任务，均每 **1 分钟**运行一次（`server/server.go`）：

| 名称 | 函数 | 作用 |
|------|------|------|
| `sync-usage` | `jobs.SyncUsage` | 从节点拉取流量 delta，更新用户/节点字节数，写日统计桶，状态变化时重下发配置 |
| `reset-traffic` | `jobs.ResetTraffic` | 按策略（day/week/month/year）重置用户流量，清零 SyncedBytes 游标 |
| `activate-on-hold` | `jobs.ActivateExpiredOnHold` | 将到期的 on_hold 用户激活 |

调度器为手写 ticker，见 `jobs/scheduler.go`，无第三方依赖。

---

## 测试

- 业务逻辑层（`jobs`、`usage`、`proxycfg`）有单元测试
- `store/sqlite` 有集成测试（真实 SQLite）
- 节点/服务端 API 有集成测试（httptest）
- 内存 Store（`nodes/memory.go`、`users/memory.go`、`inbounds/memory.go`）用于测试隔离

新增 store 接口方法时，必须同时在内存 Store 中实现（否则编译失败）。

---

## 常见改动模式

### 新增数据库字段
1. 在 `store/sqlite/sqlite.go` 的建表语句中加字段（`IF NOT EXISTS` 幂等）
2. 在对应的 `migrateXxxTable()` 中加 `ALTER TABLE ... ADD COLUMN`（检查列是否存在后才执行）

### 新增 Panel 路由
在 `panel/handler.go` 的 `Register()` 方法中注册，handler 方法命名为 `xxxPage` / `xxxPartial`。

### 新增模板函数
在 `panel/handler.go` 的 `templateFuncs()` 中注册。

### 新增节点 Store 接口方法
同时修改：`nodes/store.go`（接口）、`nodes/memory.go`（stub）、`store/sqlite/nodes.go`（实现）。
