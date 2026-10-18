#!/usr/bin/env bash
set -euo pipefail

# Scert 生产环境全新一键安装部署脚本 (755)

usage() {
  echo "用法：$0 [安装根目录]"
  echo "全新部署会初始化 Scert 专属运行目录并配置 systemd 系统服务。"
}

if [ "${1:-}" = "-h" ] || [ "${1:-}" = "--help" ]; then
  usage
  exit 0
fi

if [ "$(id -u)" -ne 0 ]; then
  echo "错误：必须使用 root 用户执行全新安装！" >&2
  exit 1
fi

script_dir="$(cd "$(dirname "$0")" && pwd)"
package_root="$(realpath -e -- "$script_dir/..")"

runtime_dir="${1:-/scert}"
fresh_confirmation="确认全新部署 Scert"

printf '这是不可逆的 Scert 专属数据与环境部署操作。请输入“%s”继续： ' "$fresh_confirmation"
if ! IFS= read -r confirmation || [ "$confirmation" != "$fresh_confirmation" ]; then
  echo "未获得有效确认，已取消全新部署。" >&2
  exit 1
fi

case "$(uname -m)" in
  x86_64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) echo "错误：不支持的系统架构 $(uname -m)" >&2; exit 1 ;;
esac

# 1. 查找二进制程序
if [ -f "$package_root/scert" ] && [ -f "$package_root/scert-admin" ]; then
  src_scert="$package_root/scert"
  src_scert_admin="$package_root/scert-admin"
  src_scert_nginx="$package_root/scert-nginx"
  src_scert_storage="$package_root/scert-storage"
else
  echo "错误：未能在 $package_root 目录下找到必要的 scert/scert-admin 二进制！" >&2
  exit 1
fi

# 2. 停止旧服务
echo ">>> 正在检查并停止已有相关服务..."
systemctl stop Scert.service 2>/dev/null || true
systemctl stop Scert-admin.service 2>/dev/null || true
systemctl stop scert-nginx.service 2>/dev/null || true
systemctl stop Scert-storage.service 2>/dev/null || true

# 3. 创建系统专用运行用户与组
if ! getent group Scert >/dev/null; then
  groupadd --system Scert
fi
if ! getent passwd Scert >/dev/null; then
  useradd --system --gid Scert --home-dir "$runtime_dir" --shell /usr/sbin/nologin Scert
fi

# 4. 创建标准目录结构
echo ">>> 正在创建标准目录结构: $runtime_dir"
mkdir -p "$runtime_dir"
for dir in config data log ssl web conf deploy; do
  mkdir -p "$runtime_dir/$dir"
done

# 5. 安装二进制文件
echo ">>> 正在安装程序文件..."
cp -f "$src_scert" "$runtime_dir/scert"
chown Scert:Scert "$runtime_dir/scert"
chmod 0755 "$runtime_dir/scert"

cp -f "$src_scert_admin" "$runtime_dir/scert-admin"
chown root:root "$runtime_dir/scert-admin"
chmod 0700 "$runtime_dir/scert-admin"

if [ -f "$src_scert_nginx" ]; then
  cp -f "$src_scert_nginx" "$runtime_dir/scert-nginx"
  chown root:root "$runtime_dir/scert-nginx"
  chmod 0755 "$runtime_dir/scert-nginx"
fi

if [ -f "$src_scert_storage" ]; then
  cp -f "$src_scert_storage" "$runtime_dir/scert-storage"
  chown root:root "$runtime_dir/scert-storage"
  chmod 0755 "$runtime_dir/scert-storage"
fi

# 6. 复制前端静态文件与部署套件
if [ -d "$package_root/web" ]; then
  cp -rf "$package_root/web"/* "$runtime_dir/web/" 2>/dev/null || true
fi
if [ -d "$package_root/deploy" ]; then
  cp -rf "$package_root/deploy"/* "$runtime_dir/deploy/" 2>/dev/null || true
  chmod -R 0755 "$runtime_dir/deploy" 2>/dev/null || true
fi

chown -R Scert:Scert "$runtime_dir/config" "$runtime_dir/data" "$runtime_dir/log" "$runtime_dir/ssl" "$runtime_dir/web" "$runtime_dir/conf"
chmod 0750 "$runtime_dir/config" "$runtime_dir/data" "$runtime_dir/log" "$runtime_dir/ssl" "$runtime_dir/conf"

# 7. 安装 systemd 服务单元
systemd_src="$package_root/deploy/systemd"
if [ ! -d "$systemd_src" ] && [ -d "$runtime_dir/deploy/systemd" ]; then
  systemd_src="$runtime_dir/deploy/systemd"
fi

if [ -d "$systemd_src" ]; then
  echo ">>> 正在注册 systemd 服务单元..."
  for svc in Scert.service Scert-admin.service scert-nginx.service scert-nginx.socket Scert-storage.service Scert-storage.socket; do
    if [ -f "$systemd_src/$svc" ]; then
      sed -e "s|/scert|$runtime_dir|g" "$systemd_src/$svc" > "/etc/systemd/system/$svc"
      chmod 0644 "/etc/systemd/system/$svc"
      chown root:root "/etc/systemd/system/$svc"
    fi
  done
  systemctl daemon-reload
  systemctl enable Scert.service >/dev/null 2>&1 || true
fi

# 8. 检查并放行常用防火墙端口 (80, 443)
open_firewall_ports() {
  local ports="80 443"
  echo ">>> 正在检查并自动开放网络防火墙端口 (80, 443)..."
  local opened=false

  if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q "Status: active"; then
    for p in $ports; do
      ufw allow "${p}/tcp" >/dev/null 2>&1 || true
    done
    opened=true
    echo "ufw 端口放行完成 (80, 443)。"
  fi

  if command -v firewall-cmd >/dev/null 2>&1 && systemctl is-active --quiet firewalld; then
    for p in $ports; do
      firewall-cmd --permanent --add-port="${p}/tcp" >/dev/null 2>&1 || true
    done
    firewall-cmd --reload >/dev/null 2>&1 || true
    opened=true
    echo "firewalld 端口放行并重载完成 (80, 443)。"
  fi

  if [ "$opened" = false ] && command -v iptables >/dev/null 2>&1; then
    for p in $ports; do
      if ! iptables -C INPUT -p tcp --dport "$p" -j ACCEPT >/dev/null 2>&1; then
        iptables -I INPUT -p tcp --dport "$p" -j ACCEPT >/dev/null 2>&1 || true
      fi
    done
    echo "iptables INPUT 规则已更新放行 (80, 443)。"
  fi
}

open_firewall_ports

# 9. 启动 Scert 服务
echo ">>> 正在启动 Scert 主服务..."
systemctl restart Scert.service || true

echo "=========================================================="
echo " Scert 全新安装与系统服务注册完成！"
echo " 安装路径: $runtime_dir"
echo " 运行用户: Scert"
echo " 开机自启: 已启用 (systemctl enable Scert)"
echo " 服务状态: $(systemctl is-active Scert.service 2>/dev/null || echo '未运行')"
echo "=========================================================="
