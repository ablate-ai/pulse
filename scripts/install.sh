#!/bin/sh
set -eu

usage() {
  cat <<'EOF'
用法:
  install.sh server|node [version]

环境变量:
  PULSE_REPO          GitHub 仓库，格式 owner/repo，默认 ablate-ai/pulse
  PULSE_INSTALL_BIN   二进制安装目录，默认 /usr/local/bin
  PULSE_INSTALL_ETC   配置安装目录，默认 /etc/pulse
  PULSE_INSTALL_SHARE 共享资源目录，默认 /usr/local/share/pulse
  PULSE_INSTALL_LIB   systemd 安装目录，默认 /etc/systemd/system
  PULSE_STATE_DIR     工作目录，默认 /var/lib/pulse
  PULSE_ADMIN_USERNAME server 安装时写入管理员用户名，默认 admin
  PULSE_ADMIN_PASSWORD server 安装时写入管理员密码，不指定则随机生成
  PULSE_SERVER_ADDR    server 监听地址，不指定则随机端口（格式 :端口）
  PULSE_PANEL_DOMAIN   server 面板对外域名，设置后 TLS proxy 自动申请证书并启用 HTTPS
  PULSE_SERVER_NODE_CLIENT_CERT_FILE server 访问节点时使用的客户端证书路径
  PULSE_SERVER_NODE_CLIENT_KEY_FILE  server 访问节点时使用的客户端私钥路径
  PULSE_NODE_TLS_CERT_FILE           node 服务端证书路径
  PULSE_NODE_TLS_KEY_FILE            node 服务端私钥路径

示例:
  curl -fsSL https://raw.githubusercontent.com/ablate-ai/pulse/main/scripts/install.sh | \
    PULSE_ADMIN_PASSWORD='strong-password' sh -s -- server

  curl -fsSL https://raw.githubusercontent.com/ablate-ai/pulse/main/scripts/install.sh | \
    sh -s -- node
EOF
}

tty_available() {
  [ -r /dev/tty ] && [ -w /dev/tty ]
}

prompt_panel_domain() {
  [ "${PULSE_PANEL_DOMAIN+x}" = "x" ] && return
  tty_available || return
  printf "面板域名（用于 HTTPS，留空则跳过）: " > /dev/tty
  read -r PULSE_PANEL_DOMAIN < /dev/tty || true
  export PULSE_PANEL_DOMAIN
}

prompt_node_client_cert_pem() {
  target_file="$1"
  force="${2:-0}"

  # 更新场景：证书已存在且未强制，直接跳过
  if [ -f "$target_file" ] && [ "$force" != "1" ]; then
    return
  fi

  if ! tty_available; then
    echo "无法交互输入证书，请确保在终端中运行安装脚本" >&2
    exit 1
  fi

  cert_tmp="$(mktemp)"
  printf "请粘贴 node 需要信任的 server 客户端证书 PEM 内容。\n" > /dev/tty
  printf "粘贴完成后，按两次回车（输入空行）确认。\n" > /dev/tty
  : > "$cert_tmp"
  while IFS= read -r line < /dev/tty; do
    [ -z "$line" ] && break
    printf "%s\n" "$line" >> "$cert_tmp"
  done
  if [ ! -s "$cert_tmp" ]; then
    rm -f "$cert_tmp"
    echo "证书内容为空，安装终止" >&2
    exit 1
  fi

  run_as_root mkdir -p "$(dirname "$target_file")"
  run_as_root install -m 0644 "$cert_tmp" "$target_file"
  rm -f "$cert_tmp"
}

random_password() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -base64 18 | tr -d '/+=' | head -c 24
  else
    tr -dc 'A-Za-z0-9' < /dev/urandom | head -c 24
  fi
}

random_port() {
  awk 'BEGIN{srand(); print int(rand()*55535)+10000}'
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

quote_env_value() {
  printf "'%s'" "$(printf "%s" "$1" | sed "s/'/'\\\\''/g")"
}

set_env_file_value() {
  file="$1"
  key="$2"
  value="$(quote_env_value "$3")"
  tmp_file="$(mktemp)"

  awk -v key="$key" -v value="$value" '
    BEGIN { updated = 0 }
    index($0, key "=") == 1 {
      print key "=" value
      updated = 1
      next
    }
    { print }
    END {
      if (!updated) {
        print key "=" value
      }
    }
  ' "$file" > "$tmp_file"

  run_as_root install -m 0644 "$tmp_file" "$file"
  rm -f "$tmp_file"
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

force=0
component=""
version="latest"
for _arg in "$@"; do
  case "$_arg" in
    --force|-f) force=1 ;;
    -h|--help) usage; exit 0 ;;
    server|node)
      component="$_arg"
      ;;
    *)
      if [ -n "$component" ]; then
        version="$_arg"
      else
        echo "未知参数: $_arg" >&2
        usage
        exit 1
      fi
      ;;
  esac
done

if [ -z "$component" ]; then
  usage
  exit 0
fi

need_cmd curl
need_cmd tar
need_cmd install

