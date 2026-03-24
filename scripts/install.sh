#!/bin/sh
set -eu

usage() {
  cat <<'EOF'
用法:
  install.sh server|node [version]

环境变量:
  PULSE_REPO          GitHub 仓库，格式 owner/repo，必填
  PULSE_INSTALL_BIN   二进制安装目录，默认 /usr/local/bin
  PULSE_INSTALL_ETC   配置安装目录，默认 /etc/pulse
  PULSE_INSTALL_SHARE 共享资源目录，默认 /usr/local/share/pulse
  PULSE_INSTALL_LIB   systemd 安装目录，默认 /etc/systemd/system
  PULSE_STATE_DIR     工作目录，默认 /var/lib/pulse

示例:
  curl -fsSL https://raw.githubusercontent.com/OWNER/REPO/main/scripts/install.sh | \
    PULSE_REPO=OWNER/REPO sh -s -- server

  curl -fsSL https://raw.githubusercontent.com/OWNER/REPO/main/scripts/install.sh | \
    PULSE_REPO=OWNER/REPO sh -s -- node v0.1.0
EOF
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "缺少命令: $1" >&2
    exit 1
  }
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

arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo amd64 ;;
    aarch64|arm64) echo arm64 ;;
    *)
      echo "不支持的架构: $(uname -m)" >&2
      exit 1
      ;;
  esac
}

component="${1:-}"
version="${2:-latest}"

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

need_cmd curl
need_cmd tar
need_cmd install

repo="${PULSE_REPO:-}"
if [ -z "$repo" ]; then
  echo "必须设置 PULSE_REPO=owner/repo" >&2
  exit 1
fi

os="linux"
cpu="$(arch)"
asset="pulse-${component}-${os}-${cpu}.tar.gz"

if [ "$version" = "latest" ]; then
  download_url="https://github.com/${repo}/releases/latest/download/${asset}"
else
  download_url="https://github.com/${repo}/releases/download/${version}/${asset}"
fi

bin_dir="${PULSE_INSTALL_BIN:-/usr/local/bin}"
etc_dir="${PULSE_INSTALL_ETC:-/etc/pulse}"
share_dir="${PULSE_INSTALL_SHARE:-/usr/local/share/pulse}"
lib_dir="${PULSE_INSTALL_LIB:-/etc/systemd/system}"
state_dir="${PULSE_STATE_DIR:-/var/lib/pulse}"

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT INT TERM

echo "下载 ${download_url}"
curl -fsSL "$download_url" -o "${tmp_dir}/${asset}"
tar -xzf "${tmp_dir}/${asset}" -C "$tmp_dir"

package_dir="${tmp_dir}/pulse-${component}-${os}-${cpu}"
if [ ! -d "$package_dir" ]; then
  echo "安装包内容异常: ${package_dir} 不存在" >&2
  exit 1
fi

run_as_root mkdir -p "$bin_dir" "$etc_dir" "$share_dir" "$lib_dir" "$state_dir"
run_as_root install -m 0755 "${package_dir}/bin/pulse-${component}" "${bin_dir}/pulse-${component}"

if [ "$component" = "server" ]; then
  run_as_root mkdir -p "${share_dir}/web"
  run_as_root rm -rf "${share_dir}/web/mvp"
  run_as_root cp -R "${package_dir}/share/pulse/web/mvp" "${share_dir}/web/mvp"
  env_target="${etc_dir}/pulse-server.env"
  if [ ! -f "$env_target" ]; then
    run_as_root install -m 0644 "${package_dir}/etc/pulse/pulse-server.env.example" "$env_target"
  fi
  run_as_root install -m 0644 "${package_dir}/lib/systemd/system/pulse-server.service" "${lib_dir}/pulse-server.service"
  if command -v systemctl >/dev/null 2>&1; then
    run_as_root systemctl daemon-reload
    run_as_root systemctl enable --now pulse-server
  fi
else
  env_target="${etc_dir}/pulse-node.env"
  if [ ! -f "$env_target" ]; then
    run_as_root install -m 0644 "${package_dir}/etc/pulse/pulse-node.env.example" "$env_target"
  fi
  run_as_root install -m 0644 "${package_dir}/lib/systemd/system/pulse-node.service" "${lib_dir}/pulse-node.service"
  if command -v systemctl >/dev/null 2>&1; then
    run_as_root systemctl daemon-reload
    run_as_root systemctl enable --now pulse-node
  fi
fi

echo "安装完成: pulse-${component}"
echo "配置文件: ${env_target}"
echo "工作目录: ${state_dir}"
