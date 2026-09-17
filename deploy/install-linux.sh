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
certificate_dir_explicit=false
nologin_shell=""
transaction_dir=""
transaction_active=false
declare -a transaction_targets=()

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
                certificate_dir_explicit=true
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

load_existing_certificate_dir() {
    if [[ "$certificate_dir_explicit" == "true" || ! -e "$INSTALL_METADATA_FILE" ]]; then
        return
    fi
    reject_symlink_path "$INSTALL_METADATA_FILE" "安装路径记录"
    [[ -f "$INSTALL_METADATA_FILE" ]] || fail "安装路径记录必须是普通文件：$INSTALL_METADATA_FILE"

    local line
    local existing_certificate_dir=""
    while IFS= read -r line; do
        if [[ "$line" =~ \"certificate_root\"[[:space:]]*:[[:space:]]*\"([^\"]+)\" ]]; then
            existing_certificate_dir="${BASH_REMATCH[1]}"
            break
        fi
    done < "$INSTALL_METADATA_FILE"
    [[ -n "$existing_certificate_dir" ]] || fail "无法从既有安装记录读取证书目录，请显式使用 --certificate-dir"
    certificate_dir="$existing_certificate_dir"
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
    for managed_path in "$INSTALL_DIR" "$INSTALL_DIR/conf" "$INSTALL_DIR/data" \
        "$INSTALL_DIR/log" "$INSTALL_DIR/backups" "$INSTALL_DIR/docs" \
        "$INSTALL_DIR/web" "$SERVICE_DIR" "$SERVICE_DROP_IN_DIR"; do
        [[ ! -e "$managed_path" || -d "$managed_path" ]] || fail "Scert 目录路径不是普通目录：$managed_path"
    done
    for managed_path in "$INSTALL_DIR/scert" "$INSTALL_DIR/scert-admin" \
        "$INSTALL_METADATA_FILE" "$INSTALL_DIR/docs/Scert-service-management.md" \
        "$INSTALL_DIR/docs/Scert-admin-service-management.md" \
        "$SERVICE_DIR/Scert.service" "$SERVICE_DIR/Scert-admin.service" \
        "$SERVICE_DROP_IN_DIR/10-certificate-path.conf"; do
        [[ ! -e "$managed_path" || -f "$managed_path" ]] || fail "Scert 文件路径不是普通文件：$managed_path"
    done
    if [[ -d "$INSTALL_DIR/web" ]]; then
        [[ -z "$(find "$INSTALL_DIR/web" ! -type f ! -type d -print -quit)" ]] || fail "既有 Web 目录包含符号链接或特殊文件，拒绝覆盖"
    fi
}

validate_environment() {
    [[ "$(uname -s)" == "Linux" ]] || fail "此安装器只支持 Linux"
    [[ "$(id -u)" -eq 0 ]] || fail "请使用 root 或 sudo 运行安装器"
    local command_name
    for command_name in install systemctl getent groupadd useradd id chmod chown cut grep readlink find file mv cp mktemp; do
        command -v "$command_name" >/dev/null 2>&1 || fail "找不到系统命令：$command_name"
    done
    nologin_shell="$(command -v nologin || true)"
    [[ -n "$nologin_shell" ]] || fail "找不到 nologin，无法创建禁止登录的服务用户"
    load_existing_certificate_dir
    validate_certificate_dir
    validate_managed_paths
}

validate_artifacts() {
    [[ -d "$source_dir" ]] || fail "安装产物目录不存在：$source_dir"
    source_dir="$(cd -- "$source_dir" && pwd -P)"
    case "$source_dir/" in
        "$INSTALL_DIR/"|"$INSTALL_DIR/web/"*) fail "不能从正在替换的安装文件或 Web 目录安装" ;;
    esac
    local artifact
    for artifact in scert scert-admin; do
        [[ -f "$source_dir/$artifact" && ! -L "$source_dir/$artifact" ]] || fail "安装产物必须是普通文件：$source_dir/$artifact"
        [[ -r "$source_dir/$artifact" && -x "$source_dir/$artifact" ]] || fail "安装产物不可读取或执行：$source_dir/$artifact"
        local artifact_description
        artifact_description="$(LC_ALL=C file -b -- "$source_dir/$artifact")"
        [[ "$artifact_description" == *"ELF 64-bit LSB"* && "$artifact_description" == *"x86-64"* ]] || \
            fail "安装产物不是 Linux amd64 ELF：$source_dir/$artifact"
    done
    [[ -f "$source_dir/web/index.html" && ! -L "$source_dir/web/index.html" ]] || fail "缺少普通前端入口文件：$source_dir/web/index.html"
    [[ -z "$(find "$source_dir/web" -type l -print -quit)" ]] || fail "Web 安装产物不能包含符号链接"
    [[ -z "$(find "$source_dir/web" ! -type f ! -type d -print -quit)" ]] || fail "Web 安装产物包含不支持的特殊文件"
    [[ -f "$SCRIPT_DIR/systemd/Scert.service" && ! -L "$SCRIPT_DIR/systemd/Scert.service" ]] || fail "缺少普通 Scert systemd unit"
    [[ -f "$SCRIPT_DIR/systemd/Scert-admin.service" && ! -L "$SCRIPT_DIR/systemd/Scert-admin.service" ]] || fail "缺少普通 Scert-admin systemd unit"
    [[ -f "$SCRIPT_DIR/systemd/Scert-admin服务管理说明.md" && ! -L "$SCRIPT_DIR/systemd/Scert-admin服务管理说明.md" ]] || fail "缺少普通 Scert-admin 服务管理说明"
    [[ -f "$SCRIPT_DIR/systemd/Scert服务管理说明.md" && ! -L "$SCRIPT_DIR/systemd/Scert服务管理说明.md" ]] || fail "缺少普通 Scert 服务管理说明"
}

begin_install_transaction() {
    install -d -o root -g "$SERVICE_GROUP" -m 0750 "$INSTALL_DIR"
    transaction_dir="$(mktemp -d "$INSTALL_DIR/.install-XXXXXXXX")"
    chmod 0700 "$transaction_dir"
    transaction_targets=(
        "$INSTALL_METADATA_FILE" "$INSTALL_DIR/scert" "$INSTALL_DIR/scert-admin"
        "$INSTALL_DIR/web" "$SERVICE_DIR/Scert.service" "$SERVICE_DIR/Scert-admin.service"
        "$SERVICE_DROP_IN_DIR/10-certificate-path.conf"
        "$INSTALL_DIR/docs/Scert-service-management.md"
        "$INSTALL_DIR/docs/Scert-admin-service-management.md"
    )
    local index target
    for index in "${!transaction_targets[@]}"; do
        target="${transaction_targets[$index]}"
        printf '%s\n' "$target" >> "$transaction_dir/targets.txt"
        if [[ -e "$target" ]]; then
            cp -a -- "$target" "$transaction_dir/before-$index"
        fi
    done
    transaction_active=true
}

finish_install_transaction() {
    local status=$?
    trap - EXIT INT TERM
    if [[ "$transaction_active" != "true" ]]; then
        exit "$status"
    fi
    set +e
    local index target
    local rollback_failed=false
    for ((index=${#transaction_targets[@]}-1; index>=0; index--)); do
        target="${transaction_targets[$index]}"
        if [[ -e "$target" || -L "$target" ]]; then
            if ! mv -T -- "$target" "$transaction_dir/failed-$index"; then
                printf '无法保留失败文件，请人工恢复：%s\n' "$target" >&2
                rollback_failed=true
                continue
            fi
        fi
        if [[ -e "$transaction_dir/before-$index" ]]; then
            if ! mv -T -- "$transaction_dir/before-$index" "$target"; then
                printf '无法恢复升级前文件，请人工恢复：%s\n' "$target" >&2
                rollback_failed=true
            fi
        fi
    done
    if ! reload_systemd; then
        rollback_failed=true
    fi
    if [[ "$rollback_failed" == "true" ]]; then
        printf '安装回退不完整，禁止启动服务。恢复记录：%s\n' "$transaction_dir" >&2
    else
        printf '安装失败，已恢复升级前发布文件，服务保持停止。失败现场：%s\n' "$transaction_dir" >&2
    fi
    [[ "$status" -ne 0 ]] || status=1
    exit "$status"
}

ensure_services_stopped() {
    if systemctl is-active --quiet Scert.service; then
        fail "升级或安装前必须停止 Scert.service"
    fi
    if systemctl is-active --quiet Scert-admin.service; then
        fail "升级或安装前必须停止 Scert-admin.service"
    fi
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
    local temporary_file="$INSTALL_METADATA_FILE.new.$$"
    printf '{\n  "certificate_root": "%s"\n}\n' "$certificate_dir" > "$temporary_file"
    chown root:"$SERVICE_GROUP" "$temporary_file"
    chmod 0640 "$temporary_file"
    mv -fT -- "$temporary_file" "$INSTALL_METADATA_FILE"
}

install_binaries() {
    local main_temporary="$INSTALL_DIR/.scert.new.$$"
    local admin_temporary="$INSTALL_DIR/.scert-admin.new.$$"
    install -o root -g root -m 0755 "$source_dir/scert" "$main_temporary"
    install -o root -g root -m 0755 "$source_dir/scert-admin" "$admin_temporary"
    mv -fT -- "$main_temporary" "$INSTALL_DIR/scert"
    mv -fT -- "$admin_temporary" "$INSTALL_DIR/scert-admin"
}

install_web() {
    local staged_web="$transaction_dir/web-new"
    cp -a -- "$source_dir/web" "$staged_web"
    find "$staged_web" -type d -exec chown root:root {} + -exec chmod 0755 {} +
    find "$staged_web" -type f -exec chown root:root {} + -exec chmod 0644 {} +
    mv -T -- "$INSTALL_DIR/web" "$transaction_dir/web-replaced"
    mv -T -- "$staged_web" "$INSTALL_DIR/web"
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
    ensure_services_stopped
    ensure_service_account
    trap finish_install_transaction EXIT
    trap 'exit 130' INT
    trap 'exit 143' TERM
    begin_install_transaction
    create_directories
    write_install_metadata
    install_binaries
    install_web
    install_service_files
    reload_systemd
    transaction_active=false
    printf '升级前发布文件已保留：%s\n' "$transaction_dir"
    print_completion
}

main "$@"
