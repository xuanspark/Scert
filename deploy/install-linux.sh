#!/usr/bin/env bash
set -Eeuo pipefail

readonly SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly INSTALL_DIR="/scert"
readonly DEFAULT_CERTIFICATE_DIR="/ssl"
readonly SERVICE_USER="Scert"
readonly SERVICE_GROUP="Scert"
readonly SERVICE_DIR="/etc/systemd/system"
readonly SERVICE_DROP_IN_DIR="$SERVICE_DIR/Scert.service.d"
readonly INSTALL_METADATA_FILE="$INSTALL_DIR/conf/installation.json"

source_dir="$SCRIPT_DIR"
certificate_dir="$DEFAULT_CERTIFICATE_DIR"
nologin_shell=""

print_usage() {
    cat <<'USAGE'
Scert Linux 安装器

用法：
  sudo ./install-linux.sh [选项]

选项：
  --source-dir DIR       安装产物目录，必须包含 scert 和 scert-admin
  --certificate-dir DIR  证书根目录，默认 /ssl
  -h, --help             显示帮助

Linux 安装目录固定为 /scert，证书业务目录可在首次初始化时继续配置。
安装器只负责安装文件、创建运行用户和目录、注册 systemd unit，并执行一次
systemctl daemon-reload。安装器不会启动或启用 Scert-admin，也不会创建公网防火墙规则。
USAGE
}

fail() {
    printf '安装失败：%s\n' "$1" >&2
    exit 1
}

