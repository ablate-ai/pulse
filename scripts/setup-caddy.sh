#!/bin/sh
# setup-caddy.sh — 安装 Caddy 并初始化最小配置
#
# 只做一件事：确保 Caddy 安装并运行，Caddyfile 包含 pulse.d/ 目录导入。
# 面板域名、Trojan 路由等均通过 Pulse 面板的 Caddy 管理页面配置。
#
# 用法：
#   sh setup-caddy.sh
#
# 环境变量：
#   CADDYFILE    Caddyfile 路径，默认 /etc/caddy/Caddyfile
#   ACME_EMAIL   Let's Encrypt 账号邮箱（可选，推荐设置）

set -eu

CADDYFILE="${CADDYFILE:-/etc/caddy/Caddyfile}"

info()  { printf '\033[32m[INFO]\033[0m  %s\n' "$*"; }
warn()  { printf '\033[33m[WARN]\033[0m  %s\n' "$*"; }
error() { printf '\033[31m[ERROR]\033[0m %s\n' "$*" >&2; exit 1; }

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
    apt-get install -y debian-keyring debian-archive-keyring apt-transport-https curl gnupg 2>/dev/null || true
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

write_caddyfile() {
  CADDYFILE_DIR=$(dirname "$CADDYFILE")
  mkdir -p "$CADDYFILE_DIR/pulse.d"

  if [ -f "$CADDYFILE" ]; then
    BACKUP="${CADDYFILE}.bak.$(date +%Y%m%d%H%M%S)"
    cp "$CADDYFILE" "$BACKUP"
    warn "已备份原 Caddyfile 至 $BACKUP"
  fi

  if [ -n "${ACME_EMAIL:-}" ]; then
    printf '{\n\temail %s\n}\n\n' "$ACME_EMAIL" > "$CADDYFILE"
  else
    : > "$CADDYFILE"
  fi

  printf '# 由 Pulse 面板自动管理，请勿手动编辑此目录\n' >> "$CADDYFILE"
  printf 'import %s/pulse.d/*.caddy\n' "$CADDYFILE_DIR" >> "$CADDYFILE"

  info "Caddyfile 已写入: $CADDYFILE"
}

start_caddy() {
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

main() {
  [ "$(id -u)" = "0" ] || error "请以 root 身份运行"

  check_caddy
  check_port_443
  write_caddyfile
  start_caddy

  printf '\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n'
  printf '  Caddy 安装完成\n'
  printf '  后续配置请前往 Pulse 面板 → Caddy 管理\n'
  printf '━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n'
}

main "$@"
