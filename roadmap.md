# Pulse Roadmap

目标：在一个 Go 单仓里重写 `Marzban` 和 `Marzban-node`。

状态说明：本文件已按当前仓库实现，并参照 `Marzban` 官方 README 与 `app/routers/*` 能力集重新核对。

说明：

- `[x]` 已完成
- `[ ]` 未完成

## 总目标

- [ ] 完成 Go 版控制面，对标 `Marzban`
- [ ] 完成 Go 版节点面，对标 `Marzban-node`
- [ ] 完成统一的 server/node 通信协议
- [ ] 完成 sing-box 本地与远端节点统一管理
- [ ] 完成用户、订阅、节点、系统配置、统计核心能力
- [ ] 完成可发布、可部署、可升级的单仓工程

## Phase 0: 仓库基础骨架

- [x] 建立 Go 单仓基础结构
- [x] 保留 `cmd/pulse` 总控入口
- [x] 新增 `cmd/pulse-server` 控制面入口
- [x] 新增 `cmd/pulse-node` 节点面入口
- [x] 建立 `internal/config` 共享配置骨架
- [x] 建立 `internal/server` 控制面骨架
- [x] 建立 `internal/node` 节点面骨架
- [x] 提供基础 `health/info` HTTP 接口
- [x] 更新 README，明确仓库重写目标
- [x] 验证 `go build ./cmd/pulse ./cmd/pulse-server ./cmd/pulse-node`
- [ ] 建立统一日志组件
- [ ] 建立配置加载与校验机制
- [x] 建立 Makefile 或任务脚本
- [ ] 建立本地开发用 docker compose
- [x] 建立基础 CI

## Phase 1: 节点面最小闭环

- [x] 建立 `internal/singbox`
- [x] 建立 `internal/nodeapi`
- [x] 建立 `internal/nodeauth`
- [x] 建立 `internal/cert`
- [x] 节点启动时加载配置
- [x] 节点支持证书生成或加载
- [x] 节点支持受控鉴权
- [x] 定义 server -> node 第一版控制协议
- [x] 支持下发 sing-box 配置
- [x] 支持启动 sing-box
- [x] 支持停止 sing-box
- [x] 支持重启 sing-box
- [x] 支持读取 sing-box 版本
- [ ] 支持节点日志流式输出
- [x] 支持节点健康检查
- [x] 补节点最小集成测试

## Phase 2: 控制面核心基础

- [x] 建立 `internal/serverapi`
- [x] 建立 `internal/auth`
- [ ] 建立 `internal/system`
- [x] 建立 `internal/nodes`
- [ ] 建立 `internal/configstore`
- [x] 建立管理员认证接口
- [x] 建立系统信息接口
- [x] 建立节点新增接口
- [ ] 建立节点更新接口
- [x] 建立节点删除接口
- [x] 建立节点列表与详情接口
- [ ] 建立节点状态同步机制
- [x] 建立控制面下发节点操作能力
- [x] 补控制面 API 测试

## Phase 3: 数据层与领域模型

- [x] 确定数据库方案
- [ ] 确定 migration 方案
- [ ] 建立 `internal/domain`
- [x] 建立 `internal/store`
- [ ] 建立 `internal/repository`
- [ ] 建立 `internal/migrate`
- [ ] 定义管理员领域模型
- [ ] 定义用户领域模型
- [ ] 定义节点领域模型
- [ ] 定义入站与主机领域模型
- [ ] 定义用户模板领域模型
- [ ] 定义系统配置领域模型
- [ ] 定义订阅状态模型
- [ ] 定义使用量与在线状态模型
- [x] 建立初始表结构
- [ ] 建立 migration 执行入口
- [ ] 完成管理员 CRUD
- [ ] 完成节点 CRUD
- [ ] 完成用户 CRUD

## Phase 4: 用户与订阅主链路

- [x] 建立 `internal/users`
- [x] 建立 `internal/subscription`
- [x] 建立 `internal/proxycfg`
- [ ] 建立 `internal/templates`
- [x] 支持创建用户
- [x] 支持更新用户（PUT /v1/users/{id}）
- [x] 支持删除用户
- [x] 支持用户状态管理（active/disabled/limited/expired/on_hold 状态机）
- [x] 支持流量限制
- [x] 支持到期时间限制（expire_at 字段，EffectiveStatus 自动计算）
- [x] 支持周期流量重置规则（data_limit_reset_strategy 字段 + ResetTraffic 定时 job）
- [ ] 支持单用户多协议配置
- [x] 支持 Vmess（proxycfg + vmess:// 订阅链接）
- [x] 支持 VLESS
- [x] 支持 Trojan
- [x] 支持 Shadowsocks
- [x] 支持订阅链接生成
- [ ] 支持用户自助订阅端点（`/sub/{token}` 公开 endpoint，用户无需管理员凭证）
- [ ] 支持订阅 Userinfo Header（`Subscription-Userinfo: upload=x; download=x; total=x; expire=x`）
- [ ] 支持分享链接生成
- [ ] 支持二维码生成
- [ ] 支持用户模板应用
- [x] 跑通“创建用户 -> 获取订阅 -> 节点生效”
- [x] 补订阅兼容性验证

