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
- [ ] 支持更新用户
- [x] 支持删除用户
- [ ] 支持用户状态管理
- [x] 支持流量限制
- [ ] 支持到期时间限制
- [ ] 支持周期流量重置规则
- [ ] 支持单用户多协议配置
- [ ] 支持 Vmess
- [x] 支持 VLESS
- [x] 支持 Trojan
- [x] 支持 Shadowsocks
- [x] 支持订阅链接生成
- [ ] 支持分享链接生成
- [ ] 支持二维码生成
- [ ] 支持用户模板应用
- [x] 跑通“创建用户 -> 获取订阅 -> 节点生效”
- [x] 补订阅兼容性验证

## Phase 5: 统计、任务与自动化

- [x] 建立 `internal/usage`
- [ ] 建立 `internal/jobs`
- [ ] 建立 `internal/online`
- [ ] 建立 `internal/notify`
- [x] 支持使用量采集
- [ ] 支持在线状态采集
- [ ] 支持定时任务调度
- [ ] 支持过期用户自动处理
- [ ] 支持周期流量自动重置
- [ ] 支持基础通知机制
- [ ] 支持基础运营报表
- [ ] 补任务可靠性测试

## Phase 6: 面板与交互层

- [ ] 明确前端策略：兼容现有面板或重做
- [ ] 如果兼容现有面板，整理兼容 API 清单
- [ ] 如果兼容现有面板，实现最小兼容接口集
- [ ] 如果重做前端，确定技术栈和目录结构
- [x] 完成最小 MVP 面板（登录/节点新增/用户新增/系统概览）
- [x] 完成登录页
- [ ] 完成用户管理页
- [ ] 完成节点管理页
- [ ] 完成系统配置页
- [ ] 完成订阅相关交互

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

- [ ] 多管理员与权限模型
- [ ] 用户模板 CRUD、模板应用与批量开通
- [ ] 用户高级状态机：`active` / `disabled` / `limited` / `expired` / `on_hold`
- [ ] 到期时间、按周期流量重置、手动重置流量、订阅撤销与轮换
- [ ] 单用户多协议
- [ ] Vmess 支持
- [ ] Clash / ClashMeta / Sing-box / V2Ray JSON / Outline 订阅输出
- [ ] 分享链接与二维码生成
- [ ] 入站、出站、host、fallback 与系统核心配置的读取和编辑
- [ ] TLS / REALITY / 多入站同端口等高级配置管理
- [ ] 节点更新、重连、状态持久化、节点使用量统计
- [ ] 节点日志 WebSocket 流式转发
- [ ] 用户历史用量、节点历史用量、在线用户统计
- [ ] 定时任务体系：usage record / review users / expired cleanup / reset
- [ ] Webhook 通知
- [ ] Telegram Bot
- [ ] Discord 集成
- [ ] 管理 CLI（admin / user / subscription）
- [ ] 数据导入导出与备份恢复
- [ ] 多语言与完整前端面板

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
