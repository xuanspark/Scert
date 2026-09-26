"""在私有挂载命名空间中验证固定路径安装器，不操作正式服务。"""

import argparse
import hashlib
import json
import os
import pwd
from pathlib import Path
import subprocess
import sys
import tempfile


SERVICE_STATES = {
    "service-active": ("loaded", "active", "running", "51", "0", ""),
    "service-activating": ("loaded", "activating", "start", "0", "52", "7"),
    "service-deactivating": ("loaded", "deactivating", "stop-sigterm", "51", "0", "8"),
    "service-reloading": ("loaded", "reloading", "reload", "51", "52", "9"),
    "service-failed-process": ("loaded", "failed", "failed", "51", "0", ""),
    "service-pending-job": ("loaded", "inactive", "dead", "0", "0", "10"),
    "service-masked": ("masked", "inactive", "dead", "0", "0", ""),
    "service-not-found": ("not-found", "inactive", "dead", "0", "0", ""),
    "service-failed-stopped": ("loaded", "failed", "failed", "0", "0", ""),
}
SERVICE_QUERY_SCENARIOS = [
    "service-query-failure", "service-empty-output", "service-missing-property",
    "admin-deactivating", "admin-query-failure",
]
SERVICE_GUARDS = [
    name for name in SERVICE_STATES
    if name not in ["service-not-found", "service-failed-stopped"]
] + SERVICE_QUERY_SCENARIOS
SUCCESS_SCENARIOS = ["success", "fresh-success", "service-not-found", "service-failed-stopped"]
SCENARIOS = [
    "web-link", "binary-directory", "unit-failure", "reload-failure",
    "rollback-reload-failure", "success", "fresh-failure", "fresh-success",
] + list(SERVICE_STATES) + SERVICE_QUERY_SCENARIOS


def run(*args, **kwargs):
    return subprocess.run(args, check=True, text=True, **kwargs)


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def snapshot(root):
    result = {}
    for path in sorted(root.rglob("*")):
        info = path.lstat()
        relative = str(path.relative_to(root))
        if path.is_symlink():
            result[relative] = ["link", os.readlink(path), info.st_uid, info.st_gid]
        elif path.is_file():
            result[relative] = [digest(path), info.st_mode & 0o777, info.st_uid, info.st_gid]
        else:
            result[relative] = ["directory", info.st_mode & 0o777, info.st_uid, info.st_gid]
    return result


def child(args):
    base = Path(args.child).resolve(strict=True)
    marker = base / "namespace-marker"
    if marker.read_text() != "scert-installer-test\n":
        raise RuntimeError("隔离目录标识不正确")
    source = base / "source"
    run("mount", "--bind", str(Path(args.source).resolve(strict=True)), str(source))
    run("mount", "-o", "remount,bind,ro", str(source))
    for name, target in [("install", "/scert"), ("units", "/etc/systemd/system"), ("certificates", args.certificate_root)]:
        run("mount", "--bind", str(base / name), target)
        if os.stat(base / name).st_ino != os.stat(target).st_ino:
            raise RuntimeError("隔离挂载未生效")
    environment = dict(os.environ, PATH=str(base / "commands") + ":/usr/sbin:/usr/bin:/sbin:/bin")
    command = ["bash", str(source / "install-linux.sh"), "--source-dir", str(source)]
    if not (base / "install/conf/installation.json").exists():
        command.extend(["--certificate-dir", args.certificate_root])
    result = subprocess.run(
        command,
        env=environment, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
    )
    (base / "output.txt").write_text(result.stdout)
    (base / "result.json").write_text(json.dumps({"exit_code": result.returncode}))


