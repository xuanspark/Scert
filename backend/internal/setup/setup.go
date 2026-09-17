package setup

import (
	"fmt"
	"net"
	"net/mail"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/passwordpolicy"
)

type Input struct {
	AdminUsername                  string
	AdminEmail                     string
	AdminPassword                  string
	ServerPort                     int
	ProxyHTTPSPort                 int
	EmailHTTPEnabled               bool
	EmailSMTPEnabled               bool
	EmailSMTPHost                  string
	EmailSMTPPort                  int
	EmailSMTPUsername              string
	EmailSMTPPassword              string
	EmailSMTPCredentialRef         string
	EmailSMTPMode                  string
	EmailSMTPAllowPlaintextAuth    bool
	EmailProvider                  string
	EmailEndpoint                  string
	EmailHTTPPath                  string
	EmailHTTPLoginPath             string
	EmailHTTPAuthMode              string
	EmailHTTPCredentialRef         string
	EmailHTTPHeaders               map[string]string
	EmailEventMode                 string
	EmailIncludeDeploymentDetails  bool
	EmailIncludeCertificateSerial  bool
	EmailIncludeManagementURL      bool
	EmailSenderName                string
	EmailSenderEmail               string
	EmailRecipients                []string
	EmailTimeoutSecs               int
	EmailRetryCount                int
	EmailRetryIntervalsSecs        []int
	DNSProvider                    string
	DNSCredentialRef               string
	DNSCredentials                 map[string]string
	DNSTestDomain                  string
	DNSVerified                    bool
	PropagationTimeoutSecs         int
	PropagationPollingIntervalSecs int
	CertificateRoot                string
	WebHostname                    string
	WebPublicIP                    string
	WebEnabled                     bool
	HTTPSMode                      string
}

