package admin

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestLinuxInstallerStoppedUnitStates(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip(err)
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "deploy", "install-linux.sh"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	start := strings.Index(content, "ensure_services_stopped() {")
	if start < 0 {
		t.Fatal("ensure_services_stopped")
	}
	end := strings.Index(content[start:], "\n}\n")
	if end < 0 {
		t.Fatal("ensure_services_stopped")
	}
	guard := content[start : start+end+3]
	stopped := "LoadState=loaded\nActiveState=inactive\nSubState=dead\nControlPID=0\nJob=\n"
	for _, tc := range []struct {
		name       string
		unit       string
		properties string
		allowed    bool
	}{
		{"socket-missing-main-pid", "Scert-storage.socket", stopped, true},
		{"socket-zero-main-pid", "Scert-storage.socket", stopped + "MainPID=0\n", true},
		{"socket-active", "Scert-storage.socket", strings.ReplaceAll(strings.ReplaceAll(stopped, "inactive", "active"), "dead", "listening"), false},
		{"socket-control-process", "Scert-storage.socket", strings.ReplaceAll(stopped, "ControlPID=0", "ControlPID=52"), false},
		{"socket-missing-control-pid", "Scert-storage.socket", strings.ReplaceAll(stopped, "ControlPID=0\n", ""), false},
		{"socket-pending-job", "Scert-storage.socket", strings.ReplaceAll(stopped, "Job=", "Job=7"), false},
		{"socket-missing-job", "Scert-storage.socket", strings.ReplaceAll(stopped, "Job=\n", ""), false},
		{"socket-nonzero-main-pid", "Scert-storage.socket", stopped + "MainPID=51\n", false},
		{"service-missing-main-pid", "Scert.service", stopped, false},
		{"admin-missing-main-pid", "Scert-admin.service", stopped, false},
		{"storage-missing-main-pid", "Scert-storage.service", stopped, false},
		{"service-stopped", "Scert.service", stopped + "MainPID=0\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := `set -Eeuo pipefail
fail() { printf '%s\n' "$1" >&2; exit 1; }
systemctl() {
    [[ "$1" == show ]] || exit 97
    if [[ "$2" == "$TEST_UNIT" ]]; then
        printf '%s' "$TEST_PROPERTIES"
    else
        printf '%s' "$TEST_STOPPED"
        [[ "$2" == *.socket ]] || printf 'MainPID=0\n'
    fi
    return 0
}
` + guard + "\nensure_services_stopped\n"
			command := exec.Command(bash, "--noprofile", "--norc", "-c", script)
			command.Env = append(os.Environ(), "TEST_UNIT="+tc.unit, "TEST_PROPERTIES="+tc.properties, "TEST_STOPPED="+stopped)
			output, err := command.CombinedOutput()
			if (err == nil) != tc.allowed {
				t.Fatalf("allowed=%t, err=%v, output=%s", tc.allowed, err, output)
			}
		})
	}
}

func TestLinuxInstallerCertificateOwnership(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip(err)
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "deploy", "install-linux.sh"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	start := strings.Index(content, "validate_existing_certificate_dir() {")
	if start < 0 {
		t.Fatal("validate_existing_certificate_dir")
	}
	end := strings.Index(content[start:], "\n}\n")
	if end < 0 {
		t.Fatal("validate_existing_certificate_dir")
	}
	guard := content[start : start+end+3]
	base := t.TempDir()
	file := filepath.Join(base, "keep.pem")
	if err := os.WriteFile(file, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		path    string
		owner   string
		allowed bool
	}{
		{"new-directory", filepath.Join(base, "new"), "root:root", true},
		{"owned-directory", base, "Scert:Scert", true},
		{"shared-directory", base, "root:root", false},
		{"other-service", base, "nginx:nginx", false},
		{"wrong-group", base, "Scert:root", false},
		{"missing-owner", base, "", false},
		{"existing-file", file, "Scert:Scert", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := `set -Eeuo pipefail
SERVICE_USER=Scert
SERVICE_GROUP=Scert
certificate_dir="$TEST_PATH"
fail() { printf '%s\n' "$1" >&2; exit 1; }
stat() { printf '%s\n' "$TEST_OWNER"; }
` + guard + "\nvalidate_existing_certificate_dir\n"
			command := exec.Command(bash, "--noprofile", "--norc", "-c", script)
			command.Env = append(os.Environ(), "TEST_PATH="+filepath.ToSlash(tc.path), "TEST_OWNER="+tc.owner)
			output, err := command.CombinedOutput()
			if (err == nil) != tc.allowed {
				t.Fatalf("allowed=%t, err=%v, output=%s", tc.allowed, err, output)
			}
			preserved, err := os.ReadFile(file)
			if err != nil || string(preserved) != "preserve" {
				t.Fatalf("preserved=%q, err=%v", preserved, err)
			}
		})
	}
}

func TestLinuxInstallerPolicy(t *testing.T) {
	installerPath := filepath.Join("..", "..", "..", "deploy", "install-linux.sh")
	data, err := os.ReadFile(installerPath)
	if err != nil {
		t.Fatalf("读取 Linux 安装器失败：%v", err)
	}
	content := string(data)

	required := []string{
		`readonly INSTALL_DIR="/scert"`,
		`readonly DEFAULT_CERTIFICATE_DIR="/ssl/scert"`,
		`validate_existing_certificate_dir`,
		`readonly SERVICE_USER="Scert"`,
		`readonly INSTALL_METADATA_FILE="$INSTALL_DIR/conf/installation.json"`,
		`useradd --system`,
		`--badname`,
		`--shell "$nologin_shell"`,
		`systemctl daemon-reload`,
		`--certificate-dir`,
		`readlink -m -- "$certificate_dir"`,
		`reject_symlink_path "$certificate_dir" "证书目录"`,
		`/etc|/etc/*`,
		`/scert|/scert/*`,
		`chown -R -h "$SERVICE_USER:$SERVICE_GROUP"`,
		`write_install_metadata`,
		`begin_install_transaction`,
		`trap finish_install_transaction EXIT`,
		`mv -fT -- "$main_temporary" "$INSTALL_DIR/scert"`,
		`mv -T -- "$INSTALL_DIR/web" "$transaction_dir/web-replaced"`,
		`load_existing_certificate_dir`,
		`certificate_dir_explicit=true`,
		`file -b -- "$source_dir/$artifact"`,
		`for unit in Scert.service Scert-admin.service`,
		`systemctl show "$unit" -p LoadState -p ActiveState -p SubState -p MainPID -p ControlPID -p Job`,
		`loaded:inactive:dead|loaded:failed:failed|not-found:inactive:dead`,
		`"$main_pid" == "0" && "$control_pid" == "0" && -z "$job"`,
		`pwd -P`,
		`10-certificate-path.conf`,
		`"$INSTALL_DIR/conf"`,
		`"$INSTALL_DIR/data"`,
		`"$INSTALL_DIR/log"`,
		`"$INSTALL_DIR/backups"`,
		`"$certificate_dir"`,
	}
	for _, item := range required {
		if !strings.Contains(content, item) {
			t.Errorf("Linux 安装器缺少必要策略：%s", item)
		}
	}

	forbiddenCommands := []string{
		"systemctl enable Scert-admin",
		"systemctl start Scert-admin",
		"systemctl restart Scert-admin",
		"systemctl enable --now Scert-admin",
	}
	for _, command := range forbiddenCommands {
		if containsExecutableLine(content, command) || containsSystemctlAction(content, strings.Fields(command)[1], "Scert-admin") {
			t.Errorf("Linux 安装器不得执行命令：%s", command)
		}
	}
	for _, command := range []string{"ufw ", "firewall-cmd ", "New-NetFirewallRule"} {
		if strings.Contains(content, command) {
			t.Errorf("Linux 文件安装器不得直接配置防火墙：%s", command)
		}
	}

	if countExecutableLine(content, "systemctl daemon-reload") != 1 {
		t.Errorf("Linux 安装器必须且只能执行一次 systemctl daemon-reload")
	}
}

func TestSystemctlPolicyMatcherCoversWrappedCommands(t *testing.T) {
	for _, content := range []string{
		"systemctl --no-block start Scert-admin",
		`command systemctl restart "Scert-admin"`,
		"sudo systemctl reload Scert-admin",
	} {
		if !containsSystemctlAction(content, strings.Fields(content)[2], "Scert-admin") {
			t.Fatalf("未识别受禁止的 systemctl 调用：%s", content)
		}
	}
}

func containsExecutableLine(content, command string) bool {
	return countExecutableLine(content, command) > 0
}

func containsSystemctlAction(content, action, unit string) bool {
	pattern := `(?m)^\s*(?:command\s+|sudo\s+)?systemctl\b[^\n]*\b` + regexp.QuoteMeta(action) + `\b[^\n]*\b` + regexp.QuoteMeta(unit) + `\b`
	return regexp.MustCompile(pattern).MatchString(content)
}

func countExecutableLine(content, command string) int {
	count := 0
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if line == command {
			count++
		}
	}
	return count
}
