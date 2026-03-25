#!/bin/sh
# setup-caddy.sh — 为 pulse-server（A2 架构）自动配置 Caddy
#
# 架构说明：
#   Caddy 监听 :443，同时反代：
#     - HTTPS 面板  →  127.0.0.1:PANEL_PORT
#     - Trojan WS   →  127.0.0.1:WS_PORT  (路径 /ws)
#
# 用法：
#   PANEL_DOMAIN=panel.example.com PANEL_PORT=8080 WS_PORT=10443 sh setup-caddy.sh
#   或直接运行，脚本会交互询问。
#
# 环境变量：
#   PANEL_DOMAIN   面板对外域名（必填）
#   PANEL_PORT     pulse-server 监听端口，默认 8080
#   WS_PORT        sing-box Trojan WS 本地端口（PULSE_SINGBOX_WS_PORT），默认 10443
#   CADDYFILE      Caddyfile 路径，默认 /etc/caddy/Caddyfile
#   ACME_EMAIL     Let's Encrypt 账号邮箱（可选）

set -eu

# ── 默认值 ─────────────────────────────────────────────────────────────────────
PANEL_PORT="${PANEL_PORT:-8080}"
WS_PORT="${WS_PORT:-10443}"
CADDYFILE="${CADDYFILE:-/etc/caddy/Caddyfile}"

# ── 工具函数 ───────────────────────────────────────────────────────────────────
info()  { printf '\033[32m[INFO]\033[0m  %s\n' "$*"; }
warn()  { printf '\033[33m[WARN]\033[0m  %s\n' "$*"; }
error() { printf '\033[31m[ERROR]\033[0m %s\n' "$*" >&2; exit 1; }

tty_available() { [ -r /dev/tty ] && [ -w /dev/tty ]; }

prompt_panel_domain() {
  [ "${PANEL_DOMAIN+x}" = "x" ] && return
  tty_available || error "未设置 PANEL_DOMAIN，请通过环境变量传入"
  printf '面板域名（例如 panel.example.com）: '
  read -r PANEL_DOMAIN </dev/tty
  [ -n "$PANEL_DOMAIN" ] || error "域名不能为空"
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

  EMAIL_BLOCK=""
  if [ -n "${ACME_EMAIL:-}" ]; then
    EMAIL_BLOCK="	email ${ACME_EMAIL}"
  fi

  cat >"$CADDYFILE" <<EOF
# Pulse panel + Trojan WS — 由 setup-caddy.sh 生成
# 生成时间: $(date -u '+%Y-%m-%dT%H:%M:%SZ')

{
${EMAIL_BLOCK}
}

${PANEL_DOMAIN} {
	# Trojan WebSocket 流量：路径 /ws 反代到 sing-box 本地端口
	handle /ws {
		# Caddy v2 自动处理 WebSocket 升级，无需手动转发 Connection/Upgrade
		reverse_proxy 127.0.0.1:${WS_PORT}
	}

	# 面板 API 及前端
	handle {
		reverse_proxy 127.0.0.1:${PANEL_PORT}
	}
}
EOF
  info "Caddyfile 已写入: $CADDYFILE"
}

# ── 更新 pulse-server 环境变量 ─────────────────────────────────────────────────
update_pulse_env() {
  ENV_FILE="/etc/pulse/pulse-server.env"
  [ -f "$ENV_FILE" ] || return

  # 写入或更新 PULSE_SINGBOX_WS_PORT
  if grep -q '^PULSE_SINGBOX_WS_PORT=' "$ENV_FILE" 2>/dev/null; then
    sed -i "s|^PULSE_SINGBOX_WS_PORT=.*|PULSE_SINGBOX_WS_PORT=${WS_PORT}|" "$ENV_FILE"
  else
    printf '\nPULSE_SINGBOX_WS_PORT=%s\n' "$WS_PORT" >>"$ENV_FILE"
  fi
  info "已设置 PULSE_SINGBOX_WS_PORT=${WS_PORT} 在 $ENV_FILE"
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
  systemctl is-active --quiet pulse-server 2>/dev/null || return
  systemctl restart pulse-server
  info "pulse-server 已重启"
}

# ── 主流程 ─────────────────────────────────────────────────────────────────────
main() {
  [ "$(id -u)" = "0" ] || error "请以 root 身份运行"

  prompt_panel_domain
  check_caddy
  check_port_443
  write_caddyfile
  update_pulse_env
  reload_caddy
  restart_pulse_server

  cat <<EOF

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  Caddy 配置完成
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  面板地址:   https://${PANEL_DOMAIN}
  Trojan WS:  wss://${PANEL_DOMAIN}/ws  (本地 :${WS_PORT})
  Caddyfile:  ${CADDYFILE}
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  提示: 确认 DNS 已将 ${PANEL_DOMAIN} 指向本机 IP
EOF
}

main "$@"
