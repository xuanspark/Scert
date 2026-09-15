package deployment

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/secretstore"
)

// TestRemoteDeploymentIsolatedAcceptance 仅在显式配置后运行，不启动业务数据库、DNS 或 ACME。
// 每次使用全新隔离目录，保留验收产物；不执行目标机已有服务的校验、重载或重启。
func TestRemoteDeploymentIsolatedAcceptance(t *testing.T) {
	if os.Getenv("SCERT_ACCEPTANCE_CONFIRM") != "isolated-directory" {
		t.Skip("未启用隔离实机部署验收")
	}
	cfg, err := config.Load(os.Getenv("SCERT_ACCEPTANCE_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"master.key", "secrets.json"} {
		info, err := os.Lstat(filepath.Join(cfg.Paths.Config, name))
		if err != nil || !info.Mode().IsRegular() {
			t.Fatalf("现有凭据文件不可用：%s", name)
		}
	}
	store, err := secretstore.Open(cfg.Paths.Config)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(os.Getenv("SCERT_ACCEPTANCE_TARGET"))
	if err != nil {
		t.Fatal(err)
	}
	var server Server
	if err := json.Unmarshal(data, &server); err != nil {
		t.Fatal(err)
	}
	root := "/etc/ssl/scert-acceptance-" + time.Now().UTC().Format("20060102-150405.000000000")
	server.CertificatePath = path.Join(root, "cert.pem")
	server.CertificateChainPath = path.Join(root, "fullchain.pem")
	server.PrivateKeyPath = path.Join(root, "key.pem")
	server.ValidationCommand = []string{"/usr/bin/true"}
	server.ReloadCommand = []string{"/usr/bin/true"}
	factory := SSHFactory{Secrets: store, Timeout: 10 * time.Second}
	client, err := factory.Connect(t.Context(), server)
	if err != nil {
		t.Fatal(err)
	}
	exists, err := client.Exists(t.Context(), root)
	_ = client.Close()
	if err != nil || exists {
		t.Fatalf("隔离目录必须尚不存在：exists=%t err=%v", exists, err)
	}
	t.Logf("隔离目标：%s，目录：%s；命令使用 true，不代表真实服务重载验收", server.Address, root)
	service := NewService(factory)
	original := testArtifact(t)
	updated := testArtifact(t)
	for index, artifact := range []Artifact{original, updated} {
		report := service.Deploy(t.Context(), []Target{{Server: server}}, artifact)
		if report.Status != "success" {
			t.Fatalf("第 %d 次隔离部署失败：%+v", index+1, report)
		}
		t.Logf("隔离部署 %d 成功，事务：%s", index+1, report.Results[0].BackupID)
	}
	verify := func() {
		t.Helper()
		client, err := factory.Connect(t.Context(), server)
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()
		adapter := client.(*sshClientAdapter)
		for filename, expected := range map[string][]byte{"cert.pem": updated.Certificate, "fullchain.pem": updated.FullChain, "key.pem": updated.PrivateKey} {
			file, err := adapter.sftp.Open(path.Join(root, filename))
			if err != nil {
				t.Fatal(err)
			}
			actual, err := io.ReadAll(file)
			_ = file.Close()
			if err != nil || !bytes.Equal(actual, expected) {
				t.Fatalf("文件内容不一致：%s %v", filename, err)
			}
			info, err := adapter.sftp.Stat(path.Join(root, filename))
			if err != nil {
				t.Fatal(err)
			}
			wantMode := os.FileMode(0o644)
			if filename == "key.pem" {
				wantMode = 0o600
			}
			if info.Mode().Perm() != wantMode {
				t.Fatalf("文件权限错误：%s %o", filename, info.Mode().Perm())
			}
			if filename != "key.pem" {
				t.Logf("内容与权限验证：%s SHA256=%x mode=%o", filename, sha256.Sum256(actual), wantMode)
			}
		}
		files, err := adapter.sftp.ReadDir(root)
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			if strings.Contains(file.Name(), ".scert-tmp-") {
				t.Fatalf("残留临时文件：%s", file.Name())
			}
			if strings.HasSuffix(file.Name(), ".scert-lock") {
				t.Fatalf("残留部署锁：%s", file.Name())
			}
			if strings.HasPrefix(file.Name(), "key.pem") && file.Mode().Perm() != 0o600 {
				t.Fatal("私钥或私钥备份权限不安全")
			}
		}
	}
	verify()
	// 真实连接竞争同一文件锁时必须拒绝部署，也不得释放另一事务的锁。
	lockClient, err := factory.Connect(t.Context(), server)
	if err != nil {
		t.Fatal(err)
	}
	lockPath := server.PrivateKeyPath + ".scert-lock"
	if err := lockClient.Lock(t.Context(), lockPath); err != nil {
		_ = lockClient.Close()
		t.Fatal(err)
	}
	lockedReport := service.Deploy(t.Context(), []Target{{Server: server}}, original)
	lockExists, lockErr := lockClient.Exists(t.Context(), lockPath)
	unlockErr := lockClient.Unlock(t.Context(), lockPath)
	_ = lockClient.Close()
	if lockedReport.Status != "failed" || !strings.Contains(lockedReport.Results[0].Message, "无法取得部署锁") || !lockExists || lockErr != nil || unlockErr != nil {
		t.Fatalf("真实部署锁冲突未安全拒绝：%+v exists=%t check=%v unlock=%v", lockedReport, lockExists, lockErr, unlockErr)
	}
	t.Log("真实部署锁冲突已拒绝，其他事务的锁保持不变")
	verify()
	for _, scenario := range []string{"validation_failure", "reload_failure", "validation_timeout"} {
		candidate := server
		ctx := t.Context()
		cancel := func() {}
		switch scenario {
		case "validation_failure":
			candidate.ValidationCommand = []string{"/usr/bin/false"}
		case "reload_failure":
			candidate.ReloadCommand = []string{"/usr/bin/false"}
		case "validation_timeout":
			candidate.ValidationCommand = []string{"/usr/bin/sleep", "30"}
			ctx, cancel = context.WithTimeout(ctx, 4*time.Second)
		}
		report := service.Deploy(ctx, []Target{{Server: candidate}}, original)
		cancel()
		if report.Status != "failed" || !strings.Contains(report.Results[0].Message, "文件已回滚") || strings.Contains(report.Results[0].Message, "自动回滚失败") {
			t.Fatalf("故障场景未安全回滚 %s：%+v", scenario, report)
		}
		if scenario == "validation_timeout" && !strings.Contains(report.Results[0].Message, "deadline exceeded") {
			t.Fatalf("必须实际触发超时：%+v", report)
		}
		t.Logf("故障场景 %s：%s", scenario, report.Results[0].Message)
		verify()
	}
}
