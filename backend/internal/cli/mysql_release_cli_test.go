package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// 独立发行程序不依赖测试进程的 TLS 注册表；仅向子进程提供隔离实例的信任证书。
func TestMySQLRealReleaseBackupRestoreCommands(t *testing.T) {
	binary := os.Getenv("SCERT_MYSQL_TEST_CLI")
	if binary == "" {
		t.Skip("未启用 MySQL 独立发行程序验收")
	}
	if runtime.GOOS != "linux" {
		t.Fatal("独立发行程序验收要求 Linux 系统证书加载语义")
	}
	info, err := os.Stat(binary)
	if err != nil || !filepath.IsAbs(binary) || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		t.Fatal("必须提供可执行的正式 Linux 发行程序绝对路径")
	}
	t.Logf("独立发行程序：%s；使用严格 TLS 和加密数据库凭据", binary)
	testMySQLBackupRestoreCommands(t, binary)
}

func runMySQLReleaseCLI(t *testing.T, binary, configuration, certificate, password string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, binary, append([]string{"--config", configuration}, args...)...)
	command.Env = append(os.Environ(), "SSL_CERT_FILE="+certificate, "SSL_CERT_DIR="+filepath.Dir(certificate))
	command.WaitDelay = 2 * time.Second
	output, err := command.CombinedOutput()
	if strings.Contains(string(output), password) {
		t.Fatal("独立发行程序输出不得泄漏数据库密码")
	}
	if err != nil {
		if ctx.Err() != nil {
			t.Fatalf("独立发行程序执行超时或取消：%v", ctx.Err())
		}
		return string(output), errors.New(string(output))
	}
	return string(output), nil
}