func Apply(cfg *config.Config, input Input) error {
	if cfg == nil {
		return fmt.Errorf("配置不能为空")
	}
	if strings.TrimSpace(input.AdminUsername) == "" {
		return fmt.Errorf("超级管理员用户名不能为空")
	}
	adminEmail, err := normalizeAdminEmail(input.AdminEmail)
	if err != nil {
		return err
	}
	if err := passwordpolicy.Validate(input.AdminPassword, input.AdminUsername); err != nil {
		return err
	}
	if input.ServerPort < 1 || input.ServerPort > 65535 {
		return fmt.Errorf("主服务端口无效：%d", input.ServerPort)
	}
	if input.ProxyHTTPSPort < 0 || input.ProxyHTTPSPort > 65535 {
		return fmt.Errorf("代理 HTTPS 端口必须为 1 至 65535，或使用 0 表示默认 443")
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
		return fmt.Errorf("管理页面必须配置域名或公网 IP")
	}
	if input.WebHostname != "" && input.WebPublicIP != "" {
		return fmt.Errorf("管理页面不能同时配置域名和公网 IP")
	}
	if input.WebPublicIP != "" && net.ParseIP(input.WebPublicIP) == nil {
		return fmt.Errorf("管理页面公网 IP 格式无效")
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
	cfg.Admin.Email = adminEmail
	cfg.Admin.PasswordHash = string(hash)
	cfg.ACME.AccountEmail = adminEmail
	if input.PropagationTimeoutSecs != 0 {
		cfg.ACME.PropagationTimeoutSecs = input.PropagationTimeoutSecs
	}
	if input.PropagationPollingIntervalSecs != 0 {
		cfg.ACME.PropagationPollingIntervalSecs = input.PropagationPollingIntervalSecs
	}
	cfg.Server.Port = input.ServerPort
	cfg.Server.ProxyHTTPSPort = input.ProxyHTTPSPort
	cfg.Server.HTTPSMode = input.HTTPSMode
	cfg.Web.Enabled = input.WebEnabled
	cfg.Web.Hostname = strings.TrimSpace(input.WebHostname)
	cfg.Web.PublicIP = strings.TrimSpace(input.WebPublicIP)
	// 配置输入通过不等于初始化完成；调用方必须先取得并校验管理后台证书。
	cfg.Web.InitReady = false
	cfg.Paths.Certificate = strings.TrimSpace(input.CertificateRoot)
	cfg.DNS.Provider = strings.TrimSpace(input.DNSProvider)
	cfg.DNS.CredentialRef = strings.TrimSpace(input.DNSCredentialRef)
	cfg.DNS.VerificationState = "verified"
	cfg.Notification.HTTPEnabled = input.EmailHTTPEnabled
	cfg.Notification.SMTPEnabled = input.EmailSMTPEnabled
	cfg.Notification.SMTPHost = strings.TrimSpace(input.EmailSMTPHost)
	if input.EmailSMTPPort != 0 {
		cfg.Notification.SMTPPort = input.EmailSMTPPort
	}
	cfg.Notification.SMTPUsername = strings.TrimSpace(input.EmailSMTPUsername)
	cfg.Notification.SMTPCredentialRef = strings.TrimSpace(input.EmailSMTPCredentialRef)
	if strings.TrimSpace(input.EmailSMTPMode) != "" {
		cfg.Notification.SMTPMode = strings.TrimSpace(input.EmailSMTPMode)
	}
	cfg.Notification.SMTPAllowPlaintextAuth = input.EmailSMTPAllowPlaintextAuth
	cfg.Notification.Provider = strings.TrimSpace(input.EmailProvider)
	cfg.Notification.Endpoint = strings.TrimSpace(input.EmailEndpoint)
	if strings.TrimSpace(input.EmailHTTPPath) != "" {
		cfg.Notification.HTTPPath = strings.TrimSpace(input.EmailHTTPPath)
	}
	if strings.TrimSpace(input.EmailHTTPLoginPath) != "" {
		cfg.Notification.HTTPLoginPath = strings.TrimSpace(input.EmailHTTPLoginPath)
	}
	if strings.TrimSpace(input.EmailHTTPAuthMode) != "" {
		cfg.Notification.HTTPAuthMode = strings.TrimSpace(input.EmailHTTPAuthMode)
	}
	cfg.Notification.HTTPCredentialRef = strings.TrimSpace(input.EmailHTTPCredentialRef)
	if input.EmailHTTPHeaders != nil {
		cfg.Notification.HTTPHeaders = copyHeaders(input.EmailHTTPHeaders)
	}
	if strings.TrimSpace(input.EmailEventMode) != "" {
		cfg.Notification.EventMode = strings.TrimSpace(input.EmailEventMode)
	}
	cfg.Notification.IncludeDeploymentDetails = input.EmailIncludeDeploymentDetails
	cfg.Notification.IncludeCertificateSerial = input.EmailIncludeCertificateSerial
	cfg.Notification.IncludeManagementURL = input.EmailIncludeManagementURL
	if strings.TrimSpace(input.EmailSenderName) != "" {
		cfg.Notification.SenderName = strings.TrimSpace(input.EmailSenderName)
	}
	if strings.TrimSpace(input.EmailSenderEmail) != "" {
		cfg.Notification.SenderEmail = strings.ToLower(strings.TrimSpace(input.EmailSenderEmail))
	} else {
		cfg.Notification.SenderEmail = adminEmail
	}
	if input.EmailTimeoutSecs > 0 {
		cfg.Notification.TimeoutSecs = input.EmailTimeoutSecs
	}
	if input.EmailRetryCount > 0 {
		cfg.Notification.RetryCount = input.EmailRetryCount
	}
	if input.EmailRetryIntervalsSecs != nil {
		cfg.Notification.RetryIntervalsSecs = append([]int{}, input.EmailRetryIntervalsSecs...)
	}
	cfg.Notification.Recipients = cleanRecipients(input.EmailRecipients)
	if len(cfg.Notification.Recipients) == 0 {
		cfg.Notification.Recipients = []string{adminEmail}
	}
	cfg.SetupReady = false
	if cfg.Notification.SMTPEnabled && strings.TrimSpace(cfg.Notification.SMTPCredentialRef) == "" {
		return fmt.Errorf("启用 SMTP 通知后必须配置加密凭据引用")
	}
	return cfg.Validate()
}

func copyHeaders(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		result[key] = strings.TrimSpace(value)
	}
	return result
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

func normalizeAdminEmail(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "", fmt.Errorf("超级管理员绑定邮箱不能为空")
	}
	parsed, err := mail.ParseAddress(value)
	if err != nil || parsed.Address != value || parsed.Name != "" {
		return "", fmt.Errorf("超级管理员绑定邮箱格式无效")
	}
	return value, nil
}