def prepare(base, scenario, certificate_root):
    for name in ["install/conf", "install/data", "install/log", "install/backups", "install/docs", "install/web/assets", "units/Scert.service.d", "certificates", "commands", "source"]:
        (base / name).mkdir(parents=True, exist_ok=True)
    (base / "namespace-marker").write_text("scert-installer-test\n")
    for name in ["scert", "scert-admin", "docs/Scert-service-management.md", "docs/Scert-admin-service-management.md", "web/index.html", "web/assets/old.js"]:
        (base / "install" / name).write_text("old:" + name)
    (base / "install/conf/installation.json").write_text(json.dumps({"certificate_root": certificate_root}) + "\n")
    for name in ["conf/scert.json", "conf/master.key", "conf/secrets.json", "data/scert.db", "backups/keep"]:
        (base / "install" / name).write_text("preserve:" + name)
    (base / "certificates/keep.pem").write_text("preserve:certificate")
    account = pwd.getpwnam("Scert")
    os.chown(base / "certificates", account.pw_uid, account.pw_gid)
    (base / "certificates").chmod(0o700)
    for name in ["Scert.service", "Scert-admin.service", "Scert.service.d/10-certificate-path.conf"]:
        (base / "units" / name).write_text("old:" + name)
    command_log = str(base / "systemctl.log")
    counter = str(base / "reload-count")
    systemctl = base / "commands/systemctl"
    properties = ["LoadState", "ActiveState", "SubState", "MainPID", "ControlPID", "Job"]
    states = SERVICE_STATES.get(scenario, ("loaded", "inactive", "dead", "0", "0", ""))
    if scenario == "admin-deactivating":
        states = SERVICE_STATES["service-deactivating"]
    state_output = "\n".join(key + "=" + value for key, value in zip(properties, states))
    if scenario == "service-missing-property":
        state_output = "\n".join(state_output.splitlines()[:-1])
    service_name = "Scert-admin.service" if scenario.startswith("admin-") else "Scert.service"
    state_command = "printf '%s\\n' '" + state_output + "'; exit 0\n"
    if scenario in ["service-query-failure", "admin-query-failure"]:
        state_command = "exit 1\n"
    if scenario == "service-empty-output":
        state_command = "exit 0\n"
    systemctl.write_text(
        '#!/usr/bin/env bash\nset -eu\n'
        f'printf "%s\\n" "$*" >> "{command_log}"\n'
        'if [[ "$1" == "is-active" ]]; then '
        + ('exit 0; fi\n' if scenario == "service-active" else 'exit 3; fi\n')
        + 'if [[ "$1" == "show" ]]; then\n'
        + f'if [[ "$2" == "{service_name}" ]]; then\n' + state_command + 'fi\n'
        + 'if [[ "$2" == "Scert-storage.socket" ]]; then\n'
        + "printf '%s\\n' 'LoadState=loaded' 'ActiveState=inactive' 'SubState=dead' 'ControlPID=0' 'Job='; exit 0\nfi\n"
        + "printf '%s\\n' 'LoadState=loaded' 'ActiveState=inactive' 'SubState=dead' 'MainPID=0' 'ControlPID=0' 'Job='; exit 0\nfi\n"
        + '[[ "$*" == "daemon-reload" ]] || exit 97\n'
        + (f'if [[ ! -e "{counter}" ]]; then touch "{counter}"; exit 42; fi\n' if scenario == "reload-failure" else "")
        + ('exit 42\n' if scenario == "rollback-reload-failure" else "")
    )
    systemctl.chmod(0o755)
    if scenario == "web-link":
        (base / "install/web/assets/link").symlink_to(base / "certificates/keep.pem")
    if scenario == "binary-directory":
        (base / "install/scert").rename(base / "old-scert")
        (base / "install/scert").mkdir()
    if scenario in ["unit-failure", "fresh-failure"]:
        command = base / "commands/install"
        command.write_text(
            '#!/usr/bin/env bash\nset -eu\n'
            'if [[ "${@: -1}" == "/etc/systemd/system/Scert-admin.service" ]]; then exit 43; fi\n'
            'exec /usr/bin/install "$@"\n'
        )
        command.chmod(0o755)
    if scenario in ["fresh-failure", "fresh-success"]:
        (base / "install").rename(base / "unused-install-fixture")
        (base / "install").mkdir()
        (base / "units").rename(base / "unused-units-fixture")
        (base / "units").mkdir()


