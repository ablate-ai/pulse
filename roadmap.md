# Pulse Roadmap

目标：在一个 Go 单仓里重写 `Marzban` 和 `Marzban-node`。

状态说明：本文件已按当前仓库实现，并参照 `Marzban` 官方 README 与 `app/routers/*` 能力集重新核对。

说明：

- `[x]` 已完成
- `[ ]` 未完成

## 总目标

- [x] 完成 Go 版控制面，对标 `Marzban`
- [x] 完成 Go 版节点面，对标 `Marzban-node`
- [x] 完成统一的 server/node 通信协议
- [x] 完成 sing-box 本地与远端节点统一管理
- [x] 完成用户、订阅、节点、系统配置、统计核心能力
- [x] 完成可发布、可部署、可升级的单仓工程

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
- [x] 支持节点日志流式输出（sing-box log viewer）
- [x] 支持节点健康检查
- [x] 补节点最小集成测试

## Phase 2: 控制面核心基础

- [x] 建立 `internal/serverapi`
- [x] 建立 `internal/auth`
- [x] 建立 `internal/nodes`
- [x] 建立管理员认证接口
- [x] 建立系统信息接口
- [x] 建立节点新增接口
- [x] 建立节点删除接口
- [x] 建立节点列表与详情接口
- [x] 建立控制面下发节点操作能力
- [x] 补控制面 API 测试

## Phase 3: 数据层与领域模型

- [x] 确定数据库方案（SQLite）
- [x] 建立 `internal/store`（SQLite 实现 + ALTER TABLE 增量迁移）
- [x] 建立初始表结构
- [x] 完成管理员 CRUD
- [x] 完成节点 CRUD
- [x] 完成用户 CRUD
- [x] 完成 inbound CRUD
- [x] 完成 outbound CRUD

## Phase 4: 用户与订阅主链路

- [x] 建立 `internal/users`
- [x] 建立 `internal/subscription`
- [x] 建立 `internal/proxycfg`
- [x] 建立 `internal/inbounds`
- [x] 建立 `internal/outbounds`
- [x] 支持创建用户
- [x] 支持更新用户
- [x] 支持删除用户
- [x] 支持用户状态管理（`active` / `disabled` / `limited` / `expired` / `on_hold`）
- [x] 支持 on_hold 延时自动激活
- [x] 支持流量限制
- [x] 支持到期时间限制
- [x] 支持周期流量重置规则（monthly / weekly / no_reset）
- [x] 支持 VLESS + Reality
- [x] 支持 Trojan（直连 TLS / Caddy WS 模式）
- [x] 支持 Shadowsocks 2022 多用户
- [x] 支持出口绑定（inbound → outbound forwarding，SS / VLESS+Reality）
- [x] 支持订阅链接生成（sing-box JSON 格式）
- [x] 跑通"创建用户 -> 获取订阅 -> 节点生效"
- [x] 补订阅兼容性验证
- [x] 单用户多协议（同节点多 inbound，用户自动获得所有协议订阅链接）
- [x] 订阅响应头（Subscription-Userinfo / Profile-Update-Interval）
- [ ] 支持 Vmess
- [ ] 支持分享链接生成
- [ ] 支持二维码生成
- [ ] 支持用户模板应用

## Phase 5: 统计、任务与自动化

- [x] 建立 `internal/usage`
- [x] 建立 `internal/jobs`
- [x] 支持使用量采集（定时向节点拉取 sing-box traffic）
- [x] 支持定时任务调度（SyncUsage / ResetTraffic / ActivateExpiredOnHold）
- [x] 支持过期用户自动处理
- [x] 支持周期流量自动重置
- [x] 支持节点级别流量统计（Dashboard 概览）
- [x] 支持在线用户数采集（3 分钟内有流量即视为在线）
- [x] 总览流量从节点累计值汇总（不受用户流量重置影响）
- [ ] 支持基础通知机制
- [ ] 支持基础运营报表

## Phase 6: 面板与交互层

- [x] 完成最小 MVP 面板（登录 / 节点 / 用户 / 系统概览）
- [x] 完成登录页
- [x] 完成用户管理页（列表 / 新建 / 编辑 / 批量操作 / 流量展示）
- [x] 完成节点管理页（列表 / 新建 / 删除 / 状态 / 应用配置）
- [x] 完成 Inbound 管理页（多节点创建 / 编辑 / 协议切换 / Host 管理）
- [x] 完成 Outbound 管理页（SS / VLESS+Reality 出口配置）
- [x] 完成 Caddy 管理页（per-node 状态 / WS 模式切换 / Caddyfile 预览 / 面板域名）
- [x] 完成 Dashboard 统计概览（节点流量 / 用户数 / 版本）
- [x] 完成 sing-box 日志查看器（per-node）
- [x] Settings 页展示 Node 客户端证书（供安装 node 时粘贴）
- [ ] 完成系统配置页（核心参数编辑）
- [ ] 完成订阅相关前端交互（二维码 / 分享链接）

## Phase 7: CLI、发布与运维

- [x] 提供 systemd 服务文件
- [x] 注入版本信息
- [x] 提供部署文档
- [x] 提供 Caddy 反代安装脚本（`scripts/setup-caddy.sh`）
- [x] 提供卸载脚本（`scripts/uninstall.sh`）
- [ ] 建立管理 CLI（admin / user / subscription）
- [ ] 支持数据导入导出
- [ ] 构建 Docker 镜像
- [ ] 提供升级文档

## 对照 Marzban 当前差距

- [x] 用户高级状态机：`active` / `disabled` / `limited` / `expired` / `on_hold`
- [x] 到期时间、按周期流量重置、手动重置流量
- [x] inbound / host 管理
- [x] 节点日志 WebSocket 流式转发
- [x] 节点使用量统计
- [x] Caddy 反代 + Trojan WS 模式（443 端口复用）
- [x] 出口转发（SS / VLESS+Reality 落地机绑定）
- [x] 单用户多协议（节点多 inbound 自动覆盖）
- [x] 在线用户统计
- [ ] 多管理员与权限模型
- [ ] 用户模板 CRUD 与批量开通
- [ ] Vmess 支持
- [ ] Clash / ClashMeta / V2Ray JSON / Outline 订阅输出
- [ ] 分享链接与二维码生成
- [ ] 定时任务：usage record 持久化 / 节点历史用量查询
- [ ] Webhook 通知
- [ ] Telegram Bot
- [ ] 管理 CLI
- [ ] 数据导入导出与备份恢复

## 暂缓事项

- [ ] Telegram Bot
- [ ] Discord 集成
- [ ] 多语言支持
- [ ] 高级报表
- [ ] 完全复刻 Marzban 所有边角功能

## Definition of Done

- [x] 每个阶段都有可运行代码
- [x] 每个阶段都有最小测试
- [x] 每个阶段都有验证方式
- [x] 每个阶段都有文档说明
- [x] 每个阶段不依赖手工临时操作才能工作
