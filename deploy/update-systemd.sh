#!/usr/bin/env bash
set -euo pipefail

# Scert 生产环境版本平滑升级脚本 (755)
# 规范说明：
# 1. 严格以现有生产环境的实际安装路径作为更新路径，严禁以升级包解压路径作为运行路径。
# 2. 仅更新程序文件与系统服务，严密保留现有数据库、配置文件、证书与域名，免去重新初始化流程。

usage() {
  echo "用法：$0 [现有安装目录]"
  echo "默认将自动从 /etc/systemd/system/Scert.service 中解析实际安装路径（如 /scert）。"
  echo "如果服务未安装或需要手动指定，可在参数中显式传入安装目录。"
}

if [ "${1:-}" = "-h" ] || [ "${1:-}" = "--help" ]; then
  usage
  exit 0
fi

if [ "$(id -u)" -ne 0 ]; then
  echo "错误：必须使用 root 用户执行升级！" >&2
  exit 1
fi

script_dir="$(cd "$(dirname "$0")" && pwd)"
package_root="$(realpath -e -- "$script_dir/..")"

# 1. 自动探查并确定系统的实际安装路径
target_dir="${1:-}"

if [ -z "$target_dir" ]; then
  if [ -f /etc/systemd/system/Scert.service ]; then
    service_workdir="$(grep -E '^[[:space:]]*WorkingDirectory=' /etc/systemd/system/Scert.service | head -n1 | cut -d'=' -f2- | tr -d ' ' || true)"
    if [ -n "$service_workdir" ] && [ -d "$service_workdir" ]; then
      target_dir="$service_workdir"
    fi
  fi
fi

if [ -z "$target_dir" ]; then
  for candidate in /scert /opt/scert; do
    if [ -d "$candidate" ] && [ -f "$candidate/scert" ] && [ "$candidate" != "$package_root" ]; then
      target_dir="$candidate"
      break
    fi
  done
fi

if [ -z "$target_dir" ]; then
  echo "错误：未能在系统服务中检测到 Scert 的现有安装路径！" >&2
  echo "请手动指定目标安装目录，例如：$0 /scert" >&2
  exit 1
fi

target_dir="$(realpath -m -- "$target_dir")"

if [ ! -d "$target_dir" ]; then
  echo "错误：目标安装目录不存在: $target_dir" >&2
  exit 1
fi

if [ ! -f "$target_dir/scert" ]; then
  echo "警告：在目标目录 $target_dir 下未检测到旧的 scert 主程序，请确认是否为正确的安装目录！"
fi

echo "=========================================================="
echo "               Scert 服务平滑更新                        "
echo " 升级包解压源目录 : $package_root"
echo " 目标系统安装目录 : $target_dir"
echo "=========================================================="

case "$(uname -m)" in
  x86_64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) echo "不支持的系统架构: $(uname -m)" >&2; exit 1 ;;
esac

# 2. 定位新版本程序二进制文件
if [ -f "$package_root/scert" ] && [ -f "$package_root/scert-admin" ]; then
  new_scert="$package_root/scert"
  new_scert_admin="$package_root/scert-admin"
  new_scert_nginx="$package_root/scert-nginx"
  new_scert_storage="$package_root/scert-storage"
else
  echo "错误：未能找到升级包中的二进制程序文件！" >&2
  exit 1
fi

# 3. 停止运行中的服务以解除文件占用
echo ">>> 正在停止运行中的 Scert 服务..."
systemctl stop Scert.service 2>/dev/null || true
systemctl stop Scert-admin.service 2>/dev/null || true
systemctl stop scert-nginx.service 2>/dev/null || true
systemctl stop Scert-storage.service 2>/dev/null || true

# 4. 确保运行用户与组存在
if ! getent group Scert >/dev/null; then
  groupadd --system Scert
fi
if ! getent passwd Scert >/dev/null; then
  useradd --system --gid Scert --home-dir "$target_dir" --shell /usr/sbin/nologin Scert
fi

# 5. 替换程序二进制文件至安装目录（保持现有数据库和配置文件不被覆盖）
echo ">>> 正在更新主程序及管理工具至: $target_dir"
if [ "$new_scert" != "$target_dir/scert" ]; then
  cp -f "$new_scert" "$target_dir/scert"
fi
chown Scert:Scert "$target_dir/scert"
chmod 0755 "$target_dir/scert"

if [ "$new_scert_admin" != "$target_dir/scert-admin" ]; then
  cp -f "$new_scert_admin" "$target_dir/scert-admin"
fi
chown root:root "$target_dir/scert-admin"
chmod 0700 "$target_dir/scert-admin"

if [ -f "$new_scert_nginx" ]; then
  cp -f "$new_scert_nginx" "$target_dir/scert-nginx"
  chown root:root "$target_dir/scert-nginx"
  chmod 0755 "$target_dir/scert-nginx"
fi

if [ -f "$new_scert_storage" ]; then
  cp -f "$new_scert_storage" "$target_dir/scert-storage"
  chown root:root "$target_dir/scert-storage"
  chmod 0755 "$target_dir/scert-storage"
fi

# 同步更新前端静态资源 web/
if [ -d "$package_root/web" ]; then
  echo ">>> 正在同步前端静态资源..."
  mkdir -p "$target_dir/web"
  cp -rf "$package_root/web"/* "$target_dir/web/" 2>/dev/null || true
fi

# 同时更新安装目录下的 deploy 脚本供日后维护
if [ "$package_root" != "$target_dir" ] && [ -d "$package_root/deploy" ]; then
  mkdir -p "$target_dir/deploy"
  cp -rf "$package_root/deploy"/* "$target_dir/deploy/" 2>/dev/null || true
  chmod -R 0755 "$target_dir/deploy" 2>/dev/null || true
fi

# 6. 更新 systemd 服务单元
systemd_src="$package_root/deploy/systemd"
if [ ! -d "$systemd_src" ] && [ -d "$target_dir/deploy/systemd" ]; then
  systemd_src="$target_dir/deploy/systemd"
fi

if [ -d "$systemd_src" ]; then
  for svc in Scert.service Scert-admin.service scert-nginx.service scert-nginx.socket Scert-storage.service Scert-storage.socket; do
    if [ -f "$systemd_src/$svc" ]; then
      sed -e "s|/scert|$target_dir|g" "$systemd_src/$svc" > "/etc/systemd/system/$svc"
      chmod 0644 "/etc/systemd/system/$svc"
      chown root:root "/etc/systemd/system/$svc"
    fi
  done
fi

# 7. 重载 systemd 并重新启动 Scert
echo ">>> 重载 systemd 配置并重启 Scert 服务..."
systemctl daemon-reload
systemctl enable Scert.service >/dev/null 2>&1 || true
systemctl restart Scert.service

# 8. 检查运行状态
sleep 1
if systemctl is-active --quiet Scert.service; then
  echo "=========================================================="
  echo " Scert 平滑升级成功！"
  echo " 升级程序写入目录: $target_dir"
  echo " 数据库、SSL 证书及配置均完整保留。"
  echo " 服务状态: active (running)"
  echo "=========================================================="
else
  echo "警告：Scert 服务启动异常，请检查服务日志: journalctl -u Scert -n 20 --no-pager" >&2
  exit 1
fi
