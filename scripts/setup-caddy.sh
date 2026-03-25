#!/bin/sh
# setup-caddy.sh — 为 pulse（A2 架构）配置 Caddy 反代
#
# 架构说明：
#   Caddy 监听 :443，按需反代：
#     - PANEL_DOMAIN  → 127.0.0.1:PANEL_PORT  （面板 HTTPS，可选）
#     - TROJAN_DOMAIN → 127.0.0.1:WS_PORT /ws （Trojan WS，可选，可与面板同域）
#   至少需要指定其中一个。
#
# 用法：
#   # 仅面板 HTTPS
#   PANEL_DOMAIN=panel.example.com sh setup-caddy.sh
#
#   # 仅 Trojan WS（添加新 inbound 时）
#   TROJAN_DOMAIN=nc.example.com sh setup-caddy.sh
#
#   # 两者同域
#   PANEL_DOMAIN=example.com TROJAN_DOMAIN=example.com sh setup-caddy.sh
#
#   # 两者不同域
#   PANEL_DOMAIN=panel.example.com TROJAN_DOMAIN=nc.example.com sh setup-caddy.sh
#
# 环境变量：
#   PANEL_DOMAIN   面板对外域名（可选）
#   TROJAN_DOMAIN  Trojan inbound 域名（可选，与 PANEL_DOMAIN 可相同可不同）
#   WS_PORT        sing-box Trojan WS 本地端口（PULSE_SINGBOX_WS_PORT），默认 10443
#   PANEL_PORT     pulse-server 监听端口，默认自动从配置文件读取，兜底 8080
#   CADDYFILE      Caddyfile 路径，默认 /etc/caddy/Caddyfile
#   ACME_EMAIL     Let's Encrypt 账号邮箱（可选）

set -eu

# ── 默认值 ─────────────────────────────────────────────────────────────────────
WS_PORT="${WS_PORT:-10443}"
CADDYFILE="${CADDYFILE:-/etc/caddy/Caddyfile}"
PULSE_ENV_FILE="/etc/pulse/pulse-server.env"

# ── 工具函数 ───────────────────────────────────────────────────────────────────
info()  { printf '\033[32m[INFO]\033[0m  %s\n' "$*"; }
warn()  { printf '\033[33m[WARN]\033[0m  %s\n' "$*"; }
error() { printf '\033[31m[ERROR]\033[0m %s\n' "$*" >&2; exit 1; }

# ── 从 pulse-server.env 读取面板端口 ──────────────────────────────────────────
read_panel_port() {
  # 优先使用环境变量显式传入的值
  if [ -n "${PANEL_PORT:-}" ]; then
    return
  fi
  PANEL_PORT="8080"  # 兜底默认值
  [ -f "$PULSE_ENV_FILE" ] || return
  ADDR=$(grep '^PULSE_SERVER_ADDR=' "$PULSE_ENV_FILE" 2>/dev/null | cut -d= -f2 | tr -d '"' | tr -d "'")
  [ -n "$ADDR" ] || return
  # PULSE_SERVER_ADDR 格式为 :8080 或 0.0.0.0:8080
  PORT=$(printf '%s' "$ADDR" | sed 's/.*://')
  [ -n "$PORT" ] && [ "$PORT" != "$ADDR" ] && PANEL_PORT="$PORT"
  info "面板端口: $PANEL_PORT（来自 $PULSE_ENV_FILE）"
}

# ── 参数校验 ───────────────────────────────────────────────────────────────────
validate_args() {
  [ -n "${PANEL_DOMAIN:-}" ] || [ -n "${TROJAN_DOMAIN:-}" ] || \
    error "至少需要指定 PANEL_DOMAIN 或 TROJAN_DOMAIN"

  # TROJAN_DOMAIN 存在时才需要 WS_PORT（始终有默认值，这里只做提示）
  if [ -n "${TROJAN_DOMAIN:-}" ]; then
    info "Trojan 域名: $TROJAN_DOMAIN → 127.0.0.1:$WS_PORT"
  fi
  if [ -n "${PANEL_DOMAIN:-}" ]; then
    info "面板域名: $PANEL_DOMAIN → 127.0.0.1:$PANEL_PORT"
  fi
}

