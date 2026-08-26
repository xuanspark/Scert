package setup

import (
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

func TestApplyRequiresVerifiedDNS(t *testing.T) {
	cfg := config.Default(t.TempDir())
	err := Apply(&cfg, Input{
		AdminUsername: "admin", AdminPassword: "password123", ServerPort: 8080, ResetServicePort: 8091,
		DNSProvider: "cloudflare", DNSCredentialRef: "credential-1", CertificateRoot: cfg.Paths.Certificate,
		WebHostname: "panel.example.com", WebEnabled: true,
	})
	if err == nil {
		t.Fatal("未通过 DNS 验证时不应完成初始化")
	}
}

func TestApplyBuildsReadyConfig(t *testing.T) {
	cfg := config.Default(t.TempDir())
	err := Apply(&cfg, Input{
		AdminUsername: "admin", AdminPassword: "password123", ServerPort: 8080, ResetServicePort: 8091,
		EmailProvider: "mail-api", EmailEndpoint: "https://mail.example.test/send", EmailRecipients: []string{"a@example.test,b@example.test", "a@example.test"},
		DNSProvider: "cloudflare", DNSCredentialRef: "credential-1", DNSVerified: true, CertificateRoot: cfg.Paths.Certificate,
		WebHostname: "panel.example.com", WebEnabled: true,
	})
	if err != nil {
		t.Fatalf("初始化配置失败：%v", err)
	}
	if !cfg.SetupReady || cfg.DNS.VerificationState != "verified" || cfg.Admin.Username != "admin" {
		t.Fatalf("初始化状态不正确：%+v", cfg)
	}
	if len(cfg.Notification.Recipients) != 2 {
		t.Fatalf("收件人去重失败：%v", cfg.Notification.Recipients)
	}
	if cfg.Admin.PasswordHash == "password123" || cfg.Admin.PasswordHash == "" {
		t.Fatal("管理员密码不应明文保存")
	}
}

func TestApplyRejectsWebIPAsHostnamePair(t *testing.T) {
	cfg := config.Default(t.TempDir())
	err := Apply(&cfg, Input{
		AdminUsername: "admin", AdminPassword: "password123", ServerPort: 8080, ResetServicePort: 8091,
		DNSProvider: "cloudflare", DNSCredentialRef: "credential-1", DNSVerified: true,
		WebHostname: "panel.example.com", WebPublicIP: "203.0.113.10", WebEnabled: true,
	})
	if err == nil {
		t.Fatal("域名和公网 IP 同时配置时应被拒绝")
	}
}