## Phase 5: 统计、任务与自动化

- [x] 建立 `internal/usage`
- [x] 建立 `internal/jobs`
- [ ] 建立 `internal/online`
- [ ] 建立 `internal/notify`
- [x] 支持使用量采集
- [ ] 支持在线状态采集
- [x] 支持定时任务调度（Scheduler + Job + 启动时随 context 运行）
- [ ] 支持过期用户自动处理（EffectiveStatus 已实时计算，定时重载待补）
- [x] 支持周期流量自动重置（ResetTraffic job，每分钟检查）
- [ ] 支持基础通知机制
- [ ] 支持基础运营报表
- [x] 补任务可靠性测试（ShouldResetTraffic + SyncUsage + ResetTraffic 全覆盖）

## Phase 6: 面板与交互层

- [ ] 明确前端策略：兼容现有面板或重做
- [ ] 如果兼容现有面板，整理兼容 API 清单
- [ ] 如果兼容现有面板，实现最小兼容接口集
- [ ] 如果重做前端，确定技术栈和目录结构
- [x] 完成最小 MVP 面板（登录/节点新增/用户新增/系统概览）
- [x] 完成登录页
- [x] 完成用户管理页（状态徽章、流量进度条、编辑 Modal、状态/关键词过滤）
- [x] 完成节点管理页（重启/删除、在线状态徽章）
- [x] 迁移至 HTMX + Tailwind CSS（替换 Go WASM 面板）
- [x] 完成订阅相关交互（一键获取订阅链接）
- [ ] **用户：重置单个用户流量**
- [ ] **用户：撤销订阅 token（revoke sub_token）**
- [ ] **用户：创建/编辑时选择关联 Inbound**
- [ ] **用户：备注字段（note）**
- [ ] **用户：分页 / 虚拟滚动（用户量大时性能）**
- [ ] **用户：批量操作（全选、批量删除/禁用）**
- [ ] **用户：订阅二维码弹窗**
- [ ] **Inbound 管理页**（列表、新增、编辑、删除）
- [ ] **节点编辑**（修改名称/地址）
- [ ] **操作反馈 Toast 通知**（当前容器存在但无触发逻辑）
- [ ] **按钮 loading 状态**（防重复提交）
- [ ] 完成系统配置页（订阅域名、管理员密码修改）

## Phase 7: CLI、发布与运维

- [ ] 建立管理 CLI
- [ ] 支持配置检查命令
- [ ] 支持数据导入导出
- [ ] 构建 `pulse-server` Docker 镜像
- [ ] 构建 `pulse-node` Docker 镜像
- [x] 提供 systemd 服务文件
- [x] 注入版本信息
- [x] 提供部署文档
- [ ] 提供升级文档
- [ ] 提供回滚文档

## 近期优先级

- [x] 冻结第一版 server/node API 契约
- [x] 完成 `internal/singbox` 本地进程控制
- [ ] 完成节点鉴权与证书方案
- [x] 完成节点控制主链路
- [ ] 确定数据库与 migration 方案
- [x] 完成管理员认证
- [x] 完成节点管理接口
- [x] 完成用户与订阅最小闭环

## 对照 Marzban 当前差距

> 参照 Marzban `app/routers/*`、`app/models/*`、`app/jobs/*` 能力集全面核对（2026-03-24）。
> 优先级：P0 必须 → P1 重要 → P2 中等 → P3 锦上添花

### P0：核心商业逻辑（阻塞 MVP）

