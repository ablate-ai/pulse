#!/bin/sh
# setup-caddy.sh — 为 pulse-server（A2 架构）初始化 Caddy 面板 HTTPS
#
# 架构说明：
#   Caddy 监听 :443：
#     - PANEL_DOMAIN  → 127.0.0.1:PANEL_PORT  （面板 HTTPS）
#     - Trojan 域名   → 由 pulse-server 在创建 Trojan inbound 时自动写入
#                       /etc/caddy/pulse.d/<domain>.caddy 并热重载
#
# 用法：
#   PANEL_DOMAIN=panel.example.com sh setup-caddy.sh
#
# 环境变量：
#   PANEL_DOMAIN   面板对外域名（必填）
#   PANEL_PORT     pulse-server 监听端口，默认自动从配置文件读取，兜底 8080
#   CADDYFILE      Caddyfile 路径，默认 /etc/caddy/Caddyfile
#   ACME_EMAIL     Let's Encrypt 账号邮箱（可选）

set -eu

# ── 默认值 ─────────────────────────────────────────────────────────────────────
CADDYFILE="${CADDYFILE:-/etc/caddy/Caddyfile}"
PULSE_ENV_FILE="/etc/pulse/pulse-server.env"

# ── 工具函数 ───────────────────────────────────────────────────────────────────
info()  { printf '\033[32m[INFO]\033[0m  %s\n' "$*"; }
warn()  { printf '\033[33m[WARN]\033[0m  %s\n' "$*"; }
error() { printf '\033[31m[ERROR]\033[0m %s\n' "$*" >&2; exit 1; }

tty_available() { [ -t 0 ]; }  # stdin 是终端时才交互（curl | sh 下为 false）

# ── 从 pulse-server.env 读取面板端口 ──────────────────────────────────────────
read_panel_port() {
  if [ -n "${PANEL_PORT:-}" ]; then
    return
  fi
  PANEL_PORT="8080"
  [ -f "$PULSE_ENV_FILE" ] || return
  ADDR=$(grep '^PULSE_SERVER_ADDR=' "$PULSE_ENV_FILE" 2>/dev/null | cut -d= -f2 | tr -d '"' | tr -d "'")
  [ -n "$ADDR" ] || return
  PORT=$(printf '%s' "$ADDR" | sed 's/.*://')
  [ -n "$PORT" ] && [ "$PORT" != "$ADDR" ] && PANEL_PORT="$PORT"
  info "面板端口: $PANEL_PORT（来自 $PULSE_ENV_FILE）"
}

# ── 获取面板域名（可选，agent 节点可跳过）────────────────────────────────────
prompt_panel_domain() {
  [ -n "${PANEL_DOMAIN:-}" ] && return
  # curl | sh 管道下 tty 不可用，直接跳过（PANEL_DOMAIN 留空）
  tty_available || return
  printf '面板域名（无面板直接回车跳过）: '
  read -r input </dev/tty
  PANEL_DOMAIN="${input:-}"
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
  mkdir -p "$CADDYFILE_DIR/pulse.d"

  if [ -f "$CADDYFILE" ]; then
    BACKUP="${CADDYFILE}.bak.$(date +%Y%m%d%H%M%S)"
    cp "$CADDYFILE" "$BACKUP"
    warn "已备份原 Caddyfile 至 $BACKUP"
  fi

  # 全局块
  if [ -n "${ACME_EMAIL:-}" ]; then
    printf '{\n\temail %s\n}\n\n' "$ACME_EMAIL" > "$CADDYFILE"
  else
    : > "$CADDYFILE"
  fi

  # 面板块（仅 server 节点）
  if [ -n "${PANEL_DOMAIN:-}" ]; then
    printf '# 面板 HTTPS\n' >> "$CADDYFILE"
    printf '%s {\n' "$PANEL_DOMAIN" >> "$CADDYFILE"
    printf '\thandle {\n\t\treverse_proxy 127.0.0.1:%s\n\t}\n' "$PANEL_PORT" >> "$CADDYFILE"
    printf '}\n\n' >> "$CADDYFILE"
  fi

  # Trojan 域名由 pulse-server 动态写入 pulse.d/
  printf '# Trojan inbound 域名（由 pulse-server 自动管理）\n' >> "$CADDYFILE"
  printf 'import %s/pulse.d/*.caddy\n' "$CADDYFILE_DIR" >> "$CADDYFILE"

  info "Caddyfile 已写入: $CADDYFILE"
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

# ── 重启 pulse-server ──────────────────────────────────────────────────────────
restart_pulse_server() {
  systemctl is-active --quiet pulse-server 2>/dev/null || return
  systemctl restart pulse-server
  info "pulse-server 已重启"
}

# ── 主流程 ─────────────────────────────────────────────────────────────────────
main() {
  [ "$(id -u)" = "0" ] || error "请以 root 身份运行"

  prompt_panel_domain
  read_panel_port
  check_caddy
  check_port_443
  write_caddyfile
  reload_caddy
  restart_pulse_server

  printf '\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n'
  printf '  Caddy 配置完成\n'
  printf '━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n'
  [ -n "${PANEL_DOMAIN:-}" ] && printf '  面板地址:  https://%s\n' "$PANEL_DOMAIN"
  printf '  Caddyfile: %s\n' "$CADDYFILE"
  printf '  Trojan 路由目录: %s/pulse.d/\n' "$(dirname "$CADDYFILE")"
  printf '━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n'
  [ -n "${PANEL_DOMAIN:-}" ] && printf '  提示: 确认 DNS 已将 %s 指向本机 IP\n' "$PANEL_DOMAIN"
  printf '  Trojan inbound 创建后路由会自动同步，无需手动操作\n'
}

main "$@"
