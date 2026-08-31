package admin

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestLinuxInstallerPolicy(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("无法定位测试源文件")
	}
	installerPath := filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", "deploy", "install-linux.sh")
	data, err := os.ReadFile(installerPath)
	if err != nil {
		t.Fatalf("读取 Linux 安装器失败：%v", err)
	}
	content := string(data)

	required := []string{
		`readonly INSTALL_DIR="/scert"`,
		`readonly DEFAULT_CERTIFICATE_DIR="/ssl"`,
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
		`load_existing_certificate_dir`,
		`certificate_dir_explicit=true`,
		`file -b -- "$source_dir/$artifact"`,
		`systemctl is-active --quiet Scert.service`,
		`systemctl is-active --quiet Scert-admin.service`,
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
