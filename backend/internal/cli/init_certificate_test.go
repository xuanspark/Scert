package cli

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/firewall"
	"git.pcux.cn/xuans/Scert/backend/internal/secretstore"
	"git.pcux.cn/xuans/Scert/backend/internal/setup"
)

func initTestConfig(t *testing.T) config.Config {
	t.Helper()
	root := t.TempDir()
	cfg := config.Default(root)
	cfg.Paths.Config = filepath.Join(root, "conf")
	cfg.Paths.Certificate = filepath.Join(root, "certificates")
	cfg.Paths.Backup = filepath.Join(root, "backups")
	cfg.Admin.Username, cfg.Admin.Email, cfg.Admin.PasswordHash = "admin", "admin@example.com", "test-only-hash"
	cfg.Web.Hostname, cfg.Web.PublicIP = "panel.example.com", ""
	cfg.DNS.Provider, cfg.DNS.CredentialRef, cfg.DNS.VerificationState = "aliyun", "secret://dns/aliyun/init-test", "verified"
	return cfg
}

func initTestResource(t *testing.T) certificate.Resource {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), DNSNames: []string{"panel.example.com"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return certificate.Resource{
		Domains: []string{"panel.example.com"}, Certificate: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		PrivateKey: pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: private}),
	}
}

func TestCompleteInitCommitsOnlyAfterCertificateAndRollsBackFailures(t *testing.T) {
	for _, scenario := range []string{"success", "issue_failure", "invalid_files", "firewall_failure", "save_failure", "cancelled", "rollback_failure"} {
		t.Run(scenario, func(t *testing.T) {
			cfg := initTestConfig(t)
			path := filepath.Join(cfg.Paths.Config, "scert.json")
			manager := &cliFakeFirewallManager{result: firewall.Result{Created: true}}
			if scenario == "firewall_failure" {
				manager.openErr = errors.New("测试防火墙拒绝")
			}
			if scenario == "save_failure" {
				blocked := filepath.Join(t.TempDir(), "blocked")
				if err := os.WriteFile(blocked, []byte("blocked"), 0o600); err != nil {
					t.Fatal(err)
				}
				path = filepath.Join(blocked, "scert.json")
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var saved certificate.SavedResource
			issue := func(ctx context.Context, candidate config.Config) (certificate.SavedResource, error) {
				if candidate.SetupReady || candidate.Web.InitReady {
					t.Fatal("签发开始前不得标记就绪")
				}
				if scenario == "issue_failure" {
					return saved, errors.New("测试签发失败")
				}
				store := certificate.FileStore{Root: cfg.Paths.Certificate, BackupRoot: cfg.Paths.Backup}
				var err error
				saved, err = store.SaveForType(ctx, cfg.Web.Hostname, "single", initTestResource(t))
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "invalid_files" || scenario == "rollback_failure" {
					if err := os.WriteFile(saved.PrivateKeyPath, []byte("invalid-test-key"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "cancelled" {
					cancel()
				}
				return saved, nil
			}
			rolledBack := false
			rollback := func(ctx context.Context, saved certificate.SavedResource) error {
				rolledBack = true
				if ctx.Err() != nil {
					t.Fatal("回滚必须使用独立的有效上下文")
				}
				if err := certificate.RollbackSavedResource(ctx, saved); err != nil {
					return err
				}
				if scenario == "rollback_failure" {
					return errors.New("测试回滚报告失败")
				}
				return nil
			}
			_, err := completeInit(ctx, &cfg, path, manager, issue, rollback)
			if scenario == "success" {
				if err != nil || rolledBack || !cfg.SetupReady || !cfg.Web.InitReady {
					t.Fatalf("正常初始化未提交就绪状态：%v", err)
				}
				stored, err := config.Load(path)
				if err != nil || !stored.SetupReady || stored.Server.TLSCert != saved.FullChainPath || stored.Server.TLSKey != saved.PrivateKeyPath {
					t.Fatalf("证书配置未正确保存：%v", err)
				}
				return
			}
			if err == nil || cfg.SetupReady || cfg.Web.InitReady {
				t.Fatalf("失败时不得发布就绪状态：%v", err)
			}
			if _, err := os.Stat(path); err == nil {
				t.Fatal("失败时不得留下已完成的配置")
			}
			if scenario != "issue_failure" && !rolledBack {
				t.Fatal("后续阶段失败必须回滚证书")
			}
			if scenario == "save_failure" && !manager.rollbackCall {
				t.Fatal("保存失败必须回滚新建防火墙规则")
			}
			if scenario == "rollback_failure" && !strings.Contains(err.Error(), "手动检查") {
				t.Fatal("必须明确报告回滚失败")
			}
		})
	}
}

type initAuthorizationChecker struct {
	err   error
	calls int
}

func (c *initAuthorizationChecker) VerifyDomain(context.Context, string, string, map[string]string) error {
	c.calls++
	return c.err
}

func TestExistingDNSReferenceCannotBypassVerification(t *testing.T) {
	cfg := initTestConfig(t)
	store, err := secretstore.Open(cfg.Paths.Config)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(t.Context(), cfg.DNS.CredentialRef, map[string]string{"access_key_id": "test-id", "access_key_secret": "test-secret"}); err != nil {
		t.Fatal(err)
	}
	for _, denied := range []bool{false, true} {
		input := &setup.Input{DNSProvider: "aliyun", DNSCredentialRef: cfg.DNS.CredentialRef, DNSVerified: true, WebHostname: cfg.Web.Hostname}
		checker := &initAuthorizationChecker{}
		if denied {
			checker.err = errors.New("测试撤权")
		}
		cleanup, err := verifyExistingDNSCredentials(t.Context(), cfg, input, io.Discard, checker)
		cleanup()
		if checker.calls != 1 || (err != nil) != denied || input.DNSVerified == denied {
			t.Fatal("已有引用或已验证参数不得绕过当前权限检查")
		}
		if exists, err := store.Has(t.Context(), cfg.DNS.CredentialRef); err != nil || !exists {
			t.Fatal("失败清理不得删除用户原有凭据")
		}
	}
}