# ── 检查 Caddy 是否安装 ────────────────────────────────────────────────────────
check_caddy() {
  if ! command -v caddy >/dev/null 2>&1; then
    warn "未找到 caddy，尝试自动安装..."
    install_caddy
  fi
  CADDY_VERSION=$(caddy version 2>/dev/null | head -1 || echo "unknown")
  info "Caddy 版本: $CADDY_VERSION"
}

install_caddy() {
  if command -v apt-get >/dev/null 2>&1; then
    apt-get install -y debian-keyring debian-archive-keyring apt-transport-https curl 2>/dev/null || true
    curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' \
      | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
    curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' \
      | tee /etc/apt/sources.list.d/caddy-stable.list
    apt-get update
    apt-get install -y caddy
  elif command -v dnf >/dev/null 2>&1; then
    dnf install -y 'dnf-command(copr)' 2>/dev/null || true
    dnf copr enable -y @caddy/caddy
    dnf install -y caddy
  elif command -v yum >/dev/null 2>&1; then
    yum install -y yum-utils 2>/dev/null || true
    yum-config-manager --add-repo https://copr.fedorainfracloud.org/coprs/g/caddy/caddy/repo/epel-8/g-caddy-caddy-epel-8.repo
    yum install -y caddy
  else
    error "无法自动安装 Caddy，请手动安装后重试: https://caddyserver.com/docs/install"
  fi
}

# ── 检查 443 端口 ──────────────────────────────────────────────────────────────
check_port_443() {
  # 如果 caddy 已经占用 443，则跳过（稍后热重载即可）
  if systemctl is-active --quiet caddy 2>/dev/null; then
    return
  fi
  if ss -tlnp 2>/dev/null | grep -q ':443 ' || \
     netstat -tlnp 2>/dev/null | grep -q ':443 '; then
    OCCUPANT=$(ss -tlnp 2>/dev/null | grep ':443 ' | awk '{print $NF}' | head -1 || true)
    error "端口 443 已被占用（${OCCUPANT:-未知进程}），请先停止该进程再运行"
  fi
}

# ── 生成 Caddyfile ─────────────────────────────────────────────────────────────
write_caddyfile() {
  CADDYFILE_DIR=$(dirname "$CADDYFILE")
  mkdir -p "$CADDYFILE_DIR"

  # 备份现有配置
  if [ -f "$CADDYFILE" ]; then
    BACKUP="${CADDYFILE}.bak.$(date +%Y%m%d%H%M%S)"
    cp "$CADDYFILE" "$BACKUP"
    warn "已备份原 Caddyfile 至 $BACKUP"
  fi

  # 文件头
  printf '# Pulse — 由 setup-caddy.sh 生成\n# 生成时间: %s\n\n' \
    "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" > "$CADDYFILE"

  # 全局块（仅在有邮箱时写入）
  if [ -n "${ACME_EMAIL:-}" ]; then
    printf '{\n\temail %s\n}\n\n' "$ACME_EMAIL" >> "$CADDYFILE"
  fi

  # Trojan 独立域名块（TROJAN_DOMAIN 非空且与面板域名不同）
  if [ -n "${TROJAN_DOMAIN:-}" ] && [ "${TROJAN_DOMAIN}" != "${PANEL_DOMAIN:-}" ]; then
    printf '%s {\n' "$TROJAN_DOMAIN" >> "$CADDYFILE"
    printf '\t# Trojan WebSocket — Caddy v2 自动处理 WS 升级\n' >> "$CADDYFILE"
    printf '\thandle /ws {\n\t\treverse_proxy 127.0.0.1:%s\n\t}\n' "$WS_PORT" >> "$CADDYFILE"
    printf '}\n\n' >> "$CADDYFILE"
  fi

  # 面板域名块（PANEL_DOMAIN 非空）
  if [ -n "${PANEL_DOMAIN:-}" ]; then
    printf '%s {\n' "$PANEL_DOMAIN" >> "$CADDYFILE"
    # 若 Trojan 与面板同域，在面板块内加 /ws 路由
    if [ "${TROJAN_DOMAIN:-}" = "$PANEL_DOMAIN" ]; then
      printf '\t# Trojan WebSocket — Caddy v2 自动处理 WS 升级\n' >> "$CADDYFILE"
      printf '\thandle /ws {\n\t\treverse_proxy 127.0.0.1:%s\n\t}\n\n' "$WS_PORT" >> "$CADDYFILE"
    fi
    printf '\t# 面板 API 及前端\n' >> "$CADDYFILE"
    printf '\thandle {\n\t\treverse_proxy 127.0.0.1:%s\n\t}\n' "$PANEL_PORT" >> "$CADDYFILE"
    printf '}\n' >> "$CADDYFILE"
  fi

  info "Caddyfile 已写入: $CADDYFILE"
}

