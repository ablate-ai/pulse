#!/bin/sh
# 卸载 pulse-server、pulse-node、Caddy 及所有相关数据（不可恢复）
set -eu

run_as_root() {
  if [ "$(id -u)" -eq 0 ]; then "$@"; return; fi
  if command -v sudo >/dev/null 2>&1; then sudo "$@"; return; fi
  echo "需要 root 权限: $*" >&2; exit 1
}

stop_and_disable() {
  svc="$1"
  command -v systemctl >/dev/null 2>&1 || return
  systemctl is-active --quiet "$svc" 2>/dev/null && run_as_root systemctl stop "$svc" || true
  systemctl is-enabled --quiet "$svc" 2>/dev/null && run_as_root systemctl disable "$svc" || true
}

bin_dir="${PULSE_INSTALL_BIN:-/usr/local/bin}"
etc_dir="${PULSE_INSTALL_ETC:-/etc/pulse}"
share_dir="${PULSE_INSTALL_SHARE:-/usr/local/share/pulse}"
lib_dir="${PULSE_INSTALL_LIB:-/etc/systemd/system}"
state_dir="${PULSE_STATE_DIR:-/var/lib/pulse}"

# ── pulse-server ───────────────────────────────────────────────────────────────
stop_and_disable pulse-server
run_as_root rm -f "${lib_dir}/pulse-server.service"
run_as_root rm -f "${bin_dir}/pulse-server"

# ── pulse-node ─────────────────────────────────────────────────────────────────
stop_and_disable pulse-node
run_as_root rm -f "${lib_dir}/pulse-node.service"
run_as_root rm -f "${bin_dir}/pulse-node"

# systemd daemon-reload
command -v systemctl >/dev/null 2>&1 && run_as_root systemctl daemon-reload || true

# ── Caddy ──────────────────────────────────────────────────────────────────────
stop_and_disable caddy
run_as_root rm -f "${lib_dir}/caddy.service"
# 删除 Caddyfile 及 pulse 动态路由目录
run_as_root rm -rf /etc/caddy/Caddyfile /etc/caddy/pulse.d
# 若整个 /etc/caddy 为空则删除
if [ -d /etc/caddy ] && [ -z "$(ls -A /etc/caddy 2>/dev/null)" ]; then
  run_as_root rm -rf /etc/caddy
fi
# Caddy 数据目录（ACME 证书）
run_as_root rm -rf /var/lib/caddy /root/.local/share/caddy

# ── pulse 配置与数据 ────────────────────────────────────────────────────────────
run_as_root rm -rf "$etc_dir"
run_as_root rm -rf "$share_dir"
run_as_root rm -rf "$state_dir"

echo ""
echo "卸载完成：pulse-server、pulse-node、Caddy 及所有数据已删除"