parse_args() {
    while (($# > 0)); do
        case "$1" in
            --source-dir)
                (($# >= 2)) || fail "--source-dir 缺少参数"
                source_dir="$2"
                shift 2
                ;;
            --certificate-dir)
                (($# >= 2)) || fail "--certificate-dir 缺少参数"
                certificate_dir="$2"
                shift 2
                ;;
            -h|--help)
                print_usage
                exit 0
                ;;
            *)
                fail "不支持的参数：$1"
                ;;
        esac
    done
}

validate_certificate_dir() {
    [[ "$certificate_dir" == /* ]] || fail "证书目录必须是绝对路径"
    [[ "$certificate_dir" != "/" ]] || fail "证书目录不能是根目录"
    [[ "$certificate_dir" =~ ^/[A-Za-z0-9._/-]+$ ]] || fail "证书目录只能包含字母、数字、点、下划线、连字符和斜杠"
    [[ ! "$certificate_dir" =~ (^|/)\.\.(/|$) ]] || fail "证书目录不能包含上级目录片段"

    reject_symlink_path "$certificate_dir" "证书目录"
    certificate_dir="$(readlink -m -- "$certificate_dir")"
    [[ "$certificate_dir" != "/" ]] || fail "证书目录不能是根目录"

    case "$certificate_dir" in
        /bin|/bin/*|/boot|/boot/*|/dev|/dev/*|/etc|/etc/*|/home|/home/*|/lib|/lib/*|/lib64|/lib64/*|/proc|/proc/*|/root|/root/*|/run|/run/*|/sbin|/sbin/*|/sys|/sys/*|/tmp|/tmp/*|/usr|/usr/*)
            fail "证书目录不能位于受保护的系统路径：$certificate_dir"
            ;;
        /var|/var/*)
            case "$certificate_dir" in
                /var/lib/scert|/var/lib/scert/*) ;;
                *) fail "证书目录只能使用 /var/lib/scert 下的专用路径：$certificate_dir" ;;
            esac
            ;;
        /scert|/scert/*)
            fail "证书目录不能覆盖 Scert 的安装、配置、数据、日志、备份或文档目录：$certificate_dir"
            ;;
    esac

}

reject_symlink_path() {
    local target_path="$1"
    local description="$2"
    local current_path=""
    local part
    local -a path_parts
    IFS='/' read -r -a path_parts <<< "${target_path#/}"
    for part in "${path_parts[@]}"; do
        [[ -n "$part" ]] || continue
        current_path="$current_path/$part"
        if [[ -L "$current_path" ]]; then
            fail "$description 不能包含符号链接：$current_path"
        fi
    done
}

validate_managed_paths() {
    local managed_path
    for managed_path in \
        "$INSTALL_DIR" \
        "$INSTALL_DIR/conf" \
        "$INSTALL_DIR/data" \
        "$INSTALL_DIR/log" \
        "$INSTALL_DIR/backups" \
        "$INSTALL_DIR/docs" \
        "$INSTALL_DIR/web" \
        "$INSTALL_DIR/scert" \
        "$INSTALL_DIR/scert-admin" \
        "$INSTALL_METADATA_FILE" \
        "$INSTALL_DIR/docs/Scert-service-management.md" \
        "$INSTALL_DIR/docs/Scert-admin-service-management.md" \
        "$SERVICE_DIR/Scert.service" \
        "$SERVICE_DIR/Scert-admin.service" \
        "$SERVICE_DROP_IN_DIR" \
        "$SERVICE_DROP_IN_DIR/10-certificate-path.conf"; do
        reject_symlink_path "$managed_path" "Scert 受管路径"
    done
}

validate_environment() {
    [[ "$(uname -s)" == "Linux" ]] || fail "此安装器只支持 Linux"
    [[ "$(id -u)" -eq 0 ]] || fail "请使用 root 或 sudo 运行安装器"
    local command_name
    for command_name in install systemctl getent groupadd useradd id chmod chown cut grep readlink find; do
        command -v "$command_name" >/dev/null 2>&1 || fail "找不到系统命令：$command_name"
    done
    nologin_shell="$(command -v nologin || true)"
    [[ -n "$nologin_shell" ]] || fail "找不到 nologin，无法创建禁止登录的服务用户"
    validate_certificate_dir
    validate_managed_paths
}

validate_artifacts() {
    [[ -d "$source_dir" ]] || fail "安装产物目录不存在：$source_dir"
    source_dir="$(cd -- "$source_dir" && pwd)"
    local artifact
    for artifact in scert scert-admin; do
        [[ -f "$source_dir/$artifact" ]] || fail "安装产物不存在：$source_dir/$artifact"
        [[ -x "$source_dir/$artifact" ]] || fail "安装产物不可执行：$source_dir/$artifact"
    done
    [[ -f "$source_dir/web/index.html" ]] || fail "缺少前端入口文件：$source_dir/web/index.html"
    [[ -f "$SCRIPT_DIR/systemd/Scert.service" ]] || fail "缺少 Scert systemd unit"
    [[ -f "$SCRIPT_DIR/systemd/Scert-admin.service" ]] || fail "缺少 Scert-admin systemd unit"
    [[ -f "$SCRIPT_DIR/systemd/Scert-admin服务管理说明.md" ]] || fail "缺少 Scert-admin 服务管理说明"
    [[ -f "$SCRIPT_DIR/systemd/Scert服务管理说明.md" ]] || fail "缺少 Scert 服务管理说明"
}

ensure_service_account() {
    if ! getent group "$SERVICE_GROUP" >/dev/null 2>&1; then
        if ! groupadd --system "$SERVICE_GROUP"; then
            if groupadd --help 2>&1 | grep -q -- '--badname'; then
                groupadd --system --badname "$SERVICE_GROUP"
            else
                fail "当前系统无法创建名称为 $SERVICE_GROUP 的系统用户组"
            fi
        fi
    fi
    if ! id "$SERVICE_USER" >/dev/null 2>&1; then
        if ! useradd --system --gid "$SERVICE_GROUP" --home-dir "$INSTALL_DIR" --shell "$nologin_shell" "$SERVICE_USER"; then
            if useradd --help 2>&1 | grep -q -- '--badname'; then
                useradd --system --badname --gid "$SERVICE_GROUP" --home-dir "$INSTALL_DIR" --shell "$nologin_shell" "$SERVICE_USER"
            else
                fail "当前系统无法创建名称为 $SERVICE_USER 的系统用户"
            fi
        fi
    fi
    [[ "$(id -gn "$SERVICE_USER")" == "$SERVICE_GROUP" ]] || fail "用户 $SERVICE_USER 未使用预期用户组 $SERVICE_GROUP"
    local current_shell
    current_shell="$(getent passwd "$SERVICE_USER" | cut -d: -f7)"
    case "$current_shell" in
        */nologin|*/false) ;;
        *) fail "现有用户 $SERVICE_USER 允许交互式登录，请先修正其登录 Shell" ;;
    esac
}

create_directories() {
    install -d -o root -g "$SERVICE_GROUP" -m 0750 "$INSTALL_DIR"
    install -d -o root -g root -m 0755 "$INSTALL_DIR/docs"
    install -d -o root -g root -m 0755 "$INSTALL_DIR/web"
    install -d -o "$SERVICE_USER" -g "$SERVICE_GROUP" -m 0750 \
        "$INSTALL_DIR/conf" \
        "$INSTALL_DIR/data" \
        "$INSTALL_DIR/log" \
        "$INSTALL_DIR/backups"
    install -d -o "$SERVICE_USER" -g "$SERVICE_GROUP" -m 0750 "$certificate_dir"
    chown -R -h "$SERVICE_USER:$SERVICE_GROUP" \
        "$INSTALL_DIR/conf" \
        "$INSTALL_DIR/data" \
        "$INSTALL_DIR/log" \
        "$INSTALL_DIR/backups"
    chmod 0700 "$INSTALL_DIR/conf"

    local sensitive_file
    for sensitive_file in scert.json master.key secrets.json; do
        if [[ -f "$INSTALL_DIR/conf/$sensitive_file" ]]; then
            chown "$SERVICE_USER:$SERVICE_GROUP" "$INSTALL_DIR/conf/$sensitive_file"
            chmod 0600 "$INSTALL_DIR/conf/$sensitive_file"
        fi
    done
}

write_install_metadata() {
    printf '{\n  "certificate_root": "%s"\n}\n' "$certificate_dir" > "$INSTALL_METADATA_FILE"
    chown root:"$SERVICE_GROUP" "$INSTALL_METADATA_FILE"
    chmod 0640 "$INSTALL_METADATA_FILE"
}

install_binaries() {
    install -o root -g root -m 0755 "$source_dir/scert" "$INSTALL_DIR/scert"
    install -o root -g root -m 0755 "$source_dir/scert-admin" "$INSTALL_DIR/scert-admin"
}

install_web() {
    cp -a "$source_dir/web/." "$INSTALL_DIR/web/"
    find "$INSTALL_DIR/web" -type d -exec chmod 0755 {} +
    find "$INSTALL_DIR/web" -type f -exec chown root:root {} + -exec chmod 0644 {} +
}

install_service_files() {
    install -o root -g root -m 0644 "$SCRIPT_DIR/systemd/Scert.service" "$SERVICE_DIR/Scert.service"
    install -o root -g root -m 0644 "$SCRIPT_DIR/systemd/Scert-admin.service" "$SERVICE_DIR/Scert-admin.service"
    install -d -o root -g root -m 0755 "$SERVICE_DROP_IN_DIR"
    printf '[Service]\nReadWritePaths=\nReadWritePaths=%s /scert/conf /scert/data /scert/log /scert/backups\n' \
        "$certificate_dir" > "$SERVICE_DROP_IN_DIR/10-certificate-path.conf"
    chmod 0644 "$SERVICE_DROP_IN_DIR/10-certificate-path.conf"

    install -o root -g root -m 0644 \
        "$SCRIPT_DIR/systemd/Scert服务管理说明.md" \
        "$INSTALL_DIR/docs/Scert-service-management.md"
    install -o root -g root -m 0644 \
        "$SCRIPT_DIR/systemd/Scert-admin服务管理说明.md" \
        "$INSTALL_DIR/docs/Scert-admin-service-management.md"
}

reload_systemd() {
    systemctl daemon-reload
}

print_completion() {
    printf '%s\n' \
        'Scert Linux 文件安装完成。' \
        '' \
        '已安装：' \
        '  核心程序：/scert/scert' \
        '  密码重置程序：/scert/scert-admin' \
        '  配置目录：/scert/conf' \
        '  数据目录：/scert/data' \
        '  日志目录：/scert/log' \
        '  备份目录：/scert/backups' \
        "  证书目录：$certificate_dir" \
        "  安装路径记录：$INSTALL_METADATA_FILE" \
        '' \
        '本次只加载了 systemd unit，没有启动任何服务，也没有设置开机启动。' \
        '首次初始化会读取安装路径记录，并将上述证书目录作为默认值。' \
        '请先完成初始化配置。Scert-admin 需要密码重置时，管理员仅使用：' \
        '  systemctl start Scert-admin' \
        '  systemctl stop Scert-admin' \
        '' \
        'Scert 主服务是否启用和启动，应在初始化配置及证书部署完成后按运维流程执行。' \
        '本机防火墙和云安全组端口放行由初始化流程处理或提示，不由本安装器创建公网规则。'
}

main() {
    parse_args "$@"
    validate_environment
    validate_artifacts
    ensure_service_account
    create_directories
    write_install_metadata
    install_binaries
    install_web
    install_service_files
    reload_systemd
    print_completion
}

main "$@"
