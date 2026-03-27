# 233boy sing-box：Caddy + VLESS/Trojan 复用 443 端口方案分析

> 来源：https://github.com/233boy/sing-box/raw/main/install.sh
> 分析日期：2026-03-27

## 核心架构

**本质：Caddy 做 TLS 终止 + HTTP 反向代理路径分发，非 SNI 分流。**

```
客户端 (TLS)
    ↓ :443
Caddy（唯一监听 443，负责 TLS 终止 + Let's Encrypt 自动证书）
    ↓ 明文 WS / H2C（127.0.0.1:随机高位端口）
sing-box inbound（仅监听 127.0.0.1，不处理任何 TLS）
```

---

## sing-box inbound 配置

有域名时，inbound **不包含 tls 字段**，仅监听本地：

```json
{
  "type": "vless",
  "listen": "127.0.0.1",
  "listen_port": 54321,
  "users": [{ "uuid": "..." }],
  "transport": {
    "type": "ws",
    "path": "/随机path",
    "headers": { "host": "example.com" },
    "early_data_header_name": "Sec-WebSocket-Protocol"
  }
}
```

H2 传输时使用 h2c（明文 HTTP/2）：

```json
{
  "type": "vless",
  "listen": "127.0.0.1",
  "listen_port": 54321,
  "transport": { "type": "http", "path": "/随机path" }
}
```

---

## Caddy 配置

**主 Caddyfile：**

```caddyfile
{
  admin off
  http_port 80
  https_port 443
}
import /etc/caddy/233boy/*.conf
```

**每个域名的片段（`/etc/caddy/233boy/example.com.conf`）：**

```caddyfile
# WS 传输
example.com:443 {
    reverse_proxy /vless-path  127.0.0.1:54321
    reverse_proxy /trojan-path 127.0.0.1:54322
    # 无匹配 → 伪装网站（.add 文件追加）
    reverse_proxy https://www.somewebsite.com {
        header_up Host {upstream_hostport}
    }
}

# H2 传输（h2c 转发）
example.com:443 {
    reverse_proxy /path h2c://127.0.0.1:54321 {
        transport http {
            tls_insecure_skip_verify
        }
    }
}
```

---

## Trojan 的特殊处理

| 场景 | TLS 处理方 | inbound 配置 |
|---|---|---|
| Trojan + 域名（经 Caddy） | Caddy（Let's Encrypt） | 无 tls 字段，走 WS/H2 传输 |
| Trojan 无域名（裸 TCP） | sing-box 自签证书 | 带 tls 字段，`/etc/sing-box/bin/tls.cer` |

---

## 流量分发流程

1. 客户端连接 `example.com:443`
2. Caddy 完成 TLS 握手（Let's Encrypt 证书），解密流量
3. Caddy 按 URL path 匹配 `reverse_proxy` 规则：
   - 路径命中 → 转发到对应的 `127.0.0.1:<inbound_port>`
   - 无匹配 → 转发到伪装网站（防止主动探测）
4. sing-box inbound 收到明文 WS/H2C 流量，执行协议处理

---

## 各层职责总结

| 层 | 负责方 | 职责 |
|---|---|---|
| 443 TLS 终止 | Caddy | 自动申请/续期 Let's Encrypt 证书，解密 HTTPS |
| 流量路由 | Caddy | 按 URL path 区分不同 inbound |
| 协议处理 | sing-box | 在 127.0.0.1 明文处理 VLESS/Trojan/VMess |
| 伪装站 | Caddy | 无匹配时反代真实网站，防主动探测 |

---

## 与 SNI 分流的区别

| 方案 | 分流层 | TLS 处理 | 适用场景 |
|---|---|---|---|
| **此方案（path 分发）** | HTTP 应用层 | Caddy 统一终止 | 所有协议共用同一域名 |
| SNI 分流 | TLS 握手层 | 各自处理 | 不同域名/子域名分流到不同服务 |
| 端口转发 | TCP 层 | 各自处理 | 无域名场景 |

sing-box 本体在此架构中**不处理任何 TLS**（REALITY/QUIC 等协议除外，它们不走 Caddy）。
