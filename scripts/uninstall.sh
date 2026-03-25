#!/bin/sh
set -eu

usage() {
  cat <<'EOF'
用法:
  uninstall.sh server|node [--purge]

选项:
  --purge   同时删除数据目录（数据库、证书等），不可恢复

环境变量:
  PULSE_INSTALL_BIN   二进制目录，默认 /usr/local/bin
  PULSE_INSTALL_ETC   配置目录，默认 /etc/pulse
  PULSE_INSTALL_SHARE 共享资源目录，默认 /usr/local/share/pulse
  PULSE_INSTALL_LIB   systemd 目录，默认 /etc/systemd/system
  PULSE_STATE_DIR     数据目录，默认 /var/lib/pulse
EOF
}

run_as_root() {
  if [ "$(id -u)" -eq 0 ]; then
    "$@"
    return
  fi
  if command -v sudo >/dev/null 2>&1; then
    sudo "$@"
    return
  fi
  echo "需要 root 权限运行: $*" >&2
  exit 1
}

component="${1:-}"
purge=0

for arg in "$@"; do
  case "$arg" in
    --purge) purge=1 ;;
  esac
done

case "$component" in
  server|node) ;;
  -h|--help|"")
    usage
    exit 0
    ;;
  *)
    echo "未知组件: $component" >&2
    usage
    exit 1
    ;;
esac

bin_dir="${PULSE_INSTALL_BIN:-/usr/local/bin}"
etc_dir="${PULSE_INSTALL_ETC:-/etc/pulse}"
share_dir="${PULSE_INSTALL_SHARE:-/usr/local/share/pulse}"
lib_dir="${PULSE_INSTALL_LIB:-/etc/systemd/system}"
state_dir="${PULSE_STATE_DIR:-/var/lib/pulse}"

service="pulse-${component}"
bin="${bin_dir}/pulse-${component}"
env_file="${etc_dir}/pulse-${component}.env"
service_file="${lib_dir}/${service}.service"

# 停止并禁用 systemd 服务
if command -v systemctl >/dev/null 2>&1; then
  if systemctl is-active --quiet "$service" 2>/dev/null; then
    echo "停止服务 ${service}..."
    run_as_root systemctl stop "$service"
  fi
  if systemctl is-enabled --quiet "$service" 2>/dev/null; then
    run_as_root systemctl disable "$service"
  fi
fi

# 删除 systemd 服务文件
if [ -f "$service_file" ]; then
  run_as_root rm -f "$service_file"
  command -v systemctl >/dev/null 2>&1 && run_as_root systemctl daemon-reload
fi

# 删除二进制
[ -f "$bin" ] && run_as_root rm -f "$bin"

# 删除配置文件
[ -f "$env_file" ] && run_as_root rm -f "$env_file"

# server 专属：删除面板静态资源
if [ "$component" = "server" ]; then
  [ -d "${share_dir}/web/panel" ] && run_as_root rm -rf "${share_dir}/web/panel"
  # share_dir 为空则一并删除
  if [ -d "$share_dir" ] && [ -z "$(ls -A "$share_dir" 2>/dev/null)" ]; then
    run_as_root rm -rf "$share_dir"
  fi
fi

# node 专属：删除节点证书文件
if [ "$component" = "node" ]; then
  [ -f "${etc_dir}/server_client_cert.pem" ] && run_as_root rm -f "${etc_dir}/server_client_cert.pem"
fi

# etc_dir 为空则一并删除
if [ -d "$etc_dir" ] && [ -z "$(ls -A "$etc_dir" 2>/dev/null)" ]; then
  run_as_root rm -rf "$etc_dir"
fi

# --purge：删除数据目录（数据库、certmagic 证书等）
if [ "$purge" = "1" ]; then
  if [ -d "$state_dir" ]; then
    echo "删除数据目录 ${state_dir}..."
    run_as_root rm -rf "$state_dir"
  fi
fi

echo ""
echo "卸载完成: pulse-${component}"
if [ "$purge" = "0" ] && [ -d "$state_dir" ]; then
  echo "数据目录保留于 ${state_dir}（使用 --purge 可一并删除）"
fi