- [x] **用户状态机**：`active` / `disabled` / `limited` / `expired` / `on_hold` 五态；`EffectiveStatus()` 在读取时实时计算，无需写库 job
- [x] **流量重置策略**：`no_reset` / `day` / `week` / `month` / `year`；`ResetTraffic` 定时 job 每分钟检查并重置
- [x] **用户到期管理**：`expire_at` 字段 + `EffectiveStatus()` 自动计算 expired 状态；`on_hold_expire_duration` / `on_hold_timeout` 暂缓
- [x] **多协议支持**：VMess（UUID + vmess:// 标准 base64 链接）、Trojan（password）、Shadowsocks（method）完整支持；sing-box 配置统一由 `BuildSingboxConfig` 生成；单用户多协议绑定暂缓
- [x] **Inbound / Host 管理**：`/v1/inbounds` + `/v1/hosts` CRUD；Host 含 `remark/address/port/sni/host/path/security/alpn/fingerprint/allow_insecure/mux_enable` 字段；SQLite 持久化；用户级 `excluded_inbounds` 暂缓

### P1：重要功能

- [x] **定时任务体系**：`SyncUsage`（1 分钟）/ `ResetTraffic`（1 分钟，按 DataLimitResetStrategy）；Scheduler 随 server context 启动；`review_users` / `remove_expired_users` 通过 EffectiveStatus 运行时计算替代
- [ ] **Webhook 通知**：15+ 事件类型（`user_created/updated/deleted/limited/expired/disabled/enabled` 等）+ 重试机制 + `WEBHOOK_SECRET` 签名验证
- [ ] **多管理员与权限模型**：多 Admin + `is_sudo` 权限区分 + Admin 下用户隔离 + `set-owner` 转移所有权 + 批量禁用/激活所属用户
- [ ] **NextPlan 系统**：用户订阅到期/流量耗尽后自动切换下一计划（`add_remaining_traffic`、`fire_on_either` 字段）；`/user/{username}/active-next` 端点

### P2：中等优先级

- [ ] **用户模板系统**：`/user_template` CRUD；模板包含 `data_limit/expire_duration/inbounds/username_prefix/suffix`
- [ ] **使用量历史记录**：`NodeUserUsage` / `NodeUsage` 时序表；`/user/{username}/usage` 和 `/node/{id}/usage` 按日期范围查询；重置时写入 `UserUsageResetLogs`
- [ ] **批量用户操作**：`/users/reset`（全量重置流量）、`/users/expired`（查询/删除过期用户）
- [ ] **系统统计端点**：CPU/内存 + 用户分状态计数 + 在线用户数（24h）+ 实时带宽
- [ ] **节点资源监控**：per-node CPU/内存/网络实时指标，在节点详情页展示
- [ ] **用户查询过滤**：`/users` 支持 `username/search/status/admin/sort/offset/limit` 参数
- [ ] **用户在线状态追踪**：`online_at`（最后在线）+ `last_status_change` 字段
- [ ] **用户备注字段**：`note` 字段，支持记录用户信息（联系方式、备注）；API + 面板均可编辑
- [ ] **订阅撤销**：`/user/{username}/revoke_sub`；独立订阅令牌（非用户 ID）；`sub_updated_at` / `sub_last_user_agent` / `sub_revoked_at` 字段
- [ ] **通知提醒去重**：`NotificationReminder` 表；支持 `expiration_date` / `data_usage` 类型 + 阈值（如 80% 流量）
- [ ] **节点日志 WebSocket 流式转发**：`/node/{id}/logs`
- [ ] **Core 管理端点**：`/core/config`（读写）、`/core/restart`、`/core/logs`（WebSocket）
- [ ] **节点更新接口** + 节点状态持久化 + 节点历史使用量统计

### P3：锦上添花

- [ ] **高级订阅格式**：Sing-box / V2Ray JSON / Outline；User-Agent 自适应；HTML 订阅页
- [ ] **高级代理配置**：ALPN、TLS Fingerprint、Fragment、Noise、XTLS flow、REALITY
- [ ] **Telegram Bot 通知**：管理员绑定 `telegram_id`，接收用户操作事件
- [ ] **Discord Webhook 通知**
- [ ] **Admin 使用量追踪**：`users_usage` 字段 + `/admin/usage` 端点 + `AdminUsageLogs` 历史
- [ ] **分享链接与二维码生成**
- [ ] **登录审计日志**：记录登录事件（成功/失败 + IP）
- [ ] **数据导入导出与备份恢复**
- [ ] **多语言与完整前端面板**

## 暂缓事项

- [ ] Telegram Bot
- [ ] Discord 集成
- [ ] 多语言支持
- [ ] 高级报表
- [ ] 完全复刻 Marzban 所有边角功能

## Definition of Done

- [ ] 每个阶段都有可运行代码
- [ ] 每个阶段都有最小测试
- [ ] 每个阶段都有验证方式
- [ ] 每个阶段都有文档说明
- [ ] 每个阶段不依赖手工临时操作才能工作