repo="${PULSE_REPO:-ablate-ai/pulse}"

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
  env_target="${etc_dir}/pulse-server.env"
  is_new_install=0
  if [ ! -f "$env_target" ]; then
    is_new_install=1
    run_as_root install -m 0644 "${package_dir}/etc/pulse/pulse-server.env.example" "$env_target"
  fi
  if [ "$is_new_install" = "1" ]; then
    if [ "${PULSE_ADMIN_PASSWORD+x}" != "x" ]; then
      PULSE_ADMIN_PASSWORD="$(random_password)"
    fi
    if [ "${PULSE_SERVER_ADDR+x}" != "x" ]; then
      PULSE_SERVER_ADDR=":$(random_port)"
    fi
    prompt_panel_domain
  fi
  if [ "${PULSE_ADMIN_USERNAME+x}" = "x" ]; then
    set_env_file_value "$env_target" "PULSE_ADMIN_USERNAME" "$PULSE_ADMIN_USERNAME"
  fi
  if [ "${PULSE_ADMIN_PASSWORD+x}" = "x" ]; then
    set_env_file_value "$env_target" "PULSE_ADMIN_PASSWORD" "$PULSE_ADMIN_PASSWORD"
  fi
  if [ "${PULSE_SERVER_ADDR+x}" = "x" ]; then
    set_env_file_value "$env_target" "PULSE_SERVER_ADDR" "$PULSE_SERVER_ADDR"
  fi
  if [ "${PULSE_SERVER_NODE_CLIENT_CERT_FILE+x}" = "x" ]; then
    set_env_file_value "$env_target" "PULSE_SERVER_NODE_CLIENT_CERT_FILE" "$PULSE_SERVER_NODE_CLIENT_CERT_FILE"
  fi
  if [ "${PULSE_SERVER_NODE_CLIENT_KEY_FILE+x}" = "x" ]; then
    set_env_file_value "$env_target" "PULSE_SERVER_NODE_CLIENT_KEY_FILE" "$PULSE_SERVER_NODE_CLIENT_KEY_FILE"
  fi
  if [ "${PULSE_PANEL_DOMAIN+x}" = "x" ] && [ -n "$PULSE_PANEL_DOMAIN" ]; then
    set_env_file_value "$env_target" "PULSE_PANEL_DOMAIN" "$PULSE_PANEL_DOMAIN"
  fi
  run_as_root install -m 0644 "${package_dir}/lib/systemd/system/pulse-server.service" "${lib_dir}/pulse-server.service"
  if command -v systemctl >/dev/null 2>&1; then
    run_as_root systemctl daemon-reload
    run_as_root systemctl enable pulse-server
    run_as_root systemctl restart pulse-server
  fi
else
  env_target="${etc_dir}/pulse-node.env"
  if [ ! -f "$env_target" ]; then
    run_as_root install -m 0644 "${package_dir}/etc/pulse/pulse-node.env.example" "$env_target"
  fi
  cert_target="${etc_dir}/server_client_cert.pem"
  prompt_node_client_cert_pem "$cert_target" "$force"
  # 无论新装还是更新，都确保 env 中记录证书路径
  set_env_file_value "$env_target" "PULSE_NODE_TLS_CLIENT_CERT_FILE" "$cert_target"
  if [ "${PULSE_NODE_TLS_CERT_FILE+x}" = "x" ]; then
    set_env_file_value "$env_target" "PULSE_NODE_TLS_CERT_FILE" "$PULSE_NODE_TLS_CERT_FILE"
  fi
  if [ "${PULSE_NODE_TLS_KEY_FILE+x}" = "x" ]; then
    set_env_file_value "$env_target" "PULSE_NODE_TLS_KEY_FILE" "$PULSE_NODE_TLS_KEY_FILE"
  fi
  run_as_root install -m 0644 "${package_dir}/lib/systemd/system/pulse-node.service" "${lib_dir}/pulse-node.service"
  if command -v systemctl >/dev/null 2>&1; then
    run_as_root systemctl daemon-reload
    run_as_root systemctl enable pulse-node
    run_as_root systemctl restart pulse-node
  fi
fi

_installed_version="$("${bin_dir}/pulse-${component}" --version 2>/dev/null || echo "$version")"
echo ""
echo "安装完成: pulse-${component} ${_installed_version}"
echo "配置文件: ${env_target}"
echo "工作目录: ${state_dir}"
if [ "$component" = "server" ]; then
  # 从 env 文件读取实际值
  _panel_domain="$(grep '^PULSE_PANEL_DOMAIN=' "$env_target" 2>/dev/null | cut -d= -f2- | tr -d "'" | tr -d '"')"
  _addr="$(grep '^PULSE_SERVER_ADDR=' "$env_target" 2>/dev/null | cut -d= -f2- | tr -d "'" | tr -d '"')"
  _username="$(grep '^PULSE_ADMIN_USERNAME=' "$env_target" 2>/dev/null | cut -d= -f2- | tr -d "'" | tr -d '"')"
  _password="$(grep '^PULSE_ADMIN_PASSWORD=' "$env_target" 2>/dev/null | cut -d= -f2- | tr -d "'" | tr -d '"')"
  echo ""
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  if [ -n "${_panel_domain:-}" ]; then
    echo "  面板地址: https://${_panel_domain}"
  else
    _port="${_addr#:}"
    _ip="$(ip -4 addr show scope global 2>/dev/null | awk '/inet/{gsub(/\/.*/, "", $2); print $2; exit}' \
          || hostname -I 2>/dev/null | awk '{print $1}' \
          || echo "<your-ip>")"
    echo "  面板地址: http://${_ip}:${_port}"
  fi
  echo "  管理员:   ${_username:-admin}"
  echo "  密码:     ${_password:-(见 ${env_target})}"
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
fi