def parent(args):
    if sys.platform != "linux" or os.geteuid() != 0:
        raise RuntimeError("测试需要 Linux root 和私有挂载命名空间")
    for target in ["/scert", args.certificate_root, "/etc/systemd/system"]:
        if not Path(target).is_dir() or Path(target).is_symlink():
            raise RuntimeError("测试挂载目标必须是既有普通目录")
    run("getent", "passwd", "Scert", stdout=subprocess.DEVNULL)
    run("unshare", "--mount", "--propagation", "private", "true")
    output = Path(args.output).resolve(strict=True)
    for target in [Path("/scert"), Path(args.certificate_root), Path("/etc/systemd/system")]:
        if output == target or target in output.parents:
            raise RuntimeError("测试产物不能位于被隔离的挂载点内")
    source = Path(args.source).resolve(strict=True)
    failures = []
    for scenario in args.scenarios.split(","):
        if scenario not in SCENARIOS:
            raise RuntimeError("未知测试场景")
        base = Path(tempfile.mkdtemp(prefix=scenario + "-", dir=output))
        prepare(base, scenario, args.certificate_root)
        protected = ["conf/scert.json", "conf/master.key", "conf/secrets.json", "data/scert.db", "backups/keep"]
        if scenario.startswith("fresh-"):
            protected = []
        before_protected = {name: digest(base / "install" / name) for name in protected}
        before_install = snapshot(base / "install")
        before_units = snapshot(base / "units")
        before_certificates = snapshot(base / "certificates")
        run("unshare", "--mount", "--propagation", "private", sys.executable, str(Path(__file__).resolve()),
            "--source", str(source), "--child", str(base), "--certificate-root", args.certificate_root)
        code = json.loads((base / "result.json").read_text())["exit_code"]
        errors = []
        if (code == 0) != (scenario in SUCCESS_SCENARIOS):
            errors.append("退出码不符合预期")
        if {name: digest(base / "install" / name) for name in protected} != before_protected:
            errors.append("业务文件被修改")
        if snapshot(base / "certificates") != before_certificates:
            errors.append("证书文件被修改")
        if scenario in SERVICE_GUARDS and snapshot(base / "install") != before_install:
            errors.append("停服检查拒绝前已修改安装目录")
        if scenario in SUCCESS_SCENARIOS:
            for name in ["scert", "scert-admin", "web/index.html"]:
                if digest(base / "install" / name) != digest(source / name):
                    errors.append("安装文件内容不正确：" + name)
            if (base / "install/web/assets/old.js").exists():
                errors.append("旧前端资源仍在生效目录")
        else:
            after = snapshot(base / "install")
            for name, expected in before_install.items():
                # 安装器会统一业务目录权限，仅比较发布文件与安装记录。
                if name in ["scert", "scert-admin", "conf/installation.json"] or name.startswith(("web/", "docs/")):
                    if after.get(name) != expected:
                        errors.append("失败后发布文件未保留：" + name)
            if scenario == "fresh-failure":
                for name in ["scert", "scert-admin", "web", "conf/installation.json"]:
                    if (base / "install" / name).exists():
                        errors.append("首次安装失败仍残留生效文件：" + name)
                if any(path.is_file() for path in (base / "units").rglob("*")):
                    errors.append("首次安装失败仍残留服务文件")
            elif snapshot(base / "units") != before_units:
                errors.append("失败后服务文件未恢复")
        for path in (base / "install").glob(".install-*"):
            if path.stat().st_mode & 0o777 != 0o700 or path.stat().st_uid != 0:
                errors.append("升级副本目录权限不正确")
        text = (base / "output.txt").read_text()
        if scenario == "rollback-reload-failure" and "安装回退不完整，禁止启动服务" not in text:
            errors.append("回退失败缺少明确提示")
        if scenario in ["unit-failure", "reload-failure", "fresh-failure"] and "已恢复升级前发布文件" not in text:
            errors.append("缺少回退完成记录")
        command_log = base / "systemctl.log"
        calls = command_log.read_text().splitlines() if command_log.exists() else []
        allowed = {"is-active --quiet Scert.service", "is-active --quiet Scert-admin.service", "daemon-reload"}
        allowed.update("show " + name + " -p LoadState -p ActiveState -p SubState -p MainPID -p ControlPID -p Job" for name in ["Scert.service", "Scert-admin.service", "Scert-storage.service", "Scert-storage.socket"])
        if any(call not in allowed for call in calls):
            errors.append("出现不允许的服务操作")
        if scenario in SERVICE_GUARDS and "daemon-reload" in calls:
            errors.append("停服检查失败仍加载服务配置")
        report = {"scenario": scenario, "exit_code": code, "errors": errors, "artifacts": str(base)}
        (base / "report.json").write_text(json.dumps(report, ensure_ascii=False, indent=2))
        print(json.dumps(report, ensure_ascii=False), flush=True)
        failures.extend(errors)
    if failures:
        raise SystemExit(1)


parser = argparse.ArgumentParser()
parser.add_argument("--source", required=True)
parser.add_argument("--output")
parser.add_argument("--child")
parser.add_argument("--certificate-root", required=True)
parser.add_argument("--scenarios", default=",".join(SCENARIOS))
arguments = parser.parse_args()
if arguments.child:
    child(arguments)
else:
    parent(arguments)
