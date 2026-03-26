# Pulse — 项目说明

## 设计决策

### 订阅格式
**不支持多客户端订阅格式**（Clash / Clash-Meta / SingBox / V2RayN / Shadowsocks / Outline 等）。

订阅端点 `/sub/:token` 只返回 sing-box JSON 格式，由客户端自行处理。用户使用支持 sing-box 的客户端（如 Hiddify、sing-box 官方客户端）直接导入订阅链接。