# ── 更新 pulse-server 环境变量 ─────────────────────────────────────────────────
update_pulse_env() {
  # 只有配置了 Trojan 才需要写 PULSE_SINGBOX_WS_PORT
  [ -n "${TROJAN_DOMAIN:-}" ] || return
  [ -f "$PULSE_ENV_FILE" ] || return

  if grep -q '^PULSE_SINGBOX_WS_PORT=' "$PULSE_ENV_FILE" 2>/dev/null; then
    sed -i "s|^PULSE_SINGBOX_WS_PORT=.*|PULSE_SINGBOX_WS_PORT=${WS_PORT}|" "$PULSE_ENV_FILE"
  else
    printf '\nPULSE_SINGBOX_WS_PORT=%s\n' "$WS_PORT" >> "$PULSE_ENV_FILE"
  fi
  info "已设置 PULSE_SINGBOX_WS_PORT=${WS_PORT} 在 $PULSE_ENV_FILE"
}

# ── 启动/重载 Caddy ────────────────────────────────────────────────────────────
reload_caddy() {
  if systemctl is-active --quiet caddy 2>/dev/null; then
    caddy reload --config "$CADDYFILE" --adapter caddyfile
    info "Caddy 已热重载"
  elif systemctl is-enabled --quiet caddy 2>/dev/null; then
    systemctl start caddy
    info "Caddy 已启动"
  else
    systemctl enable --now caddy
    info "Caddy 已启用并启动"
  fi
}

# ── 重启 pulse-server 使新端口变量生效 ─────────────────────────────────────────
restart_pulse_server() {
  # 只有更新了 env 文件才需要重启
  [ -n "${TROJAN_DOMAIN:-}" ] || return
  [ -f "$PULSE_ENV_FILE" ] || return
  systemctl is-active --quiet pulse-server 2>/dev/null || return
  systemctl restart pulse-server
  info "pulse-server 已重启"
}

# ── 打印完成摘要 ───────────────────────────────────────────────────────────────
print_summary() {
  printf '\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n'
  printf '  Caddy 配置完成\n'
  printf '━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n'
  [ -n "${PANEL_DOMAIN:-}" ] && printf '  面板地址:   https://%s\n' "$PANEL_DOMAIN"
  [ -n "${TROJAN_DOMAIN:-}" ] && printf '  Trojan WS:  wss://%s/ws  (本地 :%s)\n' "$TROJAN_DOMAIN" "$WS_PORT"
  printf '  Caddyfile:  %s\n' "$CADDYFILE"
  printf '━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n'
  if [ -n "${PANEL_DOMAIN:-}" ] && [ -n "${TROJAN_DOMAIN:-}" ]; then
    printf '  提示: 确认 DNS 已将上述域名分别指向本机 IP\n'
  elif [ -n "${PANEL_DOMAIN:-}" ]; then
    printf '  提示: 确认 DNS 已将 %s 指向本机 IP\n' "$PANEL_DOMAIN"
  else
    printf '  提示: 确认 DNS 已将 %s 指向本机 IP\n' "$TROJAN_DOMAIN"
  fi
}

# ── 主流程 ─────────────────────────────────────────────────────────────────────
main() {
  [ "$(id -u)" = "0" ] || error "请以 root 身份运行"

  read_panel_port
  validate_args
  check_caddy
  check_port_443
  write_caddyfile
  update_pulse_env
  reload_caddy
  restart_pulse_server
  print_summary
}

main "$@"
