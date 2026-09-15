package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

func TestApplyRequiresVerifiedDNS(t *testing.T) {
	cfg := config.Default(t.TempDir())
	err := Apply(&cfg, Input{
		AdminUsername: "admin", AdminEmail: "admin@example.com", AdminPassword: "password123", ServerPort: 8080,
		DNSProvider: "cloudflare", DNSCredentialRef: "credential-1", CertificateRoot: cfg.Paths.Certificate,
		WebHostname: "panel.example.com", WebEnabled: true,
	})
	if err == nil {
		t.Fatal("未通过 DNS 验证时不应完成初始化")
	}
}

func TestApplyBuildsPendingConfig(t *testing.T) {
	cfg := config.Default(t.TempDir())
	err := Apply(&cfg, Input{
		AdminUsername: "admin", AdminEmail: "admin@example.com", AdminPassword: "password123", ServerPort: 8080,
		EmailProvider: "mail-api", EmailEndpoint: "https://mail.example.test/send", EmailRecipients: []string{"a@example.test,b@example.test", "a@example.test"},
		DNSProvider: "cloudflare", DNSCredentialRef: "credential-1", DNSVerified: true, CertificateRoot: cfg.Paths.Certificate,
		PropagationTimeoutSecs: 1200, PropagationPollingIntervalSecs: 15,
		WebHostname: "panel.example.com", WebEnabled: true,
	})
	if err != nil {
		t.Fatalf("初始化配置失败：%v", err)
	}
	if cfg.SetupReady || cfg.Web.InitReady || cfg.DNS.VerificationState != "verified" || cfg.Admin.Username != "admin" {
		t.Fatalf("初始化状态不正确：%+v", cfg)
	}
	if len(cfg.Notification.Recipients) != 2 {
		t.Fatalf("收件人去重失败：%v", cfg.Notification.Recipients)
	}
	if cfg.Admin.PasswordHash == "password123" || cfg.Admin.PasswordHash == "" {
		t.Fatal("管理员密码不应明文保存")
	}
	if cfg.ResetURL() != "https://panel.example.com/reset" {
		t.Fatalf("密码重置地址生成错误：%s", cfg.ResetURL())
	}
	if cfg.ACME.PropagationTimeoutSecs != 1200 || cfg.ACME.PropagationPollingIntervalSecs != 15 {
		t.Fatalf("DNS 传播等待配置保存错误：timeout=%d interval=%d", cfg.ACME.PropagationTimeoutSecs, cfg.ACME.PropagationPollingIntervalSecs)
	}
}

func TestApplyRejectsInvalidPropagationSettings(t *testing.T) {
	cases := []struct {
		name     string
		timeout  int
		interval int
	}{
		{name: "等待时间小于最小值", timeout: 0 - 1, interval: 10},
		{name: "等待时间超过最大值", timeout: 86401, interval: 10},
		{name: "检测间隔超过等待时间", timeout: 900, interval: 901},
	}

	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			cfg := config.Default(t.TempDir())
			err := Apply(&cfg, Input{
				AdminUsername:                  "admin",
				AdminEmail:                     "admin@example.com",
				AdminPassword:                  "password123",
				ServerPort:                     8080,
				DNSProvider:                    "cloudflare",
				DNSCredentialRef:               "credential-1",
				DNSVerified:                    true,
				PropagationTimeoutSecs:         item.timeout,
				PropagationPollingIntervalSecs: item.interval,
				WebHostname:                    "panel.example.com",
				WebEnabled:                     true,
			})
			if err == nil {
				t.Fatal("非法 DNS 传播等待配置不应完成初始化")
			}
		})
	}
}
func TestApplyRejectsWebIPAsHostnamePair(t *testing.T) {
	cfg := config.Default(t.TempDir())
	err := Apply(&cfg, Input{
		AdminUsername: "admin", AdminEmail: "admin@example.com", AdminPassword: "password123", ServerPort: 8080,
		DNSProvider: "cloudflare", DNSCredentialRef: "credential-1", DNSVerified: true,
		WebHostname: "panel.example.com", WebPublicIP: "203.0.113.10", WebEnabled: true,
	})
	if err == nil {
		t.Fatal("域名和公网 IP 同时配置时应被拒绝")
	}
}

func TestApplyUsesAdminEmailAndStoresNotificationOptions(t *testing.T) {
	cfg := config.Default(t.TempDir())
	err := Apply(&cfg, Input{
		AdminUsername: "admin", AdminEmail: "admin@example.com", AdminPassword: "password123", ServerPort: 8080,
		EmailHTTPEnabled: true, EmailProvider: "smail", EmailEndpoint: "https://mail.example.test",
		EmailHTTPPath: "/api/email/send", EmailHTTPAuthMode: "bearer", EmailHTTPCredentialRef: "secret://mail/token",
		EmailHTTPHeaders: map[string]string{" X-Tenant ": " scert "}, EmailEventMode: "detailed",
		EmailIncludeDeploymentDetails: true, EmailIncludeCertificateSerial: true, EmailIncludeManagementURL: true,
		EmailSenderName: "证书管理", EmailTimeoutSecs: 15, EmailRetryCount: 2, EmailRetryIntervalsSecs: []int{10, 30},
		DNSProvider: "cloudflare", DNSCredentialRef: "credential-1", DNSVerified: true, CertificateRoot: cfg.Paths.Certificate,
		WebHostname: "panel.example.com", WebEnabled: true,
	})
	if err != nil {
		t.Fatalf("保存邮件通知选项失败：%v", err)
	}
	if len(cfg.Notification.Recipients) != 1 || cfg.Notification.Recipients[0] != "admin@example.com" {
		t.Fatalf("未填写收件人时应使用管理员邮箱：%v", cfg.Notification.Recipients)
	}
	if cfg.Notification.HTTPCredentialRef != "secret://mail/token" || cfg.Notification.HTTPHeaders["X-Tenant"] != "scert" {
		t.Fatalf("HTTP 通知配置未正确保存：%+v", cfg.Notification)
	}
}

func TestApplyAndSaveStoresOnlyCredentialReference(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default(root)
	credentialRef := "secret://dns/cloudflare/test"
	secretValue := "test-dns-token"
	if err := Apply(&cfg, Input{
		AdminUsername:    "admin",
		AdminEmail:       "admin@example.com",
		AdminPassword:    "password123",
		ServerPort:       8080,
		DNSProvider:      "cloudflare",
		DNSCredentialRef: credentialRef,
		DNSVerified:      true,
		CertificateRoot:  cfg.Paths.Certificate,
		WebHostname:      "panel.example.com",
		WebEnabled:       true,
	}); err != nil {
		t.Fatalf("构造初始化配置失败：%v", err)
	}

	path := filepath.Join(root, "config", "scert.json")
	if err := cfg.Save(path); err != nil {
		t.Fatalf("保存初始化配置失败：%v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取初始化配置失败：%v", err)
	}
	content := string(data)
	if !strings.Contains(content, credentialRef) {
		t.Fatalf("初始化配置应保存凭据引用：%s", content)
	}
	if strings.Contains(content, secretValue) || strings.Contains(content, "api_token") {
		t.Fatalf("初始化配置不应保存 DNS 明文凭据：%s", content)
	}

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("重新读取初始化配置失败：%v", err)
	}
	if loaded.DNS.CredentialRef != credentialRef {
		t.Fatalf("重新读取后凭据引用不正确：%q", loaded.DNS.CredentialRef)
	}
}
