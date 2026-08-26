package setup

import (
	"fmt"
	"net"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

type Input struct {
	AdminUsername    string
	AdminPassword    string
	ServerPort       int
	ResetServicePort int
	EmailProvider    string
	EmailEndpoint    string
	EmailRecipients  []string
	DNSProvider      string
	DNSCredentialRef string
	DNSVerified      bool
	CertificateRoot  string
	WebHostname      string
	WebPublicIP      string
	WebEnabled       bool
	HTTPSMode        string
}

func Apply(cfg *config.Config, input Input) error {
	if cfg == nil {
		return fmt.Errorf("配置不能为空")
	}
	if strings.TrimSpace(input.AdminUsername) == "" {
		return fmt.Errorf("超级管理员用户名不能为空")
	}
	if len(input.AdminPassword) < 8 {
		return fmt.Errorf("超级管理员密码至少需要 8 个字符")
	}
	if input.ServerPort < 1 || input.ServerPort > 65535 {
		return fmt.Errorf("主服务端口无效：%d", input.ServerPort)
	}
	if input.ResetServicePort < 1 || input.ResetServicePort > 65535 {
		return fmt.Errorf("密码重置服务端口无效：%d", input.ResetServicePort)
	}
	if input.ServerPort == input.ResetServicePort {
		return fmt.Errorf("主服务和密码重置服务不能使用相同端口")
	}
	if strings.TrimSpace(input.DNSProvider) == "" {
		return fmt.Errorf("DNS 服务商不能为空")
	}
	if strings.TrimSpace(input.DNSCredentialRef) == "" {
		return fmt.Errorf("DNS 凭据引用不能为空；明文密钥不会直接写入配置文件")
	}
	if !input.DNSVerified {
		return fmt.Errorf("DNS API 尚未通过验证测试")
	}
	if strings.TrimSpace(input.WebHostname) == "" && strings.TrimSpace(input.WebPublicIP) == "" {
		return fmt.Errorf("Web 页面必须配置域名或公网 IP")
	}
	if input.WebHostname != "" && input.WebPublicIP != "" {
		return fmt.Errorf("Web 页面不能同时配置域名和公网 IP")
	}
	if input.WebPublicIP != "" && net.ParseIP(input.WebPublicIP) == nil {
		return fmt.Errorf("Web 公网 IP 格式无效")
	}
	if input.HTTPSMode == "" {
		input.HTTPSMode = "direct"
	}
	if input.HTTPSMode != "direct" && input.HTTPSMode != "proxy" {
		return fmt.Errorf("HTTPS 模式无效：%s", input.HTTPSMode)
	}
	if strings.TrimSpace(input.CertificateRoot) == "" {
		input.CertificateRoot = cfg.Paths.Certificate
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.AdminPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("生成管理员密码摘要失败：%w", err)
	}
	cfg.Admin.Username = strings.TrimSpace(input.AdminUsername)
	cfg.Admin.PasswordHash = string(hash)
	cfg.Admin.ResetServicePort = input.ResetServicePort
	cfg.Server.Port = input.ServerPort
	cfg.Server.HTTPSMode = input.HTTPSMode
	cfg.Web.Enabled = input.WebEnabled
	cfg.Web.Hostname = strings.TrimSpace(input.WebHostname)
	cfg.Web.PublicIP = strings.TrimSpace(input.WebPublicIP)
	cfg.Web.InitReady = true
	cfg.Paths.Certificate = strings.TrimSpace(input.CertificateRoot)
	cfg.DNS.Provider = strings.TrimSpace(input.DNSProvider)
	cfg.DNS.CredentialRef = strings.TrimSpace(input.DNSCredentialRef)
	cfg.DNS.VerificationState = "verified"
	cfg.Notification.Provider = strings.TrimSpace(input.EmailProvider)
	cfg.Notification.Endpoint = strings.TrimSpace(input.EmailEndpoint)
	cfg.Notification.Recipients = cleanRecipients(input.EmailRecipients)
	cfg.SetupReady = true
	return cfg.Validate()
}

func cleanRecipients(values []string) []string {
	items := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if _, ok := seen[part]; ok {
				continue
			}
			seen[part] = struct{}{}
			items = append(items, part)
		}
	}
	return items
}
